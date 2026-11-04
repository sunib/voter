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
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"
)

const shutdownTimeout = 20 * time.Second

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

	// One or the other. The reconcilers below need the client, and they sit behind
	// !ShopOnly, which SETTINGS_DIR requires.
	var serviceAccount dynamic.Interface
	var loaded *settings
	if cfg.SettingsDir != "" {
		loaded, err = loadSettings(cfg)
		if err != nil {
			log.Fatalf("settings: %v", err)
		}
		log.Printf("settings: serving menu %s, revision %s, from %s", cfg.CoffeeConfigName, loaded.revision, cfg.SettingsDir)
	} else {
		serviceAccount, err = newServiceAccountClient(&cfg)
		if err != nil {
			log.Fatalf("kubernetes: %v", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	// Beside serving and never before it: keys must not hold up readiness.
	if loaded != nil && loaded.keys != nil {
		go loaded.keys.run(ctx)
	}

	deps := handlerDeps{
		cfg:            cfg,
		serviceAccount: serviceAccount,
		settings:       loaded,
		defaultNS:      applicationNamespace(cfg),
		vouchers:       newVoucherLedger(),
		orders:         newOrderLog(),
	}

	mux := http.NewServeMux()
	registerJoinRoomHandler(mux)
	registerParticipantStorefrontHandlers(mux, deps)
	registerParticipantOrderFeedHandlers(mux, deps)
	// Last: it owns "/" and therefore everything unclaimed above.
	registerHandlers(mux, deps)

	// The round's result is a field on the round, kept there by a controller
	// that watches the API rather than this application's vote handler -- so a
	// ballot written with kubectl moves the projected number exactly as one
	// typed on a phone does. docs/live-results-design.md.
	//
	// Started before serving: the results screen has no other source, so the
	// tally must be running before the first phone connects. It runs for the
	// life of the process rather than on ctx: a half-stopped tally would be worse
	// than a stopped one, and only requests are worth finishing on the way out.
	//
	// Not in a shop-only deployment: it has no rounds, and its ServiceAccount
	// may not read them.
	if !cfg.ShopOnly {
		go newQuizReconciler(serviceAccount, deps.defaultNS).Run(context.Background())
	}
	// A seat's last why, from its committed CommitRequest onto its status
	// (seat_reconciler.go). Only with a seats GitTarget: without one there are
	// no requests to read, and perhaps no CommitRequest type to list either.
	if target := strings.TrimSpace(cfg.ConfigButlerSeatGitTargetName); !cfg.ShopOnly && target != "" {
		requestNS := strings.TrimSpace(cfg.ConfigButlerCommitRequestNamespace)
		if requestNS == "" {
			requestNS = deps.defaultNS
		}
		go newSeatReconciler(serviceAccount, deps.defaultNS, requestNS, target).Run(context.Background())
	}

	// Whether the menu's keys are set, onto the menu's status
	// (coffee_keys_reconciler.go). Not in a shop-only deployment: it has no
	// editor, and its ServiceAccount may not read those Secrets.
	if !cfg.ShopOnly {
		go newCoffeeKeysReconciler(serviceAccount, deps.defaultNS, cfg.CoffeeConfigName).Run(context.Background())
	}

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("listening on %s", addr)
	if err := serve(ctx, srv, listener); err != nil {
		log.Fatalf("serve: %v", err)
	}
	log.Print("stopped")
}

// serve runs srv until ctx ends, then lets the requests in flight finish.
//
// The wait for the edge to stop sending new ones is not here: it is a preStop
// sleep in a Deployment that does rolling updates, so one that does not (demo)
// just stops. shutdownTimeout has to fit in the grace period after that sleep.
func serve(ctx context.Context, srv *http.Server, listener net.Listener) error {
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	log.Print("stopping: finishing the requests in flight")
	shutdown, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return err
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
