package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

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

func rotateJoinCodesForLiveSessions(kube kubeClient, codes *joinCodeStore) {
	listCtx, listCancel := context.WithTimeout(context.Background(), 5*time.Second)
	sessions, err := kube.listQuizSessions(listCtx)
	listCancel()
	if err != nil {
		log.Printf("join-code: failed to list sessions: %v", err)
		return
	}
	now := time.Now()
	for _, sess := range sessions {
		ref := sessionRef{namespace: sess.Namespace, name: sess.Name}
		var code string
		if strings.TrimSpace(sess.State) == "live" {
			var ok bool
			code, ok = codes.rotateAndGet(sessionKey(ref), now)
			if !ok {
				continue
			}
			log.Printf("join-code: namespace=%s name=%s code=%s", sess.Namespace, sess.Name, code)
		}
		patchCtx, patchCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := kube.patchQuizSessionJoinCode(patchCtx, ref, code); err != nil {
			log.Printf("join-code: failed to update status namespace=%s name=%s: %v", sess.Namespace, sess.Name, err)
		}
		patchCancel()
	}
}

func main() {
	dirtyFlag := ""
	if gitDirty == "1" {
		dirtyFlag = " (dirty)"
	}
	log.Printf("auth-service starting commit=%s%s buildDate=%s", gitCommit, dirtyFlag, buildDate)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	codes := newJoinCodeStore(cfg)
	kube, err := loadKubeClient(cfg)
	if err != nil {
		log.Fatalf("kube client required: %v", err)
	}
	if cfg.JoinCodeLength <= 0 {
		cfg.JoinCodeLength = 4
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hashKey, blockKey, err := ensureSessionCookieKeys(ctx, kube)
	if err != nil {
		log.Fatalf("cookie: failed to ensure session cookie keys: %v", err)
	}
	sessionCookie, err := newSessionSecureCookie(hashKey, blockKey)
	if err != nil {
		log.Fatalf("cookie: failed to initialize secure cookie: %v", err)
	}

	logQuizSessions(kube)
	tokens := newTokenCache()

	// Trigger first token generation immediately at startup
	rotateJoinCodesForLiveSessions(kube, codes)

	// Then start periodic rotation
	go func() {
		ticker := time.NewTicker(cfg.JoinCodeRotate)
		defer ticker.Stop()
		for range ticker.C {
			rotateJoinCodesForLiveSessions(kube, codes)
		}
	}()

	const tokenTTLSeconds int64 = 600 // Not allowed to make smaller tahn 10 minbutes?!
	forwardSa := strings.TrimSpace(cfg.ForwardServiceAccount)
	if forwardSa == "" {
		log.Fatalf("config error: FORWARD_SA is required")
	}

	forwardSaNamespace := strings.TrimSpace(cfg.ForwardServiceAccountNamespace)
	if forwardSaNamespace == "" {
		log.Fatalf("config error: FORWARD_SA_NAMESPACE is required")
	}

	mux := http.NewServeMux()

	registerHandlers(mux, handlerDeps{
		cfg:           cfg,
		codes:         codes,
		kube:          kube,
		sessionCookie: sessionCookie,
		tokens:        tokens,
		forwardSaName: forwardSa,
		forwardSaNS:   forwardSaNamespace,
		tokenTTL:      tokenTTLSeconds,
	})

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("listening on %s (SESSION_COOKIE_NAME=%s COOKIE_SECURE=%v)", addr, cfg.SessionCookieName, cfg.CookieSecure)
	log.Fatal(srv.ListenAndServe())
}
