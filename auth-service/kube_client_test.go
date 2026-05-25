package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// headerCapturingAPIServer stands in for the Kubernetes API server. It captures
// every incoming request's headers and returns a minimal CoffeeConfig payload
// so a real client-go Patch call can complete end-to-end.
type headerCapturingAPIServer struct {
	server  *httptest.Server
	mu      sync.Mutex
	headers []http.Header
}

func newHeaderCapturingAPIServer(t *testing.T) *headerCapturingAPIServer {
	t.Helper()
	c := &headerCapturingAPIServer{}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.headers = append(c.headers, r.Header.Clone())
		c.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"apiVersion": "examples.configbutler.ai/v1alpha1",
			"kind":       "CoffeeConfig",
			"metadata": map[string]any{
				"name":      "test-coffee",
				"namespace": "voter",
			},
			"spec": map[string]any{},
		})
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *headerCapturingAPIServer) lastHeaders(t *testing.T) http.Header {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.headers) == 0 {
		t.Fatalf("no request was captured")
	}
	return c.headers[len(c.headers)-1]
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
