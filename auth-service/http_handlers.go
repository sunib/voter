package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/gorilla/securecookie"
)

//go:embed kubeconfig.tmpl
var kubeconfigTemplateStr string

var kubeconfigTmpl = template.Must(template.New("kubeconfig").Parse(kubeconfigTemplateStr))

type kubeconfigData struct {
	ServerURL string
	Token     string
	Namespace string
	ExpiresAt string
}

type handlerDeps struct {
	cfg           config
	codes         *joinCodeStore
	kube          kubeHandler
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
	// Two paths:
	//   1. Bearer token already present (kubectl with kubeconfig) → pass it straight through to K8s.
	//   2. No bearer token (browser) → resolve session via join code / cookie and mint a short-lived token.
	mux.HandleFunc("/private/forward-auth-decision", func(w http.ResponseWriter, r *http.Request) {
		// Path 1: kubectl / direct API access — request carries a bearer token from the kubeconfig.
		// Validate it via TokenReview before forwarding; don't blindly pass through arbitrary tokens.
		if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimPrefix(auth, "Bearer ")
			ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
			authenticated, username, err := deps.kube.reviewToken(ctx, token)
			cancel()
			if err != nil {
				log.Printf("forward-auth-decision: token review error ip=%s: %v", clientIP(r), err)
				http.Error(w, "token review failed", http.StatusInternalServerError)
				return
			}
			if !authenticated {
				log.Printf("forward-auth-decision: token not authenticated ip=%s ua=%q", clientIP(r), r.UserAgent())
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			log.Printf("forward-auth-decision: passthrough bearer user=%s ip=%s ua=%q", username, clientIP(r), r.UserAgent())
			w.Header().Set("Authorization", auth)
			w.Header().Set("X-Auth-Forwarder", "passthrough")
			w.WriteHeader(http.StatusOK)
			return
		}

		// Path 2: browser flow — require a session (join code or cookie) and mint a token.
		requireSessionMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
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
		})(w, r)
	})

	// Returns a ready-to-use kubeconfig for kubectl access.
	// Accepts a join code (?code=XXXX) or an existing session cookie — same as all other /public/ endpoints.
	//
	// Usage (one-liner for the talk):
	//   export KUBECONFIG=<(curl -s "https://<host>/auth/kubeconfig?code=XXXX")
	//   kubectl get quizsessions.examples.configbutler.ai -n voter
	mux.HandleFunc("/public/kubeconfig", requireSessionMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ref, _ := getSessionRef(r)
		log.Printf("kubeconfig: minting token for session=%s/%s ip=%s", ref.namespace, ref.name, clientIP(r))

		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		token, expiresAt, err := deps.kube.requestToken(ctx, deps.forwardSaNS, deps.forwardSaName, nil, deps.tokenTTL)
		if err != nil {
			log.Printf("kubeconfig: token request failed: %v", err)
			http.Error(w, "token request failed", http.StatusInternalServerError)
			return
		}

		// Derive server URL from the incoming request (Traefik sets X-Forwarded-* headers).
		host := r.Host
		if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
			host = fh
		}
		proto := "https"
		if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
			proto = fp
		}
		serverURL := fmt.Sprintf("%s://%s", proto, host)

		var buf bytes.Buffer
		if err := kubeconfigTmpl.Execute(&buf, kubeconfigData{
			ServerURL: serverURL,
			Token:     token,
			Namespace: ref.namespace,
			ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		}); err != nil {
			log.Printf("kubeconfig: template execution failed: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/x-yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
	}))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("auth-forwarder prototype\n\nEndpoints:\n- GET /healthz\n- GET|POST /private/forward-auth-decision (Traefik ForwardAuth)\n- GET|POST /public/session-info (Get info on current sessions)\n- GET /public/kubeconfig?code=XXXX (Download kubeconfig for kubectl access)\n"))
			return
		}
		http.NotFound(w, r)
	})
}
