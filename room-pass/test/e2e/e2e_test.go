package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	api "github.com/sunib/voter/room-pass/api/v1alpha1"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const issuer = "https://login.roompass.test:18443"
const join = "https://demo.roompass.test:18443"

type flow struct {
	t        *testing.T
	browser  *http.Client
	ctx      context.Context
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	db       client.Client
}

func (f *flow) login(enroll bool) (string, map[string]any) {
	f.t.Helper()
	state := oauth2.GenerateVerifier()
	verifier := oauth2.GenerateVerifier()
	u := f.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("connector_id", "room"))
	resp, e := f.browser.Get(u)
	if e != nil {
		f.t.Fatal(e)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Request.URL.Path != "/join" {
		f.t.Fatalf("join page: %d %s %s", resp.StatusCode, resp.Request.URL, body)
	}
	form := url.Values{}
	for _, match := range regexp.MustCompile(`name="([^"]+)" value="([^"]*)"`).FindAllStringSubmatch(string(body), -1) {
		form.Set(match[1], html.UnescapeString(match[2]))
	}
	if enroll {
		room := &api.Room{}
		if e = f.db.Get(f.ctx, client.ObjectKey{Namespace: "room-pass", Name: "demo"}, room); e != nil {
			f.t.Fatal(e)
		}
		if room.Status.JoinCode == nil {
			f.t.Fatal("no published code")
		}
		form.Set("code", room.Status.JoinCode.Code)
		form.Set("name", "Ada Demo")
	}
	req, _ := http.NewRequest("POST", join+"/join", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", join)
	req.Header.Set("X-Remote-User", "Mallory")
	req.Header.Set("X-Remote-Group", "system:masters")
	resp, e = f.browser.Do(req)
	if e != nil {
		f.t.Fatal(e)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	location := resp.Header.Get("Location")
	callback, e := url.Parse(location)
	if e != nil || callback.Path != "/app/callback" {
		f.t.Fatalf("OIDC callback: %d %s %s", resp.StatusCode, location, body)
	}
	if callback.Query().Get("state") != state {
		f.t.Fatal("application state changed")
	}
	token, e := f.oauth.Exchange(f.ctx, callback.Query().Get("code"), oauth2.VerifierOption(verifier))
	if e != nil {
		f.t.Fatal(e)
	}
	raw, _ := token.Extra("id_token").(string)
	verified, e := f.verifier.Verify(f.ctx, raw)
	if e != nil {
		f.t.Fatal(e)
	}
	claims := map[string]any{}
	if e = verified.Claims(&claims); e != nil {
		f.t.Fatal(e)
	}
	if claims["name"] != "Ada Demo" || !strings.HasSuffix(fmt.Sprint(claims["email"]), "@demo.invalid") || fmt.Sprint(claims["groups"]) != "[demo:room-pass-test]" {
		f.t.Fatalf("wrong identity claims: %v", claims)
	}
	// The whole shared-issuer containment argument rests on this claim: the
	// authenticator derives the username prefix from it, so a token that does
	// not carry it (or carries another connector) must never reach Kubernetes
	// as a demo participant. Dex only emits it for the "federated:id" scope.
	fed, ok := claims["federated_claims"].(map[string]any)
	if !ok {
		f.t.Fatalf("no federated_claims in the ID token; is the federated:id scope requested? claims: %v", claims)
	}
	if fed["connector_id"] != "room" {
		f.t.Fatalf("connector_id = %v, want room", fed["connector_id"])
	}
	return raw, claims
}
func TestRealDexAndKubernetes(t *testing.T) {
	if os.Getenv("ROOM_PASS_E2E") != "1" {
		t.Skip("run task e2e")
	}
	ctx := context.Background()
	cfg, e := clientcmd.BuildConfigFromFlags("", "../../.local/kubeconfig")
	if e != nil {
		t.Fatal(e)
	}
	scheme := runtime.NewScheme()
	_ = api.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = rbacv1.AddToScheme(scheme)
	db, e := client.New(cfg, client.Options{Scheme: scheme})
	if e != nil {
		t.Fatal(e)
	}
	ca, e := os.ReadFile("../../.local/tls.crt")
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	gateway := os.Getenv("ROOM_PASS_EDGE_IP")
	if gateway == "" {
		gateway = "127.0.0.1"
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		if strings.HasSuffix(host, ".roompass.test") {
			addr = net.JoinHostPort(gateway, "18443")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
	}}
	protocol := &http.Client{Transport: tr, Timeout: 15 * time.Second}
	ctx = oidc.ClientContext(ctx, protocol)
	provider, e := oidc.NewProvider(ctx, issuer)
	if e != nil {
		t.Fatal(e)
	}
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Transport: tr, Jar: jar, Timeout: 20 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 15 {
			return fmt.Errorf("redirect loop")
		}
		if r.URL.Path == "/app/callback" {
			return http.ErrUseLastResponse
		}
		r.Header.Set("X-Remote-User", "Mallory")
		r.Header.Set("X-Remote-Group", "system:masters")
		return nil
	}}
	f := &flow{t: t, browser: browser, ctx: ctx, db: db, oauth: oauth2.Config{ClientID: "room-pass-demo", Endpoint: provider.Endpoint(), RedirectURL: join + "/app/callback", Scopes: []string{"openid", "profile", "email", "groups", "federated:id"}}, verifier: provider.Verifier(&oidc.Config{ClientID: "room-pass-demo"})}
	token, first := f.login(true)
	// Restart only Room Pass, preserving Kubernetes records and the cookie Secret.
	kubeconfig, _ := filepath.Abs("../../.local/kubeconfig")
	for _, args := range [][]string{{"-n", "room-pass", "rollout", "restart", "deployment/room-pass"}, {"-n", "room-pass", "rollout", "status", "deployment/room-pass", "--timeout=120s"}} {
		cmd := exec.Command("kubectl", append([]string{"--kubeconfig", kubeconfig}, args...)...)
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("restart: %s %v", out, e)
		}
	}
	// Rollout completion precedes Traefik's endpoint refresh by a short interval.
	for deadline := time.Now().Add(20 * time.Second); ; {
		r, err := protocol.Get(join + "/join")
		if err == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("edge did not recover after restart")
		}
		time.Sleep(time.Second)
	}
	_, second := f.login(false)
	if first["sub"] != second["sub"] || first["email"] != second["email"] {
		t.Fatal("identity changed after restart")
	}

	// Device authorization uses the same enrolled browser, without a CLI listener.
	deviceVerifier := oauth2.GenerateVerifier()
	device, e := f.oauth.DeviceAuth(ctx, oauth2.S256ChallengeOption(deviceVerifier))
	if e != nil {
		t.Fatal(e)
	}
	deviceResponse, e := browser.PostForm(issuer+"/device/auth/verify_code", url.Values{"user_code": {device.UserCode}})
	if e != nil {
		t.Fatal(e)
	}
	deviceBody, _ := io.ReadAll(deviceResponse.Body)
	deviceResponse.Body.Close()
	if deviceResponse.StatusCode != 200 || deviceResponse.Request.URL.Path != "/join" {
		t.Fatalf("device join: %d %s", deviceResponse.StatusCode, deviceBody)
	}
	deviceForm := url.Values{}
	for _, m := range regexp.MustCompile(`name="([^"]+)" value="([^"]*)"`).FindAllStringSubmatch(string(deviceBody), -1) {
		deviceForm.Set(m[1], html.UnescapeString(m[2]))
	}
	deviceReq, _ := http.NewRequest("POST", join+"/join", strings.NewReader(deviceForm.Encode()))
	deviceReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deviceReq.Header.Set("Origin", join)
	deviceResponse, e = browser.Do(deviceReq)
	if e != nil {
		t.Fatal(e)
	}
	deviceBody, _ = io.ReadAll(deviceResponse.Body)
	deviceResponse.Body.Close()
	if deviceResponse.StatusCode != 200 {
		t.Fatalf("device completion: %d %s", deviceResponse.StatusCode, deviceBody)
	}
	deviceCtx, cancelDevice := context.WithTimeout(ctx, 20*time.Second)
	defer cancelDevice()
	deviceToken, e := f.oauth.DeviceAccessToken(deviceCtx, device, oauth2.VerifierOption(deviceVerifier))
	if e != nil {
		t.Fatal(e)
	}
	deviceRaw, _ := deviceToken.Extra("id_token").(string)
	deviceID, e := f.verifier.Verify(ctx, deviceRaw)
	if e != nil {
		t.Fatal(e)
	}
	if deviceID.Subject != first["sub"] {
		t.Fatal("device flow changed subject")
	}
	// No admin certificate or impersonation headers in this client.
	participantCfg := &rest.Config{Host: cfg.Host, TLSClientConfig: rest.TLSClientConfig{CAData: cfg.CAData, CAFile: cfg.CAFile, ServerName: cfg.ServerName}, BearerToken: token, Timeout: 10 * time.Second}
	participant, e := client.New(participantCfg, client.Options{Scheme: scheme})
	if e != nil {
		t.Fatal(e)
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{GenerateName: "participant-", Namespace: "demo"}, Data: map[string]string{"message": "A real OIDC participant wrote this"}}
	// API server retries discovery initialization while Dex is starting.
	deadline := time.Now().Add(30 * time.Second)
	for {
		e = participant.Create(ctx, cm)
		if e == nil {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal(e)
		}
		time.Sleep(time.Second)
	}
	t.Cleanup(func() { _ = db.Delete(context.Background(), cm) })
	// Inspect the actual API-server audit identity and attribution extras.
	auditOK := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		out, err := exec.Command("docker", "exec", "k3d-room-pass-e2e-server-0", "cat", "/etc/room-pass/audit.log").Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			var event struct {
				Stage     string `json:"stage"`
				Verb      string `json:"verb"`
				ObjectRef struct {
					Name string `json:"name"`
				} `json:"objectRef"`
				ResponseObject struct {
					Metadata struct {
						Name string `json:"name"`
					} `json:"metadata"`
				} `json:"responseObject"`
				User struct {
					Username string              `json:"username"`
					Extra    map[string][]string `json:"extra"`
				} `json:"user"`
			}
			if json.Unmarshal([]byte(line), &event) != nil {
				continue
			}
			if event.Stage == "ResponseComplete" && event.Verb == "create" && event.ResponseObject.Metadata.Name == cm.Name {
				auditOK = event.User.Username == "demo:"+fmt.Sprint(first["sub"]) && fmt.Sprint(event.User.Extra["configbutler.ai/claims/display-name"]) == "[Ada Demo]" && fmt.Sprint(event.User.Extra["configbutler.ai/claims/email"]) == "["+fmt.Sprint(first["email"])+"]"
			}
		}
		if auditOK {
			break
		}
		time.Sleep(time.Second)
	}
	if !auditOK {
		t.Fatal("missing or incorrect audit attribution")
	}
	// A pod without Room Pass's label cannot reach the Dex Service directly.
	probe := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{GenerateName: "bypass-probe-", Namespace: "room-pass"}, Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "probe", Image: "curlimages/curl:8.12.1", Command: []string{"sh", "-c", "curl -s -o /dev/null --max-time 5 http://dex:5556/callback/room?state=forged; rc=$?; test $rc -eq 28 || test $rc -eq 7"}}}}}
	if e = db.Create(ctx, probe); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = db.Delete(context.Background(), probe) })
	for deadline := time.Now().Add(90 * time.Second); ; {
		if e = db.Get(ctx, client.ObjectKeyFromObject(probe), probe); e != nil {
			t.Fatal(e)
		}
		if probe.Status.Phase == corev1.PodSucceeded {
			break
		}
		if probe.Status.Phase == corev1.PodFailed || time.Now().After(deadline) {
			t.Fatal("direct Dex network isolation probe failed", probe.Status.Phase)
		}
		time.Sleep(time.Second)
	}

	if e = participant.List(ctx, &corev1.SecretList{}, client.InNamespace("work")); !apierrors.IsForbidden(e) {
		t.Fatalf("work Secrets must be forbidden: %v", e)
	}
	if e = participant.List(ctx, &api.RoomList{}, client.InNamespace("room-pass")); !apierrors.IsForbidden(e) {
		t.Fatalf("Room codes must be forbidden: %v", e)
	}
	// Public callback aliases must never honor a client-supplied identity.
	for _, path := range []string{"/callback?state=forged", "/callback/other?state=forged", "/%63allback?state=forged"} {
		req, _ := http.NewRequest("GET", issuer+path, nil)
		req.Header.Set("X-Remote-User", "Mallory")
		r, e := protocol.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode < 400 {
			t.Fatalf("bypass alias %s: %d", path, r.StatusCode)
		}
	}

	// Exercise the interactive example client's callback, encrypted cookie and write form.
	interactive := *browser
	interactive.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) > 15 {
			return fmt.Errorf("redirect loop")
		}
		return nil
	}
	r, err := interactive.Get(join + "/app/login")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || r.Request.URL.Path != "/join" {
		t.Fatalf("interactive join: %d %s", r.StatusCode, b)
	}
	form := url.Values{}
	for _, m := range regexp.MustCompile(`name="([^"]+)" value="([^"]*)"`).FindAllStringSubmatch(string(b), -1) {
		form.Set(m[1], html.UnescapeString(m[2]))
	}
	req, _ := http.NewRequest("POST", join+"/join", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", join)
	r, err = interactive.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(string(b), "Welcome, Ada Demo") {
		t.Fatalf("interactive callback: %d %s", r.StatusCode, b)
	}
	form = url.Values{}
	for _, m := range regexp.MustCompile(`name="([^"]+)" value="([^"]*)"`).FindAllStringSubmatch(string(b), -1) {
		form.Set(m[1], html.UnescapeString(m[2]))
	}
	req, _ = http.NewRequest("POST", join+"/app/write", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", join)
	r, err = interactive.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(string(b), "Kubernetes returned 201") {
		t.Fatalf("interactive write: %d %s", r.StatusCode, b)
	}
	binding := &rbacv1.RoleBinding{}
	key := client.ObjectKey{Namespace: "demo", Name: "demo-editor"}
	if e = db.Get(ctx, key, binding); e != nil {
		t.Fatal(e)
	}
	if e = db.Delete(ctx, binding); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { binding.ResourceVersion = ""; binding.UID = ""; _ = db.Create(context.Background(), binding) })
	if e = participant.List(ctx, &corev1.ConfigMapList{}, client.InNamespace("demo")); !apierrors.IsForbidden(e) {
		t.Fatalf("shutdown grant withdrawal failed: %v", e)
	}
	summary, _ := json.Marshal(map[string]any{"identityStableAfterRestart": true, "nativeOIDCWrite": true, "workDenied": true, "roomCodesDenied": true, "forgedHeadersIgnored": true, "grantWithdrawalDeniedExistingToken": true})
	t.Log(string(summary))
}
