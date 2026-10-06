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
	"strings"
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

	// orders is the in-memory feed of what the room actually ordered. Not a
	// custom resource, and deliberately so -- coffee_orders.go says why.
	orders *orderLog
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
	// Explicitly exclude metrics from the public application's SPA fallback.
	mux.Handle("/metrics", http.NotFoundHandler())
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

	// GET /config.json -- where this deployment keeps its objects, rendered from
	// the environment. Settings, not identity: the same answer for everyone and
	// for no one, so it is served beside the frontend's files, outside /public/
	// and without a session. They used to ride along in /auth/session, which
	// krm-foyer answers now and which knows nothing about this application.
	mux.HandleFunc("/config.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Short-lived rather than no-store: it changes only with a rollout, and
		// a stale answer for a minute after one is harmless.
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"namespace":        deps.defaultNS,
			"coffeeConfigName": deps.cfg.CoffeeConfigName,
			"roomName":         deps.cfg.RoomName,
			// So a save can link the commit it became. Empty when unset, which
			// the page renders as a plain sha rather than a dead link.
			"commitURLTemplate": deps.cfg.AuditTrailCommitURLTemplate,
			// Which logins are the audience, for what the page shows. The rule
			// that counts is admission's; this is so a page explaining a
			// refusal agrees with it, without a second copy in the browser.
			"participantConnector": deps.cfg.ParticipantConnectorID,
			// Where a save asks ConfigButler for its commit, which the browser
			// does now, as the person who saved. An empty target asks nothing.
			"gitTargetName":           strings.TrimSpace(deps.cfg.ConfigButlerGitTargetName),
			"databaseGitTargetName":   strings.TrimSpace(deps.cfg.ConfigButlerDatabaseGitTargetName),
			"commitRequestNamespace":  strings.TrimSpace(deps.cfg.ConfigButlerCommitRequestNamespace),
			"commitCloseDelaySeconds": deps.cfg.ConfigButlerCloseDelaySeconds,
			// The Role the operator binds the audience to, and the binding's name.
			"audienceCoffeeAdminRole": deps.cfg.AudienceCoffeeAdminRole,
		})
	})

	// Registered last and matching everything left over: the built frontend,
	// with an SPA fallback. See static.go.
	mux.Handle("/", rootHandler(deps.cfg.StaticDir))
}
