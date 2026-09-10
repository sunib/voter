package network

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// A real Dex, real pod traffic and a real NetworkPolicy controller. All policy
// mutations happen only in the cluster created and destroyed by run.sh.
func TestDexNetworkBoundary(t *testing.T) {
	if os.Getenv("ROOM_PASS_NETWORK_E2E") != "1" {
		t.Skip("run task test-network")
	}
	kubeconfig, err := filepath.Abs("../../.local/network-kubeconfig")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = networkingv1.AddToScheme(scheme)
	db, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	create := func(obj client.Object) {
		t.Helper()
		if err := db.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	for _, ns := range []string{"dex", "voter", "traefik-system", "web-preview-pr-123"} {
		create(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	// Reuse the real Dex image/config from the login fixture, excluding its
	// different (Room Pass only) policy. Deliberately change the pod labels: the
	// platform policy must select ALL pods in dex, independent of chart labels.
	raw, err := os.Open("../e2e/dex.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	decoder := kyaml.NewYAMLOrJSONDecoder(raw, 4096)
	for {
		var obj runtime.RawExtension
		if err := decoder.Decode(&obj); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		var meta metav1.TypeMeta
		if err := yaml.Unmarshal(obj.Raw, &meta); err != nil {
			t.Fatal(err)
		}
		switch meta.Kind {
		case "ConfigMap":
			cm := &corev1.ConfigMap{}
			if err := yaml.Unmarshal(obj.Raw, cm); err != nil {
				t.Fatal(err)
			}
			cm.Namespace = "dex"
			create(cm)
		case "Deployment":
			dep := &appsv1.Deployment{}
			if err := yaml.Unmarshal(obj.Raw, dep); err != nil {
				t.Fatal(err)
			}
			dep.Namespace = "dex"
			labels := map[string]string{"network-test": "dex"}
			dep.Spec.Selector.MatchLabels = labels
			dep.Spec.Template.Labels = labels
			create(dep)
		}
	}
	create(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "dex", Namespace: "dex"}, Spec: corev1.ServiceSpec{
		Selector: map[string]string{"network-test": "dex"}, Ports: []corev1.ServicePort{{Port: 5556, TargetPort: intstr.FromInt32(5556)}},
	}})
	run := func(args ...string) string {
		t.Helper()
		commandCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
		defer cancel()
		out, err := exec.CommandContext(commandCtx, "kubectl", append([]string{"--kubeconfig", kubeconfig}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("kubectl %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("-n", "dex", "rollout", "status", "deployment/dex", "--timeout=150s")
	pods := &corev1.PodList{}
	if err := db.List(ctx, pods, client.InNamespace("dex")); err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != 1 || pods.Items[0].Status.PodIP == "" {
		t.Fatal("expected one ready Dex pod")
	}
	targets := []string{"http://dex.dex.svc:5556", "http://" + pods.Items[0].Status.PodIP + ":5556"}
	policyPath := os.Getenv("DEX_NETWORK_POLICY_FILE")
	if policyPath == "" {
		policyPath = "dex-networkpolicy.yaml"
	}
	policyBytes, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	policy := &networkingv1.NetworkPolicy{}
	if err := yaml.UnmarshalStrict(policyBytes, policy); err != nil {
		t.Fatal(err)
	}
	if policy.Namespace != "dex" {
		t.Fatal("policy must target the dex namespace")
	}
	create(policy)
	t.Logf("testing policy from %s", policyPath)

	type source struct {
		name, ns string
		labels   map[string]string
		allowed  bool
	}
	sources := []source{
		{"room-pass", "voter", map[string]string{"app": "room-pass"}, true},
		{"traefik", "traefik-system", nil, true},
		{"ordinary", "default", nil, false},
		{"preview", "web-preview-pr-123", nil, false},
		{"same-namespace", "dex", nil, false},
		{"wrong-app", "voter", map[string]string{"app": "voter"}, false},
		{"spoofed-room-label", "web-preview-pr-123", map[string]string{"app": "room-pass"}, false},
	}
	for _, src := range sources {
		create(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: src.name, Namespace: src.ns, Labels: src.labels}, Spec: corev1.PodSpec{
			AutomountServiceAccountToken: ptr(false), Containers: []corev1.Container{{Name: "curl", Image: "curlimages/curl:8.12.1", Command: []string{"sleep", "1800"}}},
		}})
		run("-n", src.ns, "wait", "--for=condition=Ready", "pod/"+src.name, "--timeout=150s")
	}
	// Give the network controller time to observe the new endpoints before the
	// first assertion. Network engines can reject (curl 7) or drop (curl 28).
	// Every denied source must connect when the policy is removed below, so a
	// refused/dead destination cannot count as successful isolation. DNS errors,
	// missing executables and HTTP responses do not count as network denials.
	time.Sleep(5 * time.Second)
	probe := func(t *testing.T, src source, allowed bool) {
		t.Helper()
		for _, target := range targets {
			for _, path := range []string{"/.well-known/openid-configuration", "/callback/room-pass?state=forged"} {
				commandCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				out, err := exec.CommandContext(commandCtx, "kubectl", "--kubeconfig", kubeconfig, "-n", src.ns, "exec", src.name, "--", "sh", "-c",
					`code=$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' --connect-timeout 2 --max-time 3 -H 'X-Remote-User: Mallory' -H 'X-Remote-Group: system:masters' "$1"); rc=$?; printf '%s %s' "$rc" "$code"`, "probe", target+path).CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("probe execution failed: %v: %s", err, out)
				}
				result := strings.TrimSpace(string(out))
				if allowed {
					if !strings.HasPrefix(result, "0 ") || result == "0 000" || (strings.Contains(path, "openid") && result != "0 200") {
						t.Errorf("%s -> %s%s: expected reachable, got %s", src.name, target, path, result)
					}
				} else if result != "28 000" && result != "7 000" {
					t.Errorf("%s -> %s%s: expected network denial, got %s", src.name, target, path, result)
				}
			}
		}
	}
	for _, src := range sources {
		t.Run(src.name, func(t *testing.T) { probe(t, src, src.allowed) })
	}
	// An unavailable Dex or broken routing must never count as successful isolation.
	probe(t, sources[0], true)
	t.Run("policy-removal-control", func(t *testing.T) {
		if err := db.Delete(ctx, policy); err != nil {
			t.Fatal(err)
		}
		defer func() {
			policy.ResourceVersion = ""
			policy.UID = ""
			if err := db.Create(ctx, policy); err != nil {
				t.Errorf("restore policy: %v", err)
			}
		}()
		time.Sleep(5 * time.Second)
		for _, src := range sources {
			probe(t, src, true)
		}
	})
	time.Sleep(5 * time.Second)
	t.Run("restored-policy-blocks-again", func(t *testing.T) { probe(t, sources[2], false); probe(t, sources[0], true) })
	t.Logf("checked %d source identities, Service and Pod IP, discovery and forged callback", len(sources))
}

func ptr(v bool) *bool { return &v }
