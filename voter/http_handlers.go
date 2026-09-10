package main

// The endpoints that are not part of login and not part of the application
// API: health, build info, and the static frontend.
//
// Everything else that used to live here -- /public/login, /public/session,
// /public/logout, /public/session-info and the Traefik forward-auth decision
// endpoint -- belonged to the legacy authentication model, where the browser
// asserted its own identity and this service impersonated it with its
// ServiceAccount. Login now goes through Dex and Room Pass (oidc_handlers.go),
// and participant Kubernetes calls carry the participant's own token.

import (
	"encoding/json"
	"net/http"
)

type handlerDeps struct {
	cfg       config
	kube      kubeClient
	defaultNS string
}

func registerHandlers(mux *http.ServeMux, deps handlerDeps) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/public/build-info", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"gitCommit": gitCommit,
			"gitDirty":  gitDirty,
			"buildDate": buildDate,
		})
	})

	// Registered last and matching everything left over: the built frontend,
	// with an SPA fallback. See static.go.
	mux.Handle("/", rootHandler(deps.cfg.StaticDir))
}
