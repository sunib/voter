package main

// Opt-in integration rehearsal against ONLY the disposable Room Pass fixture.
// Uses 200 real Kubernetes service-account tokens in fixture-signed Voter
// sessions. Browser tests separately exercise Dex/OIDC login and rendering.
import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type rehearsalStream struct {
	body   io.ReadCloser
	value  atomic.Value
	bytes  atomic.Int64
	resets atomic.Int64
	done   chan struct{}
	synced chan struct{}
}

func TestFixtureSharedStreamRehearsal(t *testing.T) {
	if os.Getenv("VOTER_STREAM_REHEARSAL") != "1" {
		t.Skip("set VOTER_STREAM_REHEARSAL=1 for the disposable fixture rehearsal")
	}
	raw, err := clientcmd.LoadFromFile("../room-pass/.local/kubeconfig")
	if err != nil {
		t.Fatal(err)
	}
	if raw.CurrentContext != "k3d-room-pass-e2e" {
		t.Fatal("refusing a non-fixture kubeconfig")
	}
	rc, err := clientcmd.NewDefaultClientConfig(*raw, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	rc.QPS, rc.Burst = 100, 200
	cs, err := kubernetes.NewForConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	gateway, err := exec.CommandContext(ctx, "docker", "network", "inspect", "k3d-room-pass-e2e", "--format", "{{(index .IPAM.Config 0).Gateway}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile("../room-pass/.local/tls.crt")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid fixture CA")
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(strings.TrimSpace(string(gateway)), "18443"))
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	const app = "https://app.roompass.test:18443"
	pods, err := cs.CoreV1().Pods("room-pass").List(ctx, metav1.ListOptions{LabelSelector: "app=voter"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != 1 {
		t.Fatal("rehearsal requires exactly one Voter pod after rollout")
	}
	metricsPath := "/api/v1/namespaces/room-pass/pods/" + pods.Items[0].Name + ":9090/proxy/metrics"
	fetchMetrics := func() string {
		t.Helper()
		body, err := cs.CoreV1().RESTClient().Get().AbsPath(metricsPath).DoRaw(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	metric := func(name string) int64 {
		for line := range strings.SplitSeq(fetchMetrics(), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == name {
				v, e := strconv.ParseInt(fields[1], 10, 64)
				if e != nil {
					t.Fatal(e)
				}
				return v
			}
		}
		t.Fatalf("missing metric %s", name)
		return 0
	}
	if metric("voter_stream_subscribers") != 0 {
		t.Fatal("fixture has other active streams; run without browser suites")
	}
	apiWatches := func() float64 {
		body, e := cs.CoreV1().RESTClient().Get().AbsPath("/metrics").DoRaw(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var count float64
		for line := range strings.SplitSeq(string(body), "\n") {
			if strings.HasPrefix(line, "apiserver_longrunning_requests{") && strings.Contains(line, `resource="coffeeconfigs"`) && strings.Contains(line, `verb="WATCH"`) {
				f := strings.Fields(line)
				v, e := strconv.ParseFloat(f[len(f)-1], 64)
				if e != nil {
					t.Fatal(e)
				}
				count += v
			}
		}
		return count
	}
	baselineWatches := apiWatches()
	ns := fmt.Sprintf("voter-stream-rehearsal-%d", time.Now().UnixNano())
	_, err = cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		_ = cs.RbacV1().RoleBindings("room-pass").Delete(cleanup, ns, metav1.DeleteOptions{})
		_ = cs.CoreV1().Namespaces().Delete(cleanup, ns, metav1.DeleteOptions{})
	}()
	const count = 200
	subjects := make([]rbacv1.Subject, count)
	cookies := make([]*http.Cookie, count)
	codec, err := newSessionSecureCookie(make([]byte, 32), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	for i := range count {
		name := fmt.Sprintf("viewer-%03d", i)
		_, err = cs.CoreV1().ServiceAccounts(ns).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		token, e := cs.CoreV1().ServiceAccounts(ns).CreateToken(ctx, name, &authenticationv1.TokenRequest{}, metav1.CreateOptions{})
		if e != nil {
			t.Fatal(e)
		}
		subjects[i] = rbacv1.Subject{Kind: "ServiceAccount", Name: name, Namespace: ns}
		rec := httptest.NewRecorder()
		cfg := config{ParticipantCookieName: "__Host-voter-session", SessionCookieMaxAgeSecs: 3600}
		if e = setParticipantSession(rec, cfg, codec, participantSession{IDToken: token.Status.Token, Subject: ns + ":" + name, TokenExpiry: token.Status.ExpirationTimestamp.Unix()}, time.Now()); e != nil {
			t.Fatal(e)
		}
		cookies[i] = rec.Result().Cookies()[0]
	}
	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: ns}, Subjects: subjects, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "voter"}}
	binding, err = cs.RbacV1().RoleBindings("room-pass").Create(ctx, binding, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	streams := make([]*rehearsalStream, count)
	defer func() {
		for _, s := range streams {
			if s != nil {
				_ = s.body.Close()
				<-s.done
			}
		}
	}()
	open := func(i int) (*rehearsalStream, error) {
		req, e := http.NewRequestWithContext(ctx, "GET", app+"/public/stream?group=examples.configbutler.ai&version=v1alpha1&resource=coffeeconfigs&namespace=room-pass&name=demo-coffee", nil)
		if e != nil {
			return nil, e
		}
		req.AddCookie(cookies[i])
		res, e := client.Do(req)
		if e != nil {
			return nil, e
		}
		if res.StatusCode != 200 {
			_ = res.Body.Close()
			return nil, fmt.Errorf("stream status %d", res.StatusCode)
		}
		s := &rehearsalStream{body: res.Body, done: make(chan struct{}), synced: make(chan struct{})}
		s.value.Store("")
		go func() {
			defer close(s.done)
			scanner := bufio.NewScanner(res.Body)
			scanner.Buffer(make([]byte, 4096), 1024*1024)
			var once sync.Once
			for scanner.Scan() {
				line := scanner.Text()
				s.bytes.Add(int64(len(line) + 1))
				if !strings.HasPrefix(line, "data:") {
					continue
				}
				var event struct {
					Type   string `json:"type"`
					Object struct {
						Spec struct {
							ShopName string `json:"shopName"`
						} `json:"spec"`
					} `json:"object"`
				}
				if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) != nil {
					continue
				}
				if event.Type == "reset" {
					s.resets.Add(1)
				}
				if event.Object.Spec.ShopName != "" {
					s.value.Store(event.Object.Spec.ShopName)
				}
				if event.Type == "synced" {
					once.Do(func() { close(s.synced) })
				}
			}
		}()
		select {
		case <-s.synced:
			return s, nil
		case <-s.done:
			_ = s.body.Close()
			return nil, fmt.Errorf("stream %d ended before sync", i)
		case <-ctx.Done():
			_ = s.body.Close()
			return nil, ctx.Err()
		}
	}
	openBatch := func(start, end int) {
		t.Helper()
		var wg sync.WaitGroup
		slots := make(chan struct{}, count)
		errs := make(chan error, end-start)
		for i := start; i < end; i++ {
			slots <- struct{}{}
			wg.Go(func() {
				defer func() { <-slots }()
				s, e := open(i)
				if e != nil {
					errs <- e
				} else {
					streams[i] = s
				}
			})
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Fatal(e)
		}
	}
	started := time.Now()
	openBatch(0, count)
	t.Logf("%d independently authenticated Kubernetes identities synced through ingress in %s", count, time.Since(started))
	if metric("voter_stream_subscribers") != count || metric("voter_stream_upstream_watches") != 1 {
		t.Fatal("sharing metrics disagree with 200:1")
	}
	if got := apiWatches() - baselineWatches; got != 1 {
		t.Fatalf("actual API watch increase = %v, want 1", got)
	}
	t.Log("actual API server CoffeeConfig watch increase: 1")
	// This mutation is restricted to the fixture's demo object; restore only the field changed.
	path := "/apis/examples.configbutler.ai/v1alpha1/namespaces/room-pass/coffeeconfigs/demo-coffee"
	old, err := cs.CoreV1().RESTClient().Get().AbsPath(path).DoRaw(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var object struct {
		Spec map[string]any `json:"spec"`
	}
	if err = json.Unmarshal(old, &object); err != nil {
		t.Fatal(err)
	}
	patch := func(ctx context.Context, value any) error {
		body, e := json.Marshal(map[string]any{"spec": map[string]any{"shopName": value}})
		if e != nil {
			return e
		}
		return cs.CoreV1().RESTClient().Patch(types.MergePatchType).AbsPath(path).Body(body).Do(ctx).Error()
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if e := patch(cleanup, object.Spec["shopName"]); e != nil {
			t.Errorf("restore fixture: %v", e)
		}
	}()
	changed := ns
	started = time.Now()
	if err = patch(ctx, changed); err != nil {
		t.Fatal(err)
	}
	awaitStreamCondition(t, func() bool {
		for _, s := range streams {
			if s.value.Load() != changed {
				return false
			}
		}
		return true
	})
	t.Logf("all 200 viewers converged in %s", time.Since(started))
	var bytes int64
	for _, s := range streams {
		bytes += s.bytes.Load()
	}
	t.Logf("initial snapshot plus one update: %d SSE bytes across 200 viewers", bytes)
	// Reconnect 50 concurrently while the other subscribers keep the same upstream.
	starts := metric("voter_stream_upstream_watch_starts_total")
	for i := range 50 {
		_ = streams[i].body.Close()
		<-streams[i].done
	}
	started = time.Now()
	openBatch(0, 50)
	if metric("voter_stream_upstream_watch_starts_total") != starts {
		t.Fatal("reconnect burst reopened upstream watch")
	}
	t.Logf("50 reconnects resnapshotted in %s without reopening upstream", time.Since(started))
	// Remove just one identity's grant from an already-warm scope.
	binding.Subjects = subjects[1:]
	started = time.Now()
	_, err = cs.RbacV1().RoleBindings("room-pass").Update(ctx, binding, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-streams[0].done:
	case <-time.After(60 * time.Second):
		t.Fatal("withdrawal exceeded 60s")
	}
	t.Logf("one of 200 grants withdrawn and stream closed in %s", time.Since(started))
	if metric("voter_stream_subscribers") != 199 || metric("voter_stream_upstream_watches") != 1 {
		t.Fatal("withdrawal disturbed other viewers")
	}
	for _, s := range streams[1:] {
		select {
		case <-s.done:
			t.Fatal("another viewer disconnected")
		default:
		}
	}
	usage, e := cs.CoreV1().RESTClient().Get().AbsPath("/apis/metrics.k8s.io/v1beta1/namespaces/room-pass/pods").Param("labelSelector", "app=voter").DoRaw(ctx)
	if e == nil {
		t.Logf("Voter resource sample: %s", usage)
	} else {
		t.Logf("resource metrics unavailable: %v", e)
	}
	for _, s := range streams {
		_ = s.body.Close()
		<-s.done
	}
	awaitStreamCondition(t, func() bool {
		return metric("voter_stream_subscribers") == 0 && metric("voter_stream_upstream_watches") == 0
	})
	if apiWatches() != baselineWatches {
		t.Fatal("API watch not released after final disconnect")
	}
	t.Log("final disconnect released the actual upstream watch")
}
