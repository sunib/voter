package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/securecookie"
)

type handlerDeps struct {
	cfg           config
	codes         *joinCodeStore
	kube          kubeClient
	sessionCookie *securecookie.SecureCookie
	tokens        *tokenCache
	forwardSaName string
	forwardSaNS   string
	tokenTTL      int64
}

func registerHandlers(mux *http.ServeMux, deps handlerDeps) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("/public/session-info", requireSessionMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		ref, _ := getSessionRef(r)
		now := time.Now()

		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		sess, err := deps.kube.getQuizSession(ctx, ref)
		if err != nil {
			clearSessionCookie(w, deps.cfg)
			http.Error(w, "session lookup failed", http.StatusForbidden)
			return
		}
		if strings.TrimSpace(sess.Spec.State) != "live" {
			clearSessionCookie(w, deps.cfg)
			http.Error(w, "session not live", http.StatusForbidden)
			return
		}

		// Set/refresh session cookie if join code was used
		if wasResolvedViaJoinCode(r) && deps.sessionCookie != nil {
			if err := setSessionCookie(w, deps.cfg, deps.sessionCookie, ref, now); err != nil {
				log.Printf("cookie: failed to set session cookie: %v", err)
			}
		}

		payload := map[string]string{
			"namespace": ref.namespace,
			"name":      ref.name,
			"state":     sess.Spec.State,
			"title":     sess.Spec.Title,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(payload)
	}))

	// Traefik forwardAuth endpoint.
	// Flow: join code (replaces session) → fallback to cookie → verify URI → issue token
	// Note: We trust the signed session cookie without re-fetching the QuizSession
	// on every request. The cookie has its own expiry and is cryptographically signed.
	mux.HandleFunc("/private/forward-auth-decision", requireSessionMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		forwardedURI := r.Header.Get("X-Forwarded-Uri")
		if forwardedURI == "" {
			http.Error(w, "missing X-Forwarded-Uri header", http.StatusBadRequest)
			return
		}

		forwardedMethod := r.Header.Get("X-Forwarded-Method")
		if forwardedMethod == "" {
			http.Error(w, "missing X-Forwarded-Method header", http.StatusBadRequest)
			return
		}

		log.Printf("forward-auth-decision: %s X-Forwarded-Uri=%s ip=%s ua=%q", forwardedMethod, forwardedURI, clientIP(r), r.UserAgent())

		// So we want to limit what it can create: the name should be limited
		// Check it here
		// Leave it to kube-api-server -> feels dangerous, but is it?
		// Can we create a special resource to make create explicit for a 'regex' name?

		ref, _ := getSessionRef(r)
		now := time.Now()
		// Check forwarded URI matches session (if present)
		// Should we even do this? You could also leave it the KubeAPI server?
		if uriRef, hasRef := parseSessionRef(forwardedURI); hasRef && uriRef != ref {
			http.Error(w, "You are not authorized for this resource", http.StatusForbidden)
			return
		}

		// Long story short: it's better to give a unique namespace... Then it also makes sense to limit that on url level...
		// And we should be able to create a namespace during this process?
		// But can I watch for changes without a namespace?

		// So there is two ways to get the audit logging right:
		// "Trusted authenticator": you pass headers to kube-api-server and it will just trust that we did our job right (dangerous but powerfull)
		// "Impersonating authenticator": you pass an extra header and ask the kube-api-server to impersonate (you need "Impersonate" rights to do that and the logs will show both the auths service account and the impersonated account in a seperate column)

		// Get forwarding token (using session reference as cache key)
		const skew = 20 * time.Second
		tokenToUse, err := getOrRequestToken(deps.tokens, deps.kube, "shared", now, skew, deps.forwardSaNS, deps.forwardSaName, nil, deps.tokenTTL, r.Context())
		if err != nil {
			log.Printf("auth: token request failed for sa=%s/%s: %v", deps.forwardSaNS, deps.forwardSaName, err)
			http.Error(w, "token request failed", http.StatusForbidden)
			return
		}

		w.Header().Set("Authorization", fmt.Sprintf("Bearer %s", tokenToUse))
		w.Header().Set("X-Auth-Forwarder", "ok")
		w.WriteHeader(http.StatusOK)

		// We can also make this a bit bigger and then allow them to write in a single namespace... -> but keep it limited to that and actually review what they did with a LLM!
		// And only then we bring the data into a bigger set... -> Let's see what people try to do?

		// It becomes more acceptable: but still is a liability, should I have 'default' patterns for allowing people to self create? Could also limit the amount of resources that you create at once?
	}))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("auth-forwarder prototype\n\nEndpoints:\n- GET /healthz\n- GET|POST /private/forward-auth-decision (Traefik ForwardAuth)\n\n- GET|POST /public/session-info (Get info on current sessions)\n"))
			return
		}
		http.NotFound(w, r)
	})
}
