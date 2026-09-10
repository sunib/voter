package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	registerHandlers(mux, handlerDeps{cfg: config{}})
	return mux
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	testMux(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
}

func TestBuildInfoEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	testMux(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/public/build-info", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	for _, k := range []string{"gitCommit", "gitDirty", "buildDate"} {
		if _, ok := body[k]; !ok {
			t.Errorf("missing %q in %v", k, body)
		}
	}
}

// The legacy authentication model is gone and must not come back by accident.
// These endpoints let a browser assert its own identity, or handed Traefik an
// impersonation decision derived from a cookie this service issued itself. A
// reappearance would reintroduce exactly the trust boundary the OIDC flow
// exists to remove, so pin their absence.
func TestLegacyAuthEndpointsAreGone(t *testing.T) {
	mux := testMux(t)
	for _, path := range []string{
		"/public/login",
		"/public/session",
		"/public/logout",
		"/public/session-info",
		"/private/forward-auth-decision",
		"/public/kubeconfig",
		"/public/storefront",
		"/public/orders",
		"/public/admin/session",
		"/public/admin/coffeeconfig",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			// No bundle is configured in this test, so "/" answers with the API
			// description and anything else is a 404. Either way these paths
			// must not be served by a handler of their own.
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: got %d, want 404 — a legacy endpoint is back", method, path, rec.Code)
			}
		}
	}
}
