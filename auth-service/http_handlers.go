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

type publicBuildInfoResponse struct {
	GitCommit       string `json:"gitCommit"`
	IsDirty         bool   `json:"isDirty"`
	BuildDate       string `json:"buildDate"`
	CommitWithDirty string `json:"commitWithDirty"`
}

type handlerDeps struct {
	cfg              config
	codes            *joinCodeStore
	kube             kubeHandler
	orders           *coffeeRuntime
	changes          *coffeeChangeRuntime
	sessionCookie    *securecookie.SecureCookie
	tokens           *tokenCache
	defaultNS        string
	forwardSaName    string
	forwardSaNS      string
	kubeconfigSaName string
	kubeconfigSaNS   string
	tokenTTL         int64
}

func registerHandlers(mux *http.ServeMux, deps handlerDeps) {
	registerCoffeeHandlers(mux, deps)

	mux.HandleFunc("/public/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var body struct {
			Code        string `json:"code"`
			StableID    string `json:"stableId"`
			DisplayName string `json:"displayName"`
			Email       string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid login body", http.StatusBadRequest)
			return
		}

		identity, err := audienceIdentityFromSession(body.StableID, body.DisplayName, body.Email)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !isValidDemoAccessCode(deps.cfg, deps.codes, body.Code, time.Now()) {
			clearSessionCookie(w, deps.cfg)
			http.Error(w, "invalid access code", http.StatusUnauthorized)
			return
		}
		if err := setSessionCookie(w, deps.cfg, deps.sessionCookie, identity, body.StableID, time.Now()); err != nil {
			http.Error(w, "failed to create session", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/public/session", requireSessionMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		session, ok := getBrowserSession(r)
		if !ok {
			clearSessionCookie(w, deps.cfg)
			http.Error(w, "session required", http.StatusUnauthorized)
			return
		}
		namespace := deps.defaultNS
		if namespace == "" {
			namespace = "default"
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"stableId":    session.StableID,
			"displayName": session.DisplayName,
			"email":       session.Email,
			"namespace":   namespace,
		})
	}))

	mux.HandleFunc("/public/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clearSessionCookie(w, deps.cfg)
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("/public/build-info", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		commitWithDirty := gitCommit
		if gitDirty == "1" {
			commitWithDirty = fmt.Sprintf("%s-dirty", gitCommit)
		}

		writeJSON(w, http.StatusOK, publicBuildInfoResponse{
			GitCommit:       gitCommit,
			IsDirty:         gitDirty == "1",
			BuildDate:       buildDate,
			CommitWithDirty: commitWithDirty,
		})
	})

	mux.HandleFunc("/public/session-info", requireSessionMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimSpace(r.URL.Query().Get("session"))
		if name == "" {
			http.Error(w, "session query parameter is required", http.StatusBadRequest)
			return
		}

		namespace := deps.defaultNS
		if namespace == "" {
			namespace = "default"
		}
		ref := sessionRef{namespace: namespace, name: name}

		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		sess, err := deps.kube.getQuizSession(ctx, ref)
		if err != nil {
			http.Error(w, "session lookup failed", http.StatusForbidden)
			return
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
	//   2. No bearer token (browser) → require the shared demo session and mint a short-lived token.
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

		// Path 2: browser flow — require the shared session and mint a token.
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

			// Derive the audience identity from the session BEFORE minting a
			// token. A malformed identity is fail-closed (401) — we never want
			// to return a 200 with Authorization but no Impersonate-User, since
			// Traefik would still attach the bearer token upstream and the API
			// server would then act as the raw impersonator SA.
			session, ok := getBrowserSession(r)
			if !ok {
				http.Error(w, "session required", http.StatusUnauthorized)
				return
			}
			identity, err := audienceIdentityFromSession(session.StableID, session.DisplayName, session.Email)
			if err != nil {
				log.Printf("forward-auth-decision: identity derivation failed ip=%s: %v", clientIP(r), err)
				http.Error(w, "invalid session identity", http.StatusUnauthorized)
				return
			}

			log.Printf("forward-auth-decision: %s X-Forwarded-Uri=%s ip=%s ua=%q user=%s", forwardedMethod, forwardedURI, clientIP(r), r.UserAgent(), identity.Username)

			now := time.Now()

			// All browser sessions now share the same short-lived forwarding token.
			// The token is for the impersonator SA, which has impersonate-only
			// RBAC. The actual authorization happens against the impersonated
			// audience user (voter-audience group).
			const skew = 20 * time.Second
			tokenToUse, err := getOrRequestToken(deps.tokens, deps.kube, "shared", now, skew, deps.forwardSaNS, deps.forwardSaName, nil, deps.tokenTTL, r.Context())
			if err != nil {
				log.Printf("auth: token request failed for sa=%s/%s: %v", deps.forwardSaNS, deps.forwardSaName, err)
				http.Error(w, "token request failed", http.StatusForbidden)
				return
			}

			w.Header().Set("Authorization", fmt.Sprintf("Bearer %s", tokenToUse))
			// Impersonation headers — Traefik must list these in
			// authResponseHeaders so they reach the kube-apiserver. The
			// audit/RBAC identity will be the impersonated user, not the SA.
			w.Header().Set("Impersonate-User", identity.Username)
			w.Header().Set("Impersonate-Group", audienceGroupName)
			if deps.cfg.ConfigButlerIdentityExtrasEnabled {
				// Preserve the percent-encoded extra key spelling Traefik is
				// configured to forward; Header.Set would MIME-canonicalize it.
				w.Header()[impersonateExtraHeaderName(configButlerDisplayNameExtraKey)] = []string{identity.DisplayName}
				w.Header()[impersonateExtraHeaderName(configButlerEmailExtraKey)] = []string{identity.Email}
			}
			w.Header().Set("X-Auth-Forwarder", "ok")
			w.WriteHeader(http.StatusOK)
		})(w, r)
	})

	// Returns a ready-to-use kubeconfig for kubectl access.
	// Requires the shared demo session cookie.
	//
	// Usage (one-liner for the talk):
	//   export KUBECONFIG=<(curl -s "https://<host>/auth/kubeconfig?code=XXXX")
	//   kubectl get quizsessions.examples.configbutler.ai -n voter
	mux.HandleFunc("/public/kubeconfig", requireSessionMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		namespace := deps.defaultNS
		if namespace == "" {
			namespace = "default"
		}
		saName := deps.kubeconfigSaName
		if saName == "" {
			saName = deps.forwardSaName
		}
		saNS := deps.kubeconfigSaNS
		if saNS == "" {
			saNS = deps.forwardSaNS
		}
		log.Printf("kubeconfig: minting token for namespace=%s sa=%s/%s ip=%s", namespace, saNS, saName, clientIP(r))

		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		token, expiresAt, err := deps.kube.requestToken(ctx, saNS, saName, nil, deps.tokenTTL)
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
			Namespace: namespace,
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
			_, _ = w.Write([]byte("auth-forwarder prototype\n\nEndpoints:\n- GET /healthz\n- GET /public/build-info\n- GET|POST /private/forward-auth-decision (Traefik ForwardAuth)\n- GET|POST /public/session-info (Get info on current sessions)\n- GET /public/kubeconfig?code=XXXX (Download kubeconfig for kubectl access)\n"))
			return
		}
		http.NotFound(w, r)
	})
}
