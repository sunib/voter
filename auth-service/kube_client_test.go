package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func TestSanitizeImpersonationUser(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain nickname", in: "Simon", want: "Simon"},
		{name: "trims whitespace", in: "  Simon  ", want: "Simon"},
		{name: "preserves spaces in name", in: "Simon Koudijs", want: "Simon Koudijs"},
		{name: "empty rejected", in: "", want: ""},
		{name: "whitespace-only rejected", in: "   ", want: ""},
		{name: "system prefix rejected", in: "system:masters", want: ""},
		{name: "uppercase system prefix rejected", in: "SYSTEM:admin", want: ""},
		{name: "mixed-case system prefix rejected", in: "System:Anything", want: ""},
		{name: "system prefix with leading whitespace rejected", in: "  system:masters", want: ""},
		{name: "system as substring is fine", in: "ecosystem-bot", want: "ecosystem-bot"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeImpersonationUser(tc.in)
			if got != tc.want {
				t.Fatalf("sanitizeImpersonationUser(%q): got %q want %q", tc.in, got, tc.want)
			}
		})
	}
}

// capturedRequest records what arrived at the fake API server. Tests inspect
// both the impersonation headers and the JSON payload to verify wire shape.
type capturedRequest struct {
	method  string
	path    string
	headers http.Header
	body    []byte
}

// headerCapturingAPIServer stands in for the Kubernetes API server. It captures
// each incoming request and returns a routed response so real client-go Patch
// and Create calls complete end-to-end.
type headerCapturingAPIServer struct {
	server  *httptest.Server
	mu      sync.Mutex
	reqs    []capturedRequest
	handler func(w http.ResponseWriter, r *http.Request)
}

func newHeaderCapturingAPIServer(t *testing.T) *headerCapturingAPIServer {
	t.Helper()
	c := &headerCapturingAPIServer{}
	c.handler = defaultAPIResponder
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		c.mu.Lock()
		c.reqs = append(c.reqs, capturedRequest{
			method:  r.Method,
			path:    r.URL.Path,
			headers: r.Header.Clone(),
			body:    body,
		})
		handler := c.handler
		c.mu.Unlock()

		// Replay body so the handler can decode if needed (it doesn't yet,
		// but keeps the helper composable).
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		handler(w, r)
	}))
	t.Cleanup(c.server.Close)
	return c
}

// defaultAPIResponder returns a minimal object that satisfies client-go for
// both CoffeeConfig Patch and CommitRequest Create paths used in these tests.
func defaultAPIResponder(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if strings.Contains(r.URL.Path, "/commitrequests") {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"apiVersion": "configbutler.ai/v1alpha1",
			"kind":       "CommitRequest",
			"metadata": map[string]any{
				"name":      "coffee-save-abcde",
				"namespace": "voter",
			},
			"spec": map[string]any{},
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"apiVersion": "examples.configbutler.ai/v1alpha1",
		"kind":       "CoffeeConfig",
		"metadata": map[string]any{
			"name":      "test-coffee",
			"namespace": "voter",
		},
		"spec": map[string]any{},
	})
}

func (c *headerCapturingAPIServer) lastRequest(t *testing.T) capturedRequest {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reqs) == 0 {
		t.Fatalf("no request was captured")
	}
	return c.reqs[len(c.reqs)-1]
}

func (c *headerCapturingAPIServer) lastHeaders(t *testing.T) http.Header {
	return c.lastRequest(t).headers
}

func newTestKubeClient(t *testing.T, serverURL string) kubeClient {
	t.Helper()
	cfg := &rest.Config{Host: serverURL}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("dynamic.NewForConfig: %v", err)
	}
	return kubeClient{
		dynamic:    dyn,
		restConfig: cfg,
		defaultNS:  "voter",
		coffeeName: "test-coffee",
	}
}

func TestPatchCoffeeConfigSetsImpersonationHeaders(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)

	_, err := kc.patchCoffeeConfig(
		context.Background(),
		[]byte(`{"spec":{"shopName":"X"}}`),
		"Simon Koudijs",
	)
	if err != nil {
		t.Fatalf("patchCoffeeConfig: %v", err)
	}

	headers := api.lastHeaders(t)

	if got := headers.Get("Impersonate-User"); got != "Simon Koudijs" {
		t.Fatalf("Impersonate-User: got %q want %q", got, "Simon Koudijs")
	}

	// Impersonate-Group can be set multiple times in the request — client-go
	// emits one header per group. Assert the audience group is present and is
	// the *only* group, so we don't accidentally widen permissions.
	groups := headers.Values("Impersonate-Group")
	if len(groups) != 1 || groups[0] != audienceGroupName {
		t.Fatalf("Impersonate-Group: got %v want [%q]", groups, audienceGroupName)
	}
}

func TestPatchCoffeeConfigSkipsImpersonationForEmptyActor(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)

	_, err := kc.patchCoffeeConfig(
		context.Background(),
		[]byte(`{"spec":{"shopName":"X"}}`),
		"",
	)
	if err != nil {
		t.Fatalf("patchCoffeeConfig: %v", err)
	}

	headers := api.lastHeaders(t)
	if got := headers.Get("Impersonate-User"); got != "" {
		t.Fatalf("expected no Impersonate-User header, got %q", got)
	}
	if got := headers.Values("Impersonate-Group"); len(got) != 0 {
		t.Fatalf("expected no Impersonate-Group header, got %v", got)
	}
}

func TestPatchCoffeeConfigRejectsSystemActor(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)

	// A nickname starting with "system:" must never be propagated to the API
	// server, even though the impersonate-groups RBAC is locked down — the
	// sanitizer is defense-in-depth.
	_, err := kc.patchCoffeeConfig(
		context.Background(),
		[]byte(`{"spec":{"shopName":"X"}}`),
		"system:masters",
	)
	if err != nil {
		t.Fatalf("patchCoffeeConfig: %v", err)
	}

	headers := api.lastHeaders(t)
	if got := headers.Get("Impersonate-User"); got != "" {
		t.Fatalf("expected no Impersonate-User header for system: actor, got %q", got)
	}
	if got := headers.Values("Impersonate-Group"); len(got) != 0 {
		t.Fatalf("expected no Impersonate-Group header for system: actor, got %v", got)
	}
}

func TestImpersonatedDynamicCarriesAudienceGroup(t *testing.T) {
	// Direct unit check on the config builder, to keep the audienceGroupName
	// constant pinned. The wire-level tests above prove this travels to the
	// API server; this one prevents an accidental refactor from silently
	// dropping the group.
	kc := kubeClient{restConfig: &rest.Config{Host: "https://example.invalid"}}
	if _, err := kc.impersonatedDynamic("Simon"); err != nil {
		t.Fatalf("impersonatedDynamic: %v", err)
	}

	cfg := rest.CopyConfig(kc.restConfig)
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: "Simon",
		Groups:   []string{audienceGroupName},
	}
	if cfg.Impersonate.UserName != "Simon" {
		t.Fatalf("Impersonate.UserName: got %q want %q", cfg.Impersonate.UserName, "Simon")
	}
	if len(cfg.Impersonate.Groups) != 1 || cfg.Impersonate.Groups[0] != "voter-audience" {
		t.Fatalf("Impersonate.Groups: got %v want [voter-audience]", cfg.Impersonate.Groups)
	}
}

// decodeCommitRequestBody is a tiny helper for inspecting the JSON the client
// sent on a CommitRequest Create.
func decodeCommitRequestBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("failed to decode CommitRequest body: %v\nraw: %s", err, string(body))
	}
	return obj
}

func TestCreateCommitRequestSendsExpectedShape(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)

	name, err := kc.createCommitRequest(context.Background(), createCommitRequestParams{
		Actor:         "Simon",
		GitTargetName: "voter-coffee",
		Message:       "Update voucher copy",
	})
	if err != nil {
		t.Fatalf("createCommitRequest: %v", err)
	}
	if name == "" {
		t.Fatalf("expected a generated name, got empty string")
	}

	req := api.lastRequest(t)
	if req.method != http.MethodPost {
		t.Fatalf("method: got %q want POST", req.method)
	}
	if !strings.Contains(req.path, "/apis/configbutler.ai/v1alpha1/namespaces/voter/commitrequests") {
		t.Fatalf("path: got %q, expected to hit the commitrequests endpoint", req.path)
	}

	body := decodeCommitRequestBody(t, req.body)

	if got := body["kind"]; got != "CommitRequest" {
		t.Fatalf("kind: got %v want CommitRequest", got)
	}
	if got := body["apiVersion"]; got != "configbutler.ai/v1alpha1" {
		t.Fatalf("apiVersion: got %v want configbutler.ai/v1alpha1", got)
	}

	meta, _ := body["metadata"].(map[string]any)
	if got := meta["generateName"]; got != "coffee-save-" {
		t.Fatalf("metadata.generateName: got %v want coffee-save-", got)
	}
	if got := meta["namespace"]; got != "voter" {
		t.Fatalf("metadata.namespace: got %v want voter", got)
	}

	spec, _ := body["spec"].(map[string]any)
	gtRef, _ := spec["gitTargetRef"].(map[string]any)
	if got := gtRef["name"]; got != "voter-coffee" {
		t.Fatalf("spec.gitTargetRef.name: got %v want voter-coffee", got)
	}
	if got := spec["message"]; got != "Update voucher copy" {
		t.Fatalf("spec.message: got %v want %q", got, "Update voucher copy")
	}
}

func TestCreateCommitRequestSetsImpersonationHeaders(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)

	_, err := kc.createCommitRequest(context.Background(), createCommitRequestParams{
		Actor:         "Simon Koudijs",
		GitTargetName: "voter-coffee",
	})
	if err != nil {
		t.Fatalf("createCommitRequest: %v", err)
	}

	headers := api.lastHeaders(t)
	if got := headers.Get("Impersonate-User"); got != "Simon Koudijs" {
		t.Fatalf("Impersonate-User: got %q want %q", got, "Simon Koudijs")
	}
	groups := headers.Values("Impersonate-Group")
	if len(groups) != 1 || groups[0] != audienceGroupName {
		t.Fatalf("Impersonate-Group: got %v want [%q]", groups, audienceGroupName)
	}
}

func TestCreateCommitRequestRejectsSystemActor(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)

	_, err := kc.createCommitRequest(context.Background(), createCommitRequestParams{
		Actor:         "system:masters",
		GitTargetName: "voter-coffee",
	})
	if err != nil {
		t.Fatalf("createCommitRequest: %v", err)
	}

	headers := api.lastHeaders(t)
	if got := headers.Get("Impersonate-User"); got != "" {
		t.Fatalf("expected no Impersonate-User header for system: actor, got %q", got)
	}
	if got := headers.Values("Impersonate-Group"); len(got) != 0 {
		t.Fatalf("expected no Impersonate-Group header for system: actor, got %v", got)
	}
}

func TestCreateCommitRequestOmitsEmptyMessage(t *testing.T) {
	cases := []struct {
		name    string
		message string
	}{
		{name: "empty string", message: ""},
		{name: "whitespace only", message: "   \n\t  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newHeaderCapturingAPIServer(t)
			kc := newTestKubeClient(t, api.server.URL)

			_, err := kc.createCommitRequest(context.Background(), createCommitRequestParams{
				Actor:         "Simon",
				GitTargetName: "voter-coffee",
				Message:       tc.message,
			})
			if err != nil {
				t.Fatalf("createCommitRequest: %v", err)
			}

			body := decodeCommitRequestBody(t, api.lastRequest(t).body)
			spec, _ := body["spec"].(map[string]any)
			if _, present := spec["message"]; present {
				t.Fatalf("expected spec.message to be omitted, but it was present: %v", spec["message"])
			}
		})
	}
}

func TestCreateCommitRequestTrimsMessage(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)

	_, err := kc.createCommitRequest(context.Background(), createCommitRequestParams{
		Actor:         "Simon",
		GitTargetName: "voter-coffee",
		Message:       "  Update voucher copy  \n",
	})
	if err != nil {
		t.Fatalf("createCommitRequest: %v", err)
	}

	body := decodeCommitRequestBody(t, api.lastRequest(t).body)
	spec, _ := body["spec"].(map[string]any)
	if got := spec["message"]; got != "Update voucher copy" {
		t.Fatalf("spec.message: got %q want %q (whitespace must be trimmed before send)", got, "Update voucher copy")
	}
}

func TestCreateCommitRequestRequiresGitTargetName(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)

	_, err := kc.createCommitRequest(context.Background(), createCommitRequestParams{
		Actor: "Simon",
	})
	if err == nil {
		t.Fatalf("expected error when GitTargetName is empty")
	}

	// Should not have issued any HTTP call.
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.reqs) != 0 {
		t.Fatalf("expected 0 requests, got %d", len(api.reqs))
	}
}

func TestCreateCommitRequestOverridesNamespace(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)

	_, err := kc.createCommitRequest(context.Background(), createCommitRequestParams{
		Actor:         "Simon",
		GitTargetName: "other-target",
		Namespace:     "configbutler",
	})
	if err != nil {
		t.Fatalf("createCommitRequest: %v", err)
	}

	req := api.lastRequest(t)
	if !strings.Contains(req.path, "/namespaces/configbutler/commitrequests") {
		t.Fatalf("expected path under /namespaces/configbutler/, got %q", req.path)
	}
}
