package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	api "github.com/sunib/voter/room-pass/api/v1alpha1"
	"github.com/sunib/voter/room-pass/internal/controller"
	"github.com/sunib/voter/room-pass/internal/server"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func number(k string, d int) int {
	v, e := strconv.Atoi(env(k, strconv.Itoa(d)))
	if e != nil || v <= 0 {
		log.Fatalf("invalid %s", k)
	}
	return v
}
func main() {
	if e := run(); e != nil {
		log.Fatal(e)
	}
}
func run() error {
	ctrl.SetLogger(logr.FromSlogHandler(slog.Default().Handler()))
	ctx := ctrl.SetupSignalHandler()
	scheme := runtime.NewScheme()
	_ = api.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	cfg := ctrl.GetConfigOrDie()
	cfg.QPS = float32(number("KUBE_QPS", 100))
	cfg.Burst = number("KUBE_BURST", 200)
	cfg.Timeout = 8 * time.Second
	ns := env("ROOM_NAMESPACE", "room-pass")
	key := client.ObjectKey{Namespace: ns, Name: env("ROOM_NAME", "demo")}
	db, e := client.New(cfg, client.Options{Scheme: scheme})
	if e != nil {
		return e
	}
	secret := &corev1.Secret{}
	if e = db.Get(ctx, client.ObjectKey{Namespace: ns, Name: env("COOKIE_SECRET", "room-pass-cookie")}, secret); e != nil {
		return fmt.Errorf("cookie secret: %w", e)
	}
	app, e := server.New(server.Config{Room: key, JoinOrigin: os.Getenv("JOIN_ORIGIN"), IssuerOrigin: os.Getenv("ISSUER_ORIGIN"), DexUpstream: os.Getenv("DEX_UPSTREAM"), AllowedReturns: strings.Split(os.Getenv("ALLOWED_RETURN_URLS"), ","), HashKey: secret.Data["hash-key"], BlockKey: secret.Data["block-key"], CookieLifetime: time.Duration(number("COOKIE_LIFETIME_SECONDS", 86400)) * time.Second, JoinRate: rate.Limit(number("JOIN_RATE", 20)), JoinBurst: number("JOIN_BURST", 150), HandoffRate: rate.Limit(number("HANDOFF_RATE", 20)), HandoffBurst: number("HANDOFF_BURST", 150), MaxHandoffs: number("MAX_HANDOFFS", 1000)}, db)
	if e != nil {
		return e
	}
	mgr, e := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Cache: cache.Options{DefaultNamespaces: map[string]cache.Config{ns: {}}}, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0", LeaderElection: false})
	if e != nil {
		return e
	}
	if e = ctrl.NewControllerManagedBy(mgr).For(&api.Room{}).Complete(&controller.Reconciler{Client: db, Room: key}); e != nil {
		return e
	}
	httpServer := &http.Server{Addr: env("LISTEN_ADDR", ":8080"), Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	errs := make(chan error, 2)
	go func() { errs <- mgr.Start(ctx) }()
	go func() { errs <- httpServer.ListenAndServe() }()
	select {
	case e = <-errs:
	case <-ctx.Done():
	}
	stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(stop)
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
