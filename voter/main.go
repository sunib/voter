package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/securecookie"
)

const globalDemoAccessCodeKey = "global-demo-access"

// Build information, set via ldflags at build time
var (
	gitCommit = "unknown"
	gitDirty  = "0"
	buildDate = "unknown"
)

func logQuizSessions(kube kubeClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessions, err := kube.listQuizSessions(ctx)
	if err != nil {
		log.Printf("sessions: failed to list quiz sessions: %v", err)
		return
	}
	if len(sessions) == 0 {
		log.Printf("sessions: no quiz sessions found")
		return
	}
	for _, sess := range sessions {
		log.Printf("sessions: namespace=%s name=%s state=%s title=%q", sess.Namespace, sess.Name, sess.State, sess.Title)
	}
}

func rotateGlobalAccessCode(cfg config, codes *joinCodeStore, now time.Time) {
	if strings.TrimSpace(cfg.DemoAccessCode) != "" {
		return
	}
	code, ok, created := codes.ensureActiveCode(globalDemoAccessCodeKey, now)
	if !ok {
		return
	}
	if created {
		log.Printf("demo-access-code: code=%s ttl=%s", code, cfg.JoinCodeTTL)
	}
}

func isValidDemoAccessCode(cfg config, codes *joinCodeStore, code string, now time.Time) bool {
	normalized := normalizeJoinCode(code)
	if normalized == "" {
		return false
	}
	if staticCode := normalizeJoinCode(cfg.DemoAccessCode); staticCode != "" {
		return normalized == staticCode
	}
	return codes.validate(globalDemoAccessCodeKey, normalized, now)
}

func main() {
	dirtyFlag := ""
	if gitDirty == "1" {
		dirtyFlag = " (dirty)"
	}
	log.Printf("voter starting commit=%s%s buildDate=%s", gitCommit, dirtyFlag, buildDate)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	// Refuse to start with a broken bundle rather than serving 500s to every
	// browser that loads the page.
	if err := checkStaticDir(cfg.StaticDir); err != nil {
		log.Fatalf("static: STATIC_DIR=%q is unusable: %v", cfg.StaticDir, err)
	}
	if cfg.StaticDir != "" {
		log.Printf("static: serving the frontend from %s", cfg.StaticDir)
	}

	codes := newJoinCodeStore(cfg)
	kube, err := loadKubeClient(cfg)
	if err != nil {
		log.Fatalf("kube client required: %v", err)
	}

	// OIDC mode and legacy mode are mutually exclusive, and OIDC mode is set up
	// first so a misconfigured deployment fails at boot rather than falling
	// back to browser-asserted identity under load.
	var oidcClient *oidcProvider
	if cfg.OIDCEnabled {
		hashKey, blockKey, keyErr := loadAppCookieKeys(cfg)
		if keyErr != nil {
			log.Fatalf("oidc: application cookie keys unusable: %v", keyErr)
		}
		sc, scErr := newSessionSecureCookie(hashKey, blockKey)
		if scErr != nil {
			log.Fatalf("oidc: secure cookie unavailable: %v", scErr)
		}
		sessionCookieCodec = sc

		bootCtx, bootCancel := context.WithTimeout(context.Background(), 60*time.Second)
		oidcClient, err = newOIDCProvider(bootCtx, cfg)
		bootCancel()
		if err != nil {
			log.Fatalf("oidc: %v", err)
		}
		log.Printf("oidc: enabled issuer=%s client=%s redirect=%s origin=%s",
			cfg.OIDCIssuerURL, cfg.OIDCClientID, cfg.OIDCRedirectURL, cfg.AppOrigin)
	} else {
		log.Printf("WARNING: legacy authentication mode (OIDC_ENABLED=false). " +
			"Browser-asserted identity and ServiceAccount impersonation are active; " +
			"do not expose this deployment to public traffic.")
	}
	if cfg.JoinCodeLength <= 0 {
		cfg.JoinCodeLength = 4
	}

	// The legacy cookie keys are read from -- and if absent, CREATED in -- a
	// Kubernetes Secret at runtime. That needs Secret get/create on the
	// namespace, which OIDC mode must not have: its keys are supplied by the
	// deployment from a pre-created Secret (loadAppCookieKeys, above), so the
	// ServiceAccount needs no Secret access at all. Only take this path when
	// legacy mode is actually in use.
	var sessionCookie *securecookie.SecureCookie
	if !cfg.OIDCEnabled {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hashKey, blockKey, keyErr := ensureSessionCookieKeys(ctx, kube)
		if keyErr != nil {
			log.Fatalf("cookie: failed to ensure session cookie keys: %v", keyErr)
		}
		sessionCookie, err = newSessionSecureCookie(hashKey, blockKey)
		if err != nil {
			log.Fatalf("cookie: failed to initialize secure cookie: %v", err)
		}
	}

	logQuizSessions(kube)
	tokens := newTokenCache()
	orders := newCoffeeRuntime()
	changes := newCoffeeChangeRuntime(64)

	rotateGlobalAccessCode(cfg, codes, time.Now())
	if staticCode := strings.TrimSpace(cfg.DemoAccessCode); staticCode != "" {
		log.Printf("demo-access-code: using static code from DEMO_ACCESS_CODE code=%s", staticCode)
	} else {
		go func() {
			ticker := time.NewTicker(cfg.JoinCodeRotate)
			defer ticker.Stop()
			for range ticker.C {
				rotateGlobalAccessCode(cfg, codes, time.Now())
			}
		}()
	}

	const tokenTTLSeconds int64 = 600 // Not allowed to make smaller than 10 minutes?!

	// The impersonator ServiceAccount belongs to legacy mode only. In OIDC mode
	// there is nothing to impersonate with, and requiring it would invite
	// someone to grant impersonation permissions the demo must not have.
	forwardSa := strings.TrimSpace(cfg.ForwardServiceAccount)
	forwardSaNamespace := strings.TrimSpace(cfg.ForwardServiceAccountNamespace)
	if !cfg.OIDCEnabled {
		if forwardSa == "" {
			log.Fatalf("config error: FORWARD_SA is required in legacy mode")
		}
		if forwardSaNamespace == "" {
			log.Fatalf("config error: FORWARD_SA_NAMESPACE is required in legacy mode")
		}
	}

	mux := http.NewServeMux()

	if oidcClient != nil {
		registerOIDCHandlers(mux, oidcClient, cfg)
	}

	deps := handlerDeps{
		cfg:           cfg,
		codes:         codes,
		kube:          kube,
		orders:        orders,
		changes:       changes,
		sessionCookie: sessionCookie,
		tokens:        tokens,
		defaultNS:     kube.defaultNS,
		forwardSaName: forwardSa,
		forwardSaNS:   forwardSaNamespace,
		tokenTTL:      tokenTTLSeconds,
	}

	// In OIDC mode the participant-token coffee endpoints are registered
	// FIRST, so they own their paths. The legacy handlers below keep serving
	// the storefront and the admin views during migration; they will be
	// deleted with the rest of the legacy path (plan phase 5).
	if oidcClient != nil {
		registerParticipantCoffeeHandlers(mux, deps)
	}

	registerHandlers(mux, deps)

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("listening on %s (SESSION_COOKIE_NAME=%s COOKIE_SECURE=%v)", addr, cfg.SessionCookieName, cfg.CookieSecure)
	log.Fatal(srv.ListenAndServe())
}
