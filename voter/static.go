package main

// Serving the built frontend from this same binary.
//
// The demo used to ship two images: an nginx container for the Vue bundle and
// this one for the API. That split bought nothing -- both are behind the same
// Traefik route on the same origin, and the app cookie is scoped to that origin
// anyway -- while costing a second Deployment, a second Service and a second
// place to get the SPA fallback wrong. One image, one origin, one thing to
// deploy.
//
// STATIC_DIR points at the built bundle (/srv/www in the image). When it is
// empty -- the normal local setup, where Vite serves the frontend on :5173 and
// proxies /public, /auth and /apis here -- the root handler falls back to a
// plain-text description of the API.

import (
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// apiPrefixes are owned by the API. A request under one of them that reaches
// the root handler matched no registered route, so it is a 404 -- never the SPA
// shell. Serving index.html there would turn a typo in a fetch() path into an
// HTML body that the caller tries to parse as JSON, which is a confusing way to
// discover a 404.
var apiPrefixes = []string{"/auth/", "/public/", "/private/", "/apis/", "/healthz"}

func isAPIPath(p string) bool {
	for _, prefix := range apiPrefixes {
		if p == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// checkStaticDir fails the process at boot when STATIC_DIR is set but unusable.
// An image built without the frontend stage should not start and serve 500s to
// every browser; it should refuse to start and say why.
func checkStaticDir(dir string) error {
	if dir == "" {
		return nil
	}
	_, err := os.Stat(filepath.Join(dir, "index.html"))
	return err
}

// rootHandler serves the SPA when a bundle is configured, and otherwise
// describes the API in plain text.
func rootHandler(dir string) http.Handler {
	if dir == "" {
		return http.HandlerFunc(apiDescriptionHandler)
	}
	root := http.Dir(dir)
	files := http.FileServer(root)
	indexPath := filepath.Join(dir, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		if isAPIPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}

		// http.Dir.Open rejects traversal outside the root, so a cleaned path
		// that opens is a real file inside the bundle.
		upath := path.Clean("/" + r.URL.Path)
		if f, err := root.Open(upath); err == nil {
			info, statErr := f.Stat()
			_ = f.Close()
			if statErr == nil && !info.IsDir() {
				setAssetCacheHeader(w, upath)
				files.ServeHTTP(w, r)
				return
			}
		}

		// Anything else is a client-side route: hand back the shell and let
		// vue-router resolve it.
		serveIndex(w, r, indexPath)
	})
}

// setAssetCacheHeader caches Vite's content-hashed output for a year and
// nothing else. The hash is in the filename, so a new build produces new URLs;
// caching anything unhashed that aggressively would pin a stale demo on a
// phone in the audience with no way to clear it.
func setAssetCacheHeader(w http.ResponseWriter, upath string) {
	if strings.HasPrefix(upath, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
}

// serveIndex writes the SPA shell. It is never cached: it names the hashed
// bundles, so a cached shell would keep loading a previous deploy's assets.
func serveIndex(w http.ResponseWriter, r *http.Request, indexPath string) {
	f, err := os.Open(indexPath) // #nosec G304 -- fixed path from configuration
	if err != nil {
		log.Printf("static: cannot open %s: %v", indexPath, err)
		http.Error(w, "Frontend bundle unavailable", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// A zero modtime suppresses Last-Modified and the 304 handling that would
	// undercut no-store.
	http.ServeContent(w, r, "index.html", time.Time{}, f)
}

func apiDescriptionHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("voter (no STATIC_DIR configured; the frontend is served by Vite in development)\n\n" +
		"Endpoints:\n" +
		"- GET /healthz\n" +
		"- GET /public/build-info\n" +
		"- GET /auth/login, GET /auth/callback, GET /auth/session, POST /auth/logout\n" +
		"- GET|POST /private/forward-auth-decision (Traefik ForwardAuth)\n" +
		"- GET|POST /public/session-info (Get info on current sessions)\n"))
}
