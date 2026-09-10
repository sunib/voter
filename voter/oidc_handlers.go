package main

// The endpoints the SPA talks to in OIDC mode.
//
// /auth/session is the only way the frontend learns who it is. It returns
// display information and a CSRF token -- never the ID token, never anything
// the browser could use as a Kubernetes credential.

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

func registerOIDCHandlers(mux *http.ServeMux, p *oidcProvider, cfg config) {
	mux.HandleFunc("/auth/login", p.handleLogin)
	mux.HandleFunc("/auth/callback", p.handleCallback)

	// GET /auth/session -- "who am I, and what CSRF token should I send?"
	// 401 with a JSON body when unauthenticated. Never a redirect: the SPA
	// calls this with fetch(), and a redirect chain inside fetch is exactly
	// how login loops get built by accident.
	mux.HandleFunc("/auth/session", func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		s, ok := getParticipantSession(r, cfg, sessionCookieCodec, time.Now())
		if !ok {
			clearParticipantSession(w, cfg)
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authenticated": false,
				"loginUrl":      "/auth/login",
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"authenticated": true,
			// The Kubernetes username this participant acts as, as reported by
			// the API server itself (SelfSubjectReview at login) -- so what the
			// audience sees on screen is what appears in the audit log. This
			// used to be "demo:" + subject, which was a guess at the apiserver's
			// claimMappings and became wrong as soon as a second connector
			// existed. Empty if the review failed.
			"username":    s.KubeUsername,
			"displayName": s.DisplayName,
			"email":       s.Email,
			"groups":      s.Groups,
			"csrfToken":   s.CSRF,
			"expiresAt":   s.ExpiresAt,
		})
	})

	// POST /auth/logout -- CSRF-protected, because it is state-changing.
	//
	// This clears the APPLICATION session only. It does not revoke the Dex
	// token, does not clear Room Pass enrolment, and does not remove the
	// Participant. Saying otherwise would be a lie the demo cannot back up.
	mux.HandleFunc("/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if s, ok := getParticipantSession(r, cfg, sessionCookieCodec, time.Now()); ok {
			if err := checkCSRF(r, cfg, s); err != nil {
				http.Error(w, "invalid request", http.StatusForbidden)
				return
			}
		}
		clearParticipantSession(w, cfg)
		w.WriteHeader(http.StatusNoContent)
	})
}

// requireParticipant authenticates a request from the session cookie and, for
// unsafe methods, enforces CSRF. Handlers wrapped in this can assume a verified
// participant and a usable ID token.
func requireParticipant(cfg config, next func(http.ResponseWriter, *http.Request, participantSession)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, ok := getParticipantSession(r, cfg, sessionCookieCodec, time.Now())
		if !ok {
			clearParticipantSession(w, cfg)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":    "authentication required",
				"loginUrl": "/auth/login",
			})
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if err := checkCSRF(r, cfg, s); err != nil {
				log.Printf("csrf: rejected %s %s: %v", r.Method, r.URL.Path, err)
				http.Error(w, "invalid request", http.StatusForbidden)
				return
			}
		}
		next(w, r, s)
	}
}
