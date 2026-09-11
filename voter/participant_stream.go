package main

// krm-stream owns watch translation and recovery. This increment still opens
// watches with participant credentials; shared service-account watches are a
// separate integration requiring platform RBAC and subscriber identity mapping.

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/ConfigButler/krm-stream/gateway"
	"github.com/ConfigButler/krm-stream/gateway/kube"
)

// streamPrincipal is what the gateway carries around as "who is calling".
//
// It holds the session rather than a copy of its fields so the token cannot
// drift out of sync with the identity it belongs to: whoever the principal
// says this is, the client below is built from that same session's token.
type streamPrincipal struct {
	session participantSession
}

func registerParticipantStreamHandlers(mux *http.ServeMux, deps handlerDeps) {
	cfg := deps.cfg

	handler := gateway.Handler(gateway.Options{
		// Identity comes from the signed session cookie and nowhere else. The
		// gateway never trusts browser identity headers.
		Principal: func(r *http.Request) (gateway.Principal, error) {
			s, ok := getParticipantSession(r, cfg, sessionCookieCodec, time.Now())
			if !ok {
				return nil, errors.New("no participant session")
			}
			return &streamPrincipal{session: s}, nil
		},

		// Participant-token watches rely on Kubernetes RBAC. SharedBackend will
		// require SubjectAccessReviewAuthorizer and service-account grants.
		Authorizer: participantWatchAuthorizer{namespace: deps.defaultNS, name: cfg.CoffeeConfigName},

		// One client per caller, built from that caller's token. Nothing is
		// cached across principals, so one participant's credential can never
		// serve another's stream.
		Clients: func(_ context.Context, _ string, p gateway.Principal) (gateway.Backend, error) {
			principal, ok := p.(*streamPrincipal)
			if !ok {
				return nil, errors.New("unexpected principal type")
			}
			clients, err := deps.participantClientsFor(principal.session.IDToken)
			if err != nil {
				return nil, err
			}
			return kube.NewBackend(clients.dynamic), nil
		},

		// Deny by default, and narrow on purpose. The audience's RBAC already
		// stops them reading Secrets, but a scope allowlist means a mistake in
		// RBAC is not immediately also a streaming mistake. Two locks.
		Scopes: gateway.ScopePolicy{
			// The single-cluster host that never names a target.
			Targets: []string{""},
			Resources: []gateway.GroupResource{
				{
					Group:    "examples.configbutler.ai",
					Resource: "coffeeconfigs",
					Scope:    gateway.ResourceScopeNamespaced,
				},
			},
		},

		Projection: gateway.ProjectionFull,
	})

	// requireParticipant gives the 401-with-loginUrl shape the SPA already
	// understands, so an expired session on a stream looks like an expired
	// session anywhere else. GET only: a stream is a read.
	mux.HandleFunc("/public/stream", requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		deadline := time.Unix(s.ExpiresAt, 0)
		if s.TokenExpiry > 0 && time.Unix(s.TokenExpiry, 0).Before(deadline) {
			deadline = time.Unix(s.TokenExpiry, 0)
		}
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		defer cancel()
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
}

// Scope restriction precedes any data disclosure; Kubernetes authorizes the
// participant-backed watch itself. This is not a shared-cache authorizer.
type participantWatchAuthorizer struct{ namespace, name string }

func (a participantWatchAuthorizer) Authorize(_ context.Context, p gateway.Principal, scope gateway.Scope) error {
	principal, ok := p.(*streamPrincipal)
	if !ok {
		return gateway.Forbidden("not authenticated")
	}
	if principal.session.IDToken == "" {
		return gateway.Forbidden("no participant credential")
	}
	if scope.Namespace != a.namespace || scope.Name != a.name || scope.Version != "v1alpha1" {
		return gateway.Forbidden("stream is restricted to the configured CoffeeConfig")
	}
	log.Printf("stream: open sub=%s resource=%s ns=%s name=%s",
		principal.session.Subject, scope.Resource, scope.Namespace, scope.Name)
	return nil
}
