package main

// The endpoints the SPA talks to in OIDC mode.
//
// /auth/session is the only way the frontend learns who it is. It returns
// display information and a CSRF token -- never the ID token, never anything
// the browser could use as a Kubernetes credential.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

func registerOIDCHandlers(mux *http.ServeMux, p *oidcProvider, cfg config, namespace string) {
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
			"username": s.KubeUsername,
			// Which Dex connector authenticated this login, and the decision
			// that follows from it. The SPA gets the ANSWER, not the inputs:
			// which connector counts as a participant is deployment
			// configuration (PARTICIPANT_CONNECTOR_ID), and a browser comparing
			// ids itself would be a second place to get that wrong. The vote
			// handler applies the same comparison, so the page can only ever be
			// explaining a refusal the backend will also make.
			"connector":   s.Connector,
			"canVote":     s.Connector == cfg.ParticipantConnectorID,
			"displayName": s.DisplayName,
			"email":       s.Email,
			"groups":      s.Groups,
			"csrfToken":   s.CSRF,
			"expiresAt":   s.ExpiresAt,
			// Where the application's objects live, so the SPA can address the
			// live stream without hardcoding it or being told by the user. The
			// stream's scope allowlist would refuse anything else anyway; this
			// just means the browser never has to guess.
			"namespace":        namespace,
			"coffeeConfigName": cfg.CoffeeConfigName,
			"roomName":         cfg.RoomName,
			// So a save can link the commit it became. Empty when unset, which
			// the page renders as a plain sha rather than a dead link.
			"commitURLTemplate": cfg.AuditTrailCommitURLTemplate,
		})
	})

	// GET /auth/whoami -- the apiserver's own answer about this login, as YAML.
	//
	// The home screen links the Kubernetes username to this. It deliberately
	// does NOT render what the session cookie remembers: it spends a fresh
	// SelfSubjectReview with THIS browser's token, so what an audience reads is
	// what the apiserver says right now, groups and extras included. There is
	// no other object to show -- a login is not stored anywhere in Kubernetes,
	// it is derived per request from the token -- and every authenticated
	// identity may ask (system:basic-user), so this works for a participant
	// whose only other grant is patching one CoffeeConfig.
	mux.HandleFunc("/auth/whoami", requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		noStore(w)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		review, err := selfSubjectReview(ctx, cfg, s.IDToken)
		if err != nil {
			// JSON, like every other Kubernetes-backed route: a failure here is
			// an expired token or an unreachable apiserver, and the SPA reads
			// those the same way whatever the success body looks like.
			writeParticipantKubeError(w, err)
			return
		}
		out, err := yaml.Marshal(review)
		if err != nil {
			http.Error(w, "could not render the review", http.StatusInternalServerError)
			return
		}

		// text/plain, not application/yaml: this URL is opened as a link from
		// the home page, and browsers download the latter instead of showing
		// it. nosniff keeps the tab from treating the body as anything else.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(out)
	}))

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
		recorder := &refusalRecorder{ResponseWriter: w}
		next(recorder, r, s)
		recorder.log(r, s)
	}
}

// refusalRecorder writes down every refusal a participant was given.
//
// Here rather than at each refusal for one reason: a refusal a handler can
// forget to log is exactly the defect this repairs. Thirty-two call sites write
// a 4xx, and on 2026-09-17 not one of them wrote a line -- the application
// logged every coffee order and every login and no refusal at all. So two
// defects survived a demo in front of two hundred people and had to be
// reconstructed four days later from the creationTimestamps of the objects that
// did get written. docs/post-demo-2026-09-17.md.
//
// A refusal is the most interesting thing this application does. Most of the
// talk is about how real the refusals are. It should not have been the only
// thing it did not write down.
type refusalRecorder struct {
	http.ResponseWriter
	status int
	// body is the refusal's own words, bounded: these are short JSON objects or
	// http.Error strings, and a truncated one still says which refusal it was.
	body []byte
}

const maxRefusalLogBytes = 256

func (rec *refusalRecorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
	}
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *refusalRecorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	if rec.status >= 400 && len(rec.body) < maxRefusalLogBytes {
		rec.body = append(rec.body, b[:min(len(b), maxRefusalLogBytes-len(rec.body))]...)
	}
	return rec.ResponseWriter.Write(b)
}

// Flush keeps the streaming endpoints streaming. Without it the SSE handler's
// http.Flusher assertion fails against this wrapper and every subscriber waits
// for a buffer that is never sent.
func (rec *refusalRecorder) Flush() {
	if flusher, ok := rec.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer, which the
// stream handlers use for deadlines.
func (rec *refusalRecorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

func (rec *refusalRecorder) log(r *http.Request, s participantSession) {
	if rec.status < 400 {
		return
	}
	log.Printf("refused: %s %s status=%d sub=%s connector=%s: %s",
		r.Method, r.URL.Path, rec.status, s.Subject, s.Connector,
		strings.TrimSpace(string(rec.body)))
}
