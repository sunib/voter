package main

// The Voter/Coffee application.
//
// Login is Dex + Room Pass, and that is the ONLY authentication model. There
// used to be a second one behind OIDC_ENABLED=false, where the browser asserted
// its own identity and this service impersonated it with its ServiceAccount.
// It is gone: two ways to become a participant in one binary, one of which
// trusted whatever the browser said, was the single largest source of
// confusion in this codebase.
//
// What this process does NOT do, and must not be made to do:
//
//   - accept an identity from a browser. The subject comes from a signed Dex
//     token and nowhere else.
//   - mint a token of its own. Kubernetes trusts Dex, not this service.
//   - impersonate. Each participant's own ID token is the credential used
//     against the Kubernetes API, so a denial is a 403 the participant sees,
//     never a silent retry as the ServiceAccount.

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"
)

// Build information, set via ldflags at build time
var (
	gitCommit = "unknown"
	gitDirty  = "0"
	buildDate = "unknown"
)

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

	// The OIDC client is built before anything serves, so a misconfigured
	// deployment fails at boot with a clear message instead of failing every
	// login later.
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
	oidcClient, err := newOIDCProvider(bootCtx, cfg)
	bootCancel()
	if err != nil {
		log.Fatalf("oidc: %v", err)
	}
	log.Printf("oidc: issuer=%s client=%s redirect=%s origin=%s connector=%q",
		cfg.OIDCIssuerURL, cfg.OIDCClientID, cfg.OIDCRedirectURL, cfg.AppOrigin, cfg.OIDCConnectorID)

	deps := handlerDeps{
		cfg:       cfg,
		defaultNS: applicationNamespace(cfg),
		vouchers:  newVoucherLedger(),
	}

	mux := http.NewServeMux()
	registerOIDCHandlers(mux, oidcClient, cfg, deps.defaultNS)
	registerParticipantCoffeeHandlers(mux, deps)
	registerParticipantStorefrontHandlers(mux, deps)
	registerParticipantStreamHandlers(mux, deps)
	// Last: it owns "/" and therefore everything unclaimed above.
	registerHandlers(mux, deps)

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}
