package main

// krm-stream owns watch sharing, queues and recovery. Each subscriber is
// independently authorized against its Kubernetes-resolved identity.

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/ConfigButler/krm-stream/gateway"
	"github.com/ConfigButler/krm-stream/gateway/kube"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// scopePolicy denies by default and narrows on purpose. The audience's RBAC
// already stops them reading Secrets, but a scope allowlist means a mistake in
// RBAC is not immediately also a streaming mistake. Two locks.
var scopePolicy = gateway.ScopePolicy{
	// The single-cluster host that never names a target.
	Targets: []string{""},
	Resources: []gateway.GroupResource{
		{
			Group:    "examples.configbutler.ai",
			Resource: "coffeeconfigs",
			Scope:    gateway.ResourceScopeNamespaced,
		},
	},
}

// refusal carries an already-decided refusal to the gateway as a principal.
//
// Voter needs to check the scope BEFORE spending a SelfSubjectReview on it, but
// it also wants the library to frame the answer -- SSE headers, sequencing and
// the terminal event are the library's job, and 0.4.0 exports no way to write
// them by hand. Handing the gateway a principal that authorizes to this error
// gets both: no Kubernetes call for a scope that was never going to be served,
// and a terminal event whose code still distinguishes an unallowlisted resource
// from a forbidden one.
type refusal struct{ err error }

func registerParticipantStreamHandlers(mux *http.ServeMux, deps handlerDeps) {
	cfg := deps.cfg
	streams := deps.streams
	if streams == nil {
		panic("participant stream handlers require a stream runtime")
	}
	pinned := coffeeConfigScope{namespace: deps.defaultNS, name: cfg.CoffeeConfigName}

	g := &gateway.Gateway{
		Auth: gateway.AuthorizerFunc(func(ctx context.Context, p gateway.Principal, scope gateway.Scope) error {
			if r, ok := p.(refusal); ok {
				return r.err
			}
			// Bound initial checks as well as the library's timed rechecks.
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			err := streams.authorizer.Authorize(ctx, p, scope)
			if err != nil {
				streams.metrics.reviewFailures.Add(1)
			}
			return err
		}),
		Clients: func(_ context.Context, _ string, _ gateway.Principal) (gateway.Backend, error) {
			return streams.backend, nil
		},
		Projection: gateway.ProjectionFull,
		Observer:   streams.metrics,
		// The library owns bounded delivery: it deadlines each write plus flush,
		// refuses a transport that cannot support that before a stream opens, and
		// poisons the sink on failure so a queued heartbeat cannot revive it.
		WriteTimeout:            streams.writeTimeout,
		ReauthorizationInterval: streams.reauthorizationInterval,
		ReauthorizationTimeout:  5 * time.Second,
	}

	// requireParticipant gives the 401-with-loginUrl shape the SPA already
	// understands, so an expired session on a stream looks like an expired
	// session anywhere else. GET only: a stream is a read.
	mux.HandleFunc("/public/stream", requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Counted from here, not from the stream: the difference between this
		// and voter_stream_logical_streams is the requests currently resolving
		// identity, which is where a 200-viewer opening burst shows up first.
		streams.metrics.subscribers.Add(1)
		defer streams.metrics.subscribers.Add(-1)

		deadline := time.Unix(s.ExpiresAt, 0)
		if s.TokenExpiry > 0 && time.Unix(s.TokenExpiry, 0).Before(deadline) {
			deadline = time.Unix(s.TokenExpiry, 0)
		}
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		defer cancel()

		// The order below is the point of doing this by hand: every cheap check
		// that can refuse the request runs before the one Kubernetes call that
		// identity costs. A scope this endpoint never serves is answered without
		// touching the API server at all.
		scope, err := gateway.ScopeFromQuery(r.URL.Query())
		if err == nil {
			err = scopePolicy.Validate(scope)
		}
		if err == nil {
			err = pinned.check(scope)
		}
		if err == nil && s.IDToken == "" {
			err = gateway.Forbidden("no participant credential")
		}

		// Identity comes from the signed session cookie and nowhere else; the
		// gateway never sees browser identity headers. Once resolved it is fixed
		// for this subscription, and every disclosure is still access-reviewed.
		var principal gateway.Principal = refusal{err}
		if err == nil {
			identify, done := context.WithTimeout(ctx, 5*time.Second)
			subject, resolveErr := resolveParticipantSubject(identify, deps, s.IDToken)
			done()
			if resolveErr != nil {
				streams.metrics.reviewFailures.Add(1)
				principal = refusal{resolveErr}
			} else {
				principal = subject
			}
		}

		g.ServeStreamProjection(w, r.WithContext(ctx), principal, scope, gateway.ProjectionFull)
	}))
}

// coffeeConfigScope pins the one object this stream serves. scopePolicy above
// says which KIND may be streamed at all; this says WHICH ONE.
type coffeeConfigScope struct{ namespace, name string }

func (a coffeeConfigScope) check(scope gateway.Scope) error {
	if scope.Namespace != a.namespace || scope.Name != a.name || scope.Version != "v1alpha1" {
		return gateway.Forbidden("stream is restricted to the configured CoffeeConfig")
	}
	return nil
}

// The client here MUST authenticate as the participant. Using the shared
// service-account client would resolve the service account instead, and every
// subscriber would then be authorized as Voter itself.
func resolveParticipantSubject(ctx context.Context, deps handlerDeps, idToken string) (kube.Subject, error) {
	clients, err := deps.participantClientsFor(idToken)
	if err != nil {
		return kube.Subject{}, err
	}
	review, err := clients.typed.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		return kube.Subject{}, err
	}
	info := review.Status.UserInfo
	if info.Username == "" {
		return kube.Subject{}, gateway.Forbidden("missing Kubernetes identity")
	}
	subject := kube.Subject{User: info.Username, UID: info.UID, Groups: slices.Clone(info.Groups)}
	if info.Extra != nil {
		subject.Extra = make(map[string]authorizationv1.ExtraValue, len(info.Extra))
		for key, values := range info.Extra {
			subject.Extra[key] = slices.Clone(authorizationv1.ExtraValue(values))
		}
	}
	return subject, nil
}
