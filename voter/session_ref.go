package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type sessionRef struct {
	namespace string
	name      string
}

func parseSessionRef(uri string) (sessionRef, bool) {
	if uri == "" {
		return sessionRef{}, false
	}
	u := uri
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	mux := chi.NewRouter()
	mux.Get("/apis/examples.configbutler.ai/v1alpha1/namespaces/{namespace}/quizsessions/{name}", func(http.ResponseWriter, *http.Request) {})

	rctx := chi.NewRouteContext()
	rctx.Routes = mux
	if !mux.Match(rctx, http.MethodGet, u) {
		return sessionRef{}, false
	}
	return sessionRef{namespace: rctx.URLParam("namespace"), name: rctx.URLParam("name")}, true
}

func sessionKey(ref sessionRef) string {
	if ref.namespace == "" || ref.name == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s", ref.namespace, ref.name)
}

func parseSessionKey(key string) (sessionRef, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return sessionRef{}, false
	}
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		return sessionRef{}, false
	}
	if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return sessionRef{}, false
	}
	return sessionRef{namespace: parts[0], name: parts[1]}, true
}
