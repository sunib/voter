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
		// Rounds opening and closing reach every phone in the room without a
		// reload. Participants already hold get/list/watch on these.
		{
			Group:    "examples.configbutler.ai",
			Resource: "quizsessions",
			Scope:    gateway.ResourceScopeNamespaced,
		},
		// The database requests, live. This is a COLLECTION watch: the list
		// page is the one that has to redraw when another team files a request
		// during the talk, which is the whole point of showing it.
		{
			Group:    "platform.configbutler.ai",
			Resource: "databases",
			Scope:    gateway.ResourceScopeNamespaced,
		},
		// The receipt for a save, followed until ConfigButler reports the
		// commit pushed. The page opens this pinned to the ONE CommitRequest
		// its own save created, whose name it learns from the write's response
		// -- it is never a collection watch in practice, even though the
		// allowlist below would admit one.
		//
		// Nothing here is credential material: a CommitRequest carries the
		// requester's own sentence and the sha it became, and the sentence is
		// already on its way into a Git commit message in public.
		{
			Group:    "configbutler.ai",
			Resource: "commitrequests",
			Scope:    gateway.ResourceScopeNamespaced,
		},
		// The operator page's QR code follows the join code as it rotates.
		// Allowlisting the KIND is not permission to read one: the join code is
		// operator-only credential material living in the Room's status, and a
		// participant's RBAC grants nothing on rooms at all. That refusal is
		// Kubernetes', not this allowlist's, which is the whole point of having
		// both -- see room-pass/cmd/room-qr for why this material is guarded.
		{
			Group:    "roompass.configbutler.ai",
			Resource: "rooms",
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
	pinned := streamAllowlist{
		{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "coffeeconfigs", Namespace: deps.defaultNS, Name: cfg.CoffeeConfigName},
		{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "quizsessions", Namespace: deps.defaultNS},
		{Group: "platform.configbutler.ai", Version: "v1alpha1", Resource: "databases", Namespace: deps.defaultNS},
		// Empty name: a CommitRequest is created with generateName, so the one
		// name worth pinning does not exist until the save that makes it.
		{Group: "configbutler.ai", Version: "v1alpha3", Resource: "commitrequests", Namespace: deps.defaultNS},
		{Group: "roompass.configbutler.ai", Version: "v1alpha1", Resource: "rooms", Namespace: deps.defaultNS, Name: cfg.RoomName},
	}

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

// streamAllowlist pins the exact objects this stream serves. scopePolicy above
// says which KINDS may be streamed at all; this says WHICH ONES of them, so a
// newly granted RBAC verb never silently widens what the endpoint will carry.
//
// An entry whose name is empty admits that whole collection in the namespace,
// and a single object within it. That is deliberate for quizsessions, where the
// point is watching rounds appear and change state; it is not a second grant,
// because every subscriber is still access-reviewed as itself.
type streamAllowlist []gateway.Scope

func (a streamAllowlist) check(scope gateway.Scope) error {
	for _, allowed := range a {
		switch {
		case scope.Group != allowed.Group, scope.Resource != allowed.Resource:
		case scope.Version != allowed.Version, scope.Namespace != allowed.Namespace:
		case allowed.Name != "" && scope.Name != allowed.Name:
		default:
			return nil
		}
	}
	return gateway.Forbidden("stream is restricted to this demo's own objects")
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
