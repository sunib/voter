package main

// Live resource updates for the browser, via krm-stream.
//
// The demo's point is that Kubernetes is the application API. That reads very
// differently when a change someone makes on stage appears on every phone in
// the room a moment later, without anybody refreshing -- so this is not a
// nicety, it is the part of the story that has to be seen to land.
//
// krm-stream (github.com/ConfigButler/krm-stream) does the hard half: it turns
// a Kubernetes watch into SSE an EventSource can read, coalesces many browsers
// onto fewer upstream watches, and gives the client a three-way merge so a live
// update reconciles with what someone is typing instead of clobbering it.
//
// The reason it fits THIS application rather than merely being available:
//
//   - A browser's EventSource cannot send an Authorization header. The library's
//     recommended route is therefore a same-origin HttpOnly cookie plus a server
//     that custodies the token -- which is exactly the session Voter already has.
//   - The upstream watch is opened with the CALLER's credential, so the demo's
//     central claim still holds: a participant who may not watch a resource is
//     refused by the API server, not by this code. No fallback to the
//     ServiceAccount, here or anywhere.
//
// The library holds no credential and is not an authorization boundary.
// Kubernetes is.

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
		// gateway never inspects a header for it, so an EventSource that cannot
		// send one loses nothing.
		Principal: func(r *http.Request) (gateway.Principal, error) {
			s, ok := getParticipantSession(r, cfg, sessionCookieCodec, time.Now())
			if !ok {
				return nil, errors.New("no participant session")
			}
			return &streamPrincipal{session: s}, nil
		},

		// Authorization is Kubernetes' answer, obtained with the participant's
		// OWN token. We deliberately do not use kube.SSARAuthorizer here: that
		// asks the API server a question ABOUT a user using the server's
		// credential, and needs system:auth-delegator. Opening the watch as the
		// participant gets the same verdict from the same RBAC without granting
		// this application any new power -- which is the property the whole demo
		// is built to show.
		Authorizer: participantWatchAuthorizer{},

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
	mux.HandleFunc("/public/stream", requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, _ participantSession) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.ServeHTTP(w, r)
	}))
}

// participantWatchAuthorizer permits the scope and lets Kubernetes decide.
//
// This is not AllowAll with a friendlier name. AllowAll would be a claim that
// nobody needs authorizing; this is a statement about WHERE the authorization
// happens: the backend for this principal is built from their own ID token, so
// the watch either opens as them or the API server refuses it as them. The
// gateway re-authorizes on every snapshot cycle, so a revoked grant reaches a
// stream that is already open -- by the watch failing, exactly as a revoked
// grant should.
//
// The scope allowlist above still applies; this only declines to add a second,
// weaker opinion on top of RBAC.
type participantWatchAuthorizer struct{}

func (participantWatchAuthorizer) Authorize(_ context.Context, p gateway.Principal, scope gateway.Scope) error {
	principal, ok := p.(*streamPrincipal)
	if !ok {
		return gateway.Forbidden("not authenticated")
	}
	if principal.session.IDToken == "" {
		return gateway.Forbidden("no participant credential")
	}
	log.Printf("stream: open sub=%s resource=%s ns=%s name=%s",
		principal.session.Subject, scope.Resource, scope.Namespace, scope.Name)
	return nil
}
