package main

// krm-stream owns watch sharing, queues and recovery. Each subscriber is
// independently authorized against its Kubernetes-resolved identity.

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/ConfigButler/krm-stream/gateway"
	"github.com/ConfigButler/krm-stream/gateway/kube"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// streamPrincipal is what the gateway carries around as "who is calling".
//
// It holds the session rather than a copy of its fields so the token cannot
// drift out of sync with the identity it belongs to: whoever the principal
// says this is, the client below is built from that same session's token.
type streamPrincipal struct {
	session   participantSession
	subject   *kube.Subject
	subjectMu sync.Mutex
}

func registerParticipantStreamHandlers(mux *http.ServeMux, deps handlerDeps) {
	cfg := deps.cfg
	streams := deps.streams
	if streams == nil {
		panic("participant stream handlers require a stream runtime")
	}

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

		Authorizer: gateway.AuthorizerFunc(func(ctx context.Context, p gateway.Principal, scope gateway.Scope) error {
			if err := (participantWatchAuthorizer{namespace: deps.defaultNS, name: cfg.CoffeeConfigName}).Authorize(ctx, p, scope); err != nil {
				return err
			}
			// Bound initial checks as well as the library's timed rechecks.
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			principal, ok := p.(*streamPrincipal)
			if !ok {
				return gateway.Forbidden("not authenticated")
			}
			if err := principal.resolveSubject(ctx, deps); err != nil {
				streams.metrics.reviewFailures.Add(1)
				return err
			}
			err := streams.authorizer.Authorize(ctx, p, scope)
			if err != nil {
				streams.metrics.reviewFailures.Add(1)
			}
			return err
		}),
		Clients: func(_ context.Context, _ string, _ gateway.Principal) (gateway.Backend, error) {
			return streams.backend, nil
		},
		// The library owns bounded delivery: it deadlines each write plus flush,
		// refuses a transport that cannot support that before a stream opens, and
		// poisons the sink on failure so a queued heartbeat cannot revive it.
		WriteTimeout:            streams.writeTimeout,
		ReauthorizationInterval: streams.reauthorizationInterval,
		ReauthorizationTimeout:  5 * time.Second,
		Observer:                streams.metrics,

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
		streams.metrics.subscribers.Add(1)
		defer streams.metrics.subscribers.Add(-1)
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
}

// Scope restriction precedes identity resolution and access review.
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
	return nil
}

// Serialize identity initialization ourselves instead of relying on gateway call
// ordering. Once resolved, this subscription's identity remains fixed.
func (p *streamPrincipal) resolveSubject(ctx context.Context, deps handlerDeps) error {
	p.subjectMu.Lock()
	defer p.subjectMu.Unlock()
	if p.subject == nil {
		clients, err := deps.participantClientsFor(p.session.IDToken)
		if err != nil {
			return err
		}
		review, err := clients.typed.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		info := review.Status.UserInfo
		if info.Username == "" {
			return gateway.Forbidden("missing Kubernetes identity")
		}
		subject := &kube.Subject{User: info.Username, Groups: info.Groups, UID: info.UID, Extra: map[string]authorizationv1.ExtraValue{}}
		for key, values := range info.Extra {
			subject.Extra[key] = authorizationv1.ExtraValue(values)
		}
		p.subject = subject
	}

	return nil
}

func (p *streamPrincipal) kubernetesSubject() (kube.Subject, error) {
	p.subjectMu.Lock()
	defer p.subjectMu.Unlock()
	if p.subject == nil {
		return kube.Subject{}, errors.New("missing Kubernetes identity")
	}
	return *p.subject, nil
}
