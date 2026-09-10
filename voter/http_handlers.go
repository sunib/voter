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
	defaultNS string

	// newClients builds the request-scoped Kubernetes clients from a
	// participant's ID token. It is a field rather than a direct call so tests
	// can substitute a fake API server; production always uses
	// newParticipantClients. A nil value means the real one -- a test that
	// forgets to set it must not silently reach a cluster, and
	// participantClientsFor makes that a failure instead.
	newClients func(cfg config, idToken string) (participantClients, error)

	// vouchers counts redemptions per voucher code for this process. See
	// coffee_vouchers.go for why this is in memory and what that costs.
	vouchers *voucherLedger
}

// participantClientsFor is the single place handlers obtain Kubernetes clients,
// so the "credentials come from the request and nothing else" rule has exactly
// one enforcement point.
func (d handlerDeps) participantClientsFor(idToken string) (participantClients, error) {
	if d.newClients != nil {
		return d.newClients(d.cfg, idToken)
	}
	return newParticipantClients(d.cfg, idToken)
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
