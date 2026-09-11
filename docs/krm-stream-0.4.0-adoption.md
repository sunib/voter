# Adopting krm-stream 0.4.0

Written **2026-09-11** against upstream `9aa0013` (`gateway/v0.4.0`, `gateway/kube/v0.4.0`,
`@configbutler/krm-stream@0.4.0`), from deployed Voter revision `4d6051b`.

0.4.0 releases the two things Voter hand-rolled in `85de0c0` while waiting for them:
bounded HTTP delivery owned by the library, and balanced stream/subscription lifecycle
observations. It also ships `gateway/kube/examples/sharedstream`, which is the same host
shape Voter built, so most of this work is deleting local versions of upstream code rather
than writing new code.

The upgrade itself is clean: nothing Voter uses was removed. `WriteSSEHeaders`, `SSARAuthorizer`
and the unscoped npm package are gone in 0.4.0; Voter uses none of them. The TypeScript package
changed only its version constant, so the frontend is a version bump with no code change.

## Phase 1 — bump and verify (no behavior change) — **DONE**

- `voter/go.mod`: `gateway` and `gateway/kube` to `v0.4.0`.
- `frontend/package.json`: `@configbutler/krm-stream` to `0.4.0`.
- Build and run the existing suites. Expect green before touching anything else, so the
  deletions below are attributable.

## Phase 2 — let the library bound HTTP delivery — **DONE**

`gateway.Options.WriteTimeout` now bounds each write-plus-flush, refuses a writer that cannot
support it before any stream opens, and reports `ObservationHTTPTransportRejected`.

- Set `WriteTimeout: 5 * time.Second` in the `gateway.Options` in
  [participant_stream.go](../voter/participant_stream.go).
- Delete `streamResponseWriter` from [stream_runtime.go](../voter/stream_runtime.go) (~26 lines).
  Its `Flush`-cancels-the-request workaround exists only because 0.3.0's sink had a void flush
  callback; 0.4.0 propagates the error and poisons the sink itself.
- Delete the manual `SetWriteDeadline` capability probe and its deferred reset in the
  `/public/stream` handler (~6 lines). The library refuses the transport now.
- Rewrite `TestStreamWriteDeadlineReleasesBlockedClient` to the upstream pattern: mount the real
  middleware chain around a probe calling `gateway.CheckHTTPStreaming(w)` on a `httptest.NewServer`.
  This is a better test than the current one — it exercises Voter's actual chain rather than a
  wrapper type in isolation. `httptest.ResponseRecorder` must not be used here.
- Delete `TestStreamFlushFailureCancelsWithoutWaitingForAnotherWrite` and `failingFlushWriter` from
  `stream_host_test.go` with the wrapper they exercise; 0.4.0's sink propagates flush errors and
  upstream `gateway/sse_test.go` covers it on real server writers.

The deleted wrapper was not merely redundant, it was weaker than what replaces it. The library clamps
each write deadline to the earlier of `WriteTimeout` and the request's own context deadline, so a
session expiring in one second no longer gets a five-second write window; heartbeats run through the
same bounded path; and a failed sink is latched so a queued heartbeat or terminal frame cannot revive
it. The wrapper did none of those three.

**Landed as three tests, not one.** The `CheckHTTPStreaming` probe proves Voter's chain still
*exposes* write deadlines, but not that Voter *uses* them. Bounded delivery is a chain with three
links, and each needs its own test because no single one covers the others:

| Link | Test | Fails as |
|---|---|---|
| Constructor sets the production default | `TestStreamRuntimeProductionDefaults` | `write timeout is 0s, want 5s` |
| The field reaches the library | `TestStreamReleasesSubscriberThatStopsReading` | subscriber never released |
| The chain exposes the capability | `TestStreamRouteChainSupportsBoundedDelivery` | `HTTP flush: feature not supported` |

`writeTimeout` is a `streamRuntime` field rather than a literal, mirroring `reauthorizationInterval`,
so the fixture can shorten it to 300ms; the stalled-peer test runs in 0.5s instead of 5.2s. That
tuning is exactly why the first test is needed — with the fixture overriding the field, a deleted
production default is invisible to every behavioral test. The same latent gap already existed for
`reauthorizationInterval`, whose 30-second default was unasserted, so that test covers both.

The stalled-peer test opens `/public/stream` on a raw socket and never reads; the fixture gained a
`padBytes` knob so events fill the socket buffer at test speed. Every one of the three was
negative-controlled by breaking its link and confirming that test — and only that test — fails.

## Phase 3 — take the balanced lifecycle observations — **DONE**

0.4.0 adds `ObservationStreamOpened`/`Closed`, `ObservationSharedSubscriptionOpened`/`Closed`
and `ObservationHTTPTransportRejected`, each guaranteed to open before it closes.

- Delete `observedBackend` and `observedWatcher` from `stream_runtime.go` (~27 lines). Their
  comment already says "replace with upstream lifecycle observations when that API is released".
  The `sync.Once` guarding double-`Stop` goes with them.
- Extend `streamMetrics.Observe` to the new kinds; `makeStreamRuntime` no longer wraps the backend.

**Decided: keep the counter, drop the gauge.** `voter_stream_upstream_watches` counted *physical*
API-server watch handles, which the `observedWatcher` wrapper bought by decrementing on `Stop()` --
correctness coupled to `sharedScope.pump`'s internal teardown. The new observations count *logical*
lifetimes; upstream states plainly that none of them measure physical watches, so they are not a
replacement for it.

`voter_stream_upstream_watch_starts_total` is, and needs no coupling: a monotonic counter incremented
only where a watch is opened, with no `Stop()` and no `sync.Once`. Staying at 1 while subscribers
climb to 200 is the production evidence that one watch serves every viewer. `observedWatcher` is
deleted and `observedBackend` keeps only that increment.

Exported now: `voter_stream_subscribers` (host: HTTP occupancy, counted from handler entry so it
includes identity resolution), `voter_stream_logical_streams` and `voter_stream_shared_subscriptions`
(library observations), `voter_stream_upstream_watch_starts_total`, `voter_stream_transport_rejected_total`,
plus the existing resync/overflow/review-failure counters. The gap between `subscribers` and
`logical_streams` is the requests currently resolving identity -- where a 200-viewer opening burst
shows first.

[docs/shared-streams.md](shared-streams.md) has the updated metrics table. The `metrics.watches`
assertions moved to `watchStarts` plus the balanced logical gauges. One trap: `stream_rehearsal_test.go`
reads metrics by *string* name, so its three references to the removed gauge compiled and vetted
cleanly and would only have failed when the 200-attendee rehearsal was actually run. They are fixed.

## Phase 4 — flatten the principal — **DONE**

Upstream's example makes `kube.Subject` itself the principal, resolved once in `Principal`.
Voter instead carries a `streamPrincipal` holding the session, resolves the subject lazily under a
mutex from inside the `Authorizer`, and type-asserts it back out in two places.

Voter's ordering exists for a reason worth keeping: `TestStreamPinsNamespaceNameAndVersionBeforeOpeningBackend`
proves a bogus scope costs no SelfSubjectReview. Upstream's `Principal` runs before scope validation,
so copying it verbatim would let a session burn an SSR per bad-scope request.

**Voter drives `Gateway` directly.** `Handler` is `Principal` -> `ScopeFromQuery` -> `Scopes.Validate`
-> `ServeStreamProjection` in a fixed order, and that order is the whole problem: identity is resolved
in `Principal`, before the scope has been validated, so a request naming a resource this endpoint
never serves still costs a SelfSubjectReview. Moving the scope check up into `Principal` fixes the
wasted call but breaks something else -- `Handler` deliberately masks `Principal` errors as
`FORBIDDEN "not authenticated"`, so an unallowlisted resource stops reporting `SCOPE_INVALID`.
`TestStreamRefusesAnUnallowlistedResource` catches exactly this.

Both properties survive if Voter owns the sequence and lets the library frame the answer:

```go
scope, err := gateway.ScopeFromQuery(r.URL.Query())  // 1. parse
if err == nil { err = scopePolicy.Validate(scope) }  // 2. which KIND may stream at all
if err == nil { err = pinned.check(scope) }          // 3. WHICH ONE
var principal gateway.Principal = refusal{err}       // carries a decided refusal
if err == nil { principal = resolveParticipantSubject(...) }  // 4. only now, one SSR
g.ServeStreamProjection(w, r, principal, scope, gateway.ProjectionFull)
```

`refusal` is a five-line principal that `Auth` recognizes and returns verbatim. Because
`ScopePolicy.Validate` returns a `*StreamError` and the gateway classifies terminal errors with
`errors.As`, the code survives the trip: an unallowlisted resource still reads `SCOPE_INVALID`. That
matters because 0.4.0 exports no way to write SSE headers by hand -- `WriteSSEHeaders` was removed
precisely because `ServeStream` owns them -- so hand-rolling a refusal would mean re-duplicating the
framing the release just consolidated. Routing refusals back through the gateway avoids that.

Taking `Gateway` directly deletes:

- the `streamPrincipal` type and its `subjectMu` (~10 lines),
- `resolveSubject`'s lazy-init dance, replaced by upstream's `subject.go` shape (~12 lines net),
- `kubernetesSubject` (~8 lines), and its extractor in `makeStreamRuntime` collapses to one assert,
- the `p.(*streamPrincipal)` assertion in `participantWatchAuthorizer`, which becomes a scope
  comparison with no principal in it at all,
- the `Principal` and `Clients` callbacks entirely, and the `Authorizer` closure that currently exists
  only to sequence work that becomes three consecutive statements.

The `reviewFailures` counter has to keep working across the move; count the `Principal` failure and
the `sar.Authorize` failure separately rather than folding them, since they are different outages.

## Phase 5 — optional, participant client config

`rest.AnonymousClientConfig(cluster)` does exactly what [participant_kube.go](../voter/participant_kube.go)
builds by hand, and its contract — strips credential-bearing transports and impersonation — is the
same one Voter's comment claims. Adopting it would delete `config.participantTLS`, the
`KUBERNETES_API_SERVER`-must-match-`STREAM_KUBECONFIG` check, and the `kubeCAPath` `os.Stat` fallback.

This is deliberately last and separable: `participantClientsFor` also serves quiz, storefront and
coffee handlers, so it is a wider blast radius than the stream work and should not ride along with it.

## Expected result

Roughly 100–130 lines out of the ~340 in `stream_runtime.go` and `participant_stream.go`, with
Phase 5 adding ~30 more. Phases 1–2 landed 37 of those (27 from `stream_runtime.go`, 10 from
`participant_stream.go`), leaving 313 lines across the pair. Every deletion replaces Voter code with a supported upstream contract:
bounded delivery, balanced lifetimes, and a resolved subject that cannot drift from its session.

## Not in scope

- `gateway/kube/examples/conditionalsave` remains a near-match for
  [participant_coffee.go](../voter/participant_coffee.go), but Voter's version adds the CommitRequest
  pairing and partial-success reporting, which the example has no equivalent for. One worthwhile
  detail to steal separately: the example's GET returns `redactedPaths` alongside the object, where
  Voter discards them with `projected, _ := gateway.Project(...)`.
- Re-running the 200-attendee rehearsal. The bounded-delivery and observation changes alter what the
  gauges mean, so the recorded run in `docs/shared-streams.md` should be re-read before it is cited
  against the new metric names, but no new capacity claim is being made here.
