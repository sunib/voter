package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

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
		dynamic:        dyn,
		restConfig:     cfg,
		defaultNS:      "voter",
		coffeeName:     "test-coffee",
		identityExtras: true,
	}
}

// simonIdentity is the canonical identity used across these tests.
func simonIdentity() audienceIdentity {
	return audienceIdentity{
		Username:    "demo:simon-koudijs",
		DisplayName: "Simon Koudijs",
		Email:       "simon-koudijs@demo.configbutler.ai",
	}
}

// findImpersonateExtra returns the values for an Impersonate-Extra-<key>
// header. client-go percent-encodes the key (slashes become %2F) and Go's
// http.Header canonicalization mangles the case, so we iterate and match by
// percent-decoded, case-insensitive key.
func findImpersonateExtra(headers http.Header, key string) []string {
	needle := strings.ToLower(key)
	var out []string
	for h, vs := range headers {
		if !strings.HasPrefix(strings.ToLower(h), "impersonate-extra-") {
			continue
		}
		rest := h[len("Impersonate-Extra-"):]
		decoded, err := url.PathUnescape(rest)
		if err != nil {
			continue
		}
		if strings.EqualFold(decoded, needle) {
			out = append(out, vs...)
		}
	}
	return out
}

func TestPatchCoffeeConfigSetsImpersonationHeaders(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)

	_, err := kc.patchCoffeeConfig(
		context.Background(),
		[]byte(`{"spec":{"shopName":"X"}}`),
		simonIdentity(),
	)
	if err != nil {
		t.Fatalf("patchCoffeeConfig: %v", err)
	}

	headers := api.lastHeaders(t)

	if got := headers.Get("Impersonate-User"); got != "demo:simon-koudijs" {
		t.Fatalf("Impersonate-User: got %q want %q", got, "demo:simon-koudijs")
	}

	// Impersonate-Group must be the audience group and ONLY the audience group;
	// any additional group would silently widen what voter-audience inherits.
	groups := headers.Values("Impersonate-Group")
	if len(groups) != 1 || groups[0] != audienceGroupName {
		t.Fatalf("Impersonate-Group: got %v want [%q]", groups, audienceGroupName)
	}

	if got := findImpersonateExtra(headers, configButlerDisplayNameExtraKey); len(got) != 1 || got[0] != "Simon Koudijs" {
		t.Fatalf("display-name extra: got %v want [%q]", got, "Simon Koudijs")
	}
	if got := findImpersonateExtra(headers, configButlerEmailExtraKey); len(got) != 1 || got[0] != "simon-koudijs@demo.configbutler.ai" {
		t.Fatalf("email extra: got %v want [%q]", got, "simon-koudijs@demo.configbutler.ai")
	}
}

// TestPatchCoffeeConfigRequiresIdentity verifies the fail-closed contract:
// without a derived identity, the write must error out without hitting the
// API server (and therefore can never silently fall back to the SA).
func TestPatchCoffeeConfigRequiresIdentity(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)

	_, err := kc.patchCoffeeConfig(
		context.Background(),
		[]byte(`{"spec":{"shopName":"X"}}`),
		audienceIdentity{}, // empty username
	)
	if err == nil {
		t.Fatalf("expected error for empty identity, got nil")
	}

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.reqs) != 0 {
		t.Fatalf("expected 0 requests for empty identity, got %d", len(api.reqs))
	}
}

func TestPatchCoffeeConfigOmitsExtrasWhenDisabled(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)
	kc.identityExtras = false

	_, err := kc.patchCoffeeConfig(
		context.Background(),
		[]byte(`{"spec":{"shopName":"X"}}`),
		simonIdentity(),
	)
	if err != nil {
		t.Fatalf("patchCoffeeConfig: %v", err)
	}

	headers := api.lastHeaders(t)
	if got := findImpersonateExtra(headers, configButlerDisplayNameExtraKey); len(got) != 0 {
		t.Fatalf("expected no display-name extra when disabled, got %v", got)
	}
	if got := findImpersonateExtra(headers, configButlerEmailExtraKey); len(got) != 0 {
		t.Fatalf("expected no email extra when disabled, got %v", got)
	}
	// Group + user must still be set.
	if got := headers.Get("Impersonate-User"); got != "demo:simon-koudijs" {
		t.Fatalf("Impersonate-User: got %q want %q", got, "demo:simon-koudijs")
	}
}

func TestImpersonatedDynamicRejectsEmptyUsername(t *testing.T) {
	kc := kubeClient{restConfig: &rest.Config{Host: "https://example.invalid"}}
	if _, err := kc.impersonatedDynamic(audienceIdentity{}); err == nil {
		t.Fatalf("expected impersonatedDynamic to reject empty identity")
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
		Identity:      simonIdentity(),
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
	// v1alpha3 is what a current gitops-reverser serves; v1alpha1 is gone.
	if !strings.Contains(req.path, "/apis/configbutler.ai/v1alpha3/namespaces/voter/commitrequests") {
		t.Fatalf("path: got %q, expected to hit the commitrequests endpoint", req.path)
	}

	body := decodeCommitRequestBody(t, req.body)

	if got := body["kind"]; got != "CommitRequest" {
		t.Fatalf("kind: got %v want CommitRequest", got)
	}
	if got := body["apiVersion"]; got != commitRequestAPIVersion {
		t.Fatalf("apiVersion: got %v want %s", got, commitRequestAPIVersion)
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
		Identity:      simonIdentity(),
		GitTargetName: "voter-coffee",
	})
	if err != nil {
		t.Fatalf("createCommitRequest: %v", err)
	}

	headers := api.lastHeaders(t)
	if got := headers.Get("Impersonate-User"); got != "demo:simon-koudijs" {
		t.Fatalf("Impersonate-User: got %q want %q", got, "demo:simon-koudijs")
	}
	groups := headers.Values("Impersonate-Group")
	if len(groups) != 1 || groups[0] != audienceGroupName {
		t.Fatalf("Impersonate-Group: got %v want [%q]", groups, audienceGroupName)
	}
	if got := findImpersonateExtra(headers, configButlerDisplayNameExtraKey); len(got) != 1 || got[0] != "Simon Koudijs" {
		t.Fatalf("display-name extra: got %v want [%q]", got, "Simon Koudijs")
	}
	if got := findImpersonateExtra(headers, configButlerEmailExtraKey); len(got) != 1 || got[0] != "simon-koudijs@demo.configbutler.ai" {
		t.Fatalf("email extra: got %v want [%q]", got, "simon-koudijs@demo.configbutler.ai")
	}
}

func TestCreateCommitRequestRequiresIdentity(t *testing.T) {
	api := newHeaderCapturingAPIServer(t)
	kc := newTestKubeClient(t, api.server.URL)

	_, err := kc.createCommitRequest(context.Background(), createCommitRequestParams{
		Identity:      audienceIdentity{},
		GitTargetName: "voter-coffee",
	})
	if err == nil {
		t.Fatalf("expected error for empty identity, got nil")
	}

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.reqs) != 0 {
		t.Fatalf("expected 0 requests for empty identity, got %d", len(api.reqs))
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
				Identity:      simonIdentity(),
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
		Identity:      simonIdentity(),
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
		Identity: simonIdentity(),
	})
	if err == nil {
		t.Fatalf("expected error when GitTargetName is empty")
	}

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
		Identity:      simonIdentity(),
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
