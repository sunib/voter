package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func staticFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>voter</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app-a1b2c3.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// A path under an API prefix that reached the static handler matched no
// registered route. It must 404 rather than hand back the SPA shell, or a
// mistyped fetch() path returns HTML that the caller parses as JSON.
func TestRootHandlerNeverServesTheShellForAPIPaths(t *testing.T) {
	h := rootHandler(staticFixture(t))
	for _, path := range []string{"/public/nope", "/auth/nope", "/private/nope", "/apis/v1/pods", "/healthz"} {
		if rec := get(t, h, http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404 (body %q)", path, rec.Code, rec.Body.String())
		}
	}
}

func TestRootHandlerServesTheShellForClientRoutes(t *testing.T) {
	h := rootHandler(staticFixture(t))
	for _, path := range []string{"/", "/admin/orders", "/answer/session-1"} {
		rec := get(t, h, http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<title>voter</title>") {
			t.Errorf("%s: expected the SPA shell, got %q", path, rec.Body.String())
		}
		// The shell names hashed bundles, so caching it would keep loading a
		// previous deploy's assets.
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", path, got)
		}
	}
}

func TestRootHandlerCachesHashedAssetsOnly(t *testing.T) {
	h := rootHandler(staticFixture(t))

	rec := get(t, h, http.MethodGet, "/assets/app-a1b2c3.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("asset: got %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("asset Cache-Control = %q", got)
	}

	// index.html is reachable by its own name too; it must not be cached there
	// either.
	rec = get(t, h, http.MethodGet, "/index.html")
	if got := rec.Header().Get("Cache-Control"); got == "public, max-age=31536000, immutable" {
		t.Errorf("index.html must not be cached immutably, got %q", got)
	}
}

func TestRootHandlerRejectsTraversalAndWrites(t *testing.T) {
	dir := staticFixture(t)
	secret := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := rootHandler(dir)

	// Traversal falls through to the SPA shell, never to the file.
	rec := get(t, h, http.MethodGet, "/../secret.txt")
	if strings.Contains(rec.Body.String(), "top secret") {
		t.Fatalf("traversal escaped the bundle: %q", rec.Body.String())
	}

	// Static assets answer GET and HEAD. Anything else is a route that does not
	// exist, not a file to serve.
	if rec := get(t, h, http.MethodPost, "/"); rec.Code != http.StatusNotFound {
		t.Errorf("POST /: got %d, want 404", rec.Code)
	}
}

// Without STATIC_DIR the process still runs -- that is the local Vite setup --
// and answers / with a description instead of a shell it does not have.
func TestRootHandlerWithoutABundle(t *testing.T) {
	h := rootHandler("")
	rec := get(t, h, http.MethodGet, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "STATIC_DIR") {
		t.Errorf("expected the API description, got %q", rec.Body.String())
	}
	if rec := get(t, h, http.MethodGet, "/admin"); rec.Code != http.StatusNotFound {
		t.Errorf("/admin: got %d, want 404 with no bundle configured", rec.Code)
	}
}

// An image built without the frontend stage must refuse to start rather than
// serve 500s to every browser that loads the page.
func TestCheckStaticDir(t *testing.T) {
	if err := checkStaticDir(""); err != nil {
		t.Errorf("empty STATIC_DIR is valid (Vite serves the frontend): %v", err)
	}
	if err := checkStaticDir(staticFixture(t)); err != nil {
		t.Errorf("a built bundle must pass: %v", err)
	}
	if err := checkStaticDir(t.TempDir()); err == nil {
		t.Error("a directory without index.html must fail at boot")
	}
}
