# Request: reduce shared-stream host code in krm-stream

**To:** krm-stream maintainers  
**From:** the Voter integration  
**Date:** 2026-09-11  
**Baseline:** published gateway, kube adapter and browser packages at **0.3.0**  
**Status:** consumer request for discussion; proposed API names below are illustrative.

## What we are asking for

We have integrated krm-stream's shared backend into Voter and verified the integration
in a disposable Kubernetes fixture with 200 independently authenticated identities.
The existing library owns the difficult parts: fan-out, cached snapshots, bounded
queues, recovery, subscriber authorization checks and browser reconciliation.

The integration still needed application-owned HTTP deadline handling, wrappers for
watch metrics, and Kubernetes identity-resolution glue. Several of those responsibilities
appear useful to any shared-stream host.

We would like to contribute consumer evidence and tests toward four focused improvements:

1. **Bounded SSE writes and flushes in the gateway's HTTP transport.**
2. **Complete lifecycle observations for requests, subscriptions, upstream watches and
   authorization checks, with documented counting semantics.**
3. **A small kube-adapter helper for resolving a full Kubernetes subject through
   SelfSubjectReview.**
4. **A tested shared-stream host example and capacity guidance**, including authorization
   bursts and the distinction between application deadlines and library timeouts.

The first two are our highest priorities. They would let us delete generic transport
and instrumentation wrappers from Voter. We are not requesting another stream engine,
reconciliation framework, authentication service, or a Voter-specific integration package.

## Consumer context and boundaries

Voter serves a Vue frontend and a Go backend. Participants sign in through Dex and
Room Pass. Voter holds their token in an encrypted, HttpOnly application cookie.
The demo includes collaborative editing of one namespaced CoffeeConfig resource.

Our shared-stream integration has the following shape:

- One long-lived `gateway.SharedBackend` per Voter process, wrapping a service-account
  `kube.Backend`; every same-scope subscription receives that same backend instance.
- A server-controlled allowlist for one group/version/namespace/name.
- SelfSubjectReview using the participant's token at subscription opening. We retain
  the API server's username, groups, UID and extras for that subscription.
- `kube.SubjectAccessReviewAuthorizer` using the service-account client before cached
  data is disclosed, checking both list and watch.
- `ReauthorizationInterval = 30s` and `ReauthorizationTimeout = 5s`.
- A host deadline at the earlier application-session or token expiry.
- Service-account permissions limited to named CoffeeConfig list/watch and creating
  SubjectAccessReviews. REST GET, PATCH and CommitRequest creation continue to use
  participant credentials.

The captured Kubernetes subject is distinct from the application's display identity.
We do not guess username prefixes, trust browser identity headers, or substitute OIDC
claim groups for the API server's resolved groups. Existing cookies remain compatible
because we resolve the full identity when a stream opens instead of storing more
identity fields in the cookie.

These application choices should remain explicit. The library should not choose a
credential source, cluster destination, session policy, resource allowlist, service
account, or RBAC grants for a host.

## Evidence from the integration

The initial shared-stream increment added approximately **180 net lines of application runtime code**.
It also adds substantial tests and fixture documentation. This was a capacity and
authorization increment, not a net code-reduction refactor. The main candidates for
upstream adoption are the transport and instrumentation portions of that runtime code.

A short rehearsal opened 200 streams concurrently through Traefik, the real Voter
HTTP gateway and a real Kubernetes API. Voter was limited to 1 CPU and 256 MiB memory.

| Observation | Result |
| --- | --- |
| All 200 subscriptions reached synced state | Approximately 132 ms |
| Actual Kubernetes CoffeeConfig watch increase | Exactly 1 |
| All 200 clients observed one update | Approximately 13 ms from PATCH dispatch |
| Aggregate SSE bytes for initial snapshot plus update | 378,200 bytes |
| 50-client reconnect burst | Approximately 36 ms; no new upstream watch |
| One grant withdrawn while 200 clients were connected | Affected stream closed in approximately 30 seconds; 199 remained connected |
| Final disconnect | Both host watch count and actual API watch count returned to baseline |
| One metrics-server sample | 0.02 millicores and 34.2 MiB, sampled while idle; not a peak measurement |

The harness uses 200 distinct Kubernetes service-account tokens in sessions signed
with the fixture's known application keys. It tests the authenticated streaming
boundary, not 200 Dex logins or 200 browser renderers. A separate 13-test Chromium
suite exercises the actual Room Pass/Dex/Voter login and editing journey, including
withdrawal of one viewer's RBAC grant while another continues receiving updates.

The fixture uses k3s 1.31.5 and exercises the adapter's list-then-watch compatibility
path. These observations are local evidence, not production capacity guarantees,
latency percentiles, or a statement of upstream Kubernetes version support. The
consumer implementation has not yet been published or promoted to production.

## Request 1: transport-owned write and flush deadlines

### Problem

In the reviewed implementation, `SSESink` serializes writes and flushes, while the
reauthorization delivery gate also serializes delivery with authorization checks.
A blocked downstream write can therefore delay teardown or the point at which a
reauthorization check begins. The configured reauthorization timeout does not, by
itself, bound time spent waiting to acquire the delivery gate.

Voter currently wraps `http.ResponseWriter` to set a five-second deadline before
writes and flushes. It also verifies deadline support before opening the stream and
clears the connection write deadline when the handler exits. A review follow-up
also cancels the request on flush errors, since 0.3.0's void flush callback cannot
propagate those errors through `Emit`. This temporary mitigation belongs close to
the gateway's SSE transport, which already owns framing, flushing and heartbeats.

### Proposed behavior

Please consider a configurable HTTP transport write timeout, for example
`Options.WriteTimeout`, with the final name and default chosen by maintainers.

The contract should make the following points explicit:

- Bound writes of resource frames, terminal frames, initial header flushing and
  heartbeat comments. A terminal error cannot be assumed deliverable to a blocked peer.
- Treat flushing as part of downstream I/O. Propagate flush failures where the HTTP
  transport permits it; do not silently claim successful delivery after a failed flush.
- Define whether the deadline applies to a complete frame-plus-flush operation or to
  each operation separately. This affects the host's end-to-end revocation budget.
- Make cancellation and timeout release the affected subscriber and its resources.
  Other subscribers must continue, and the last subscriber must release the shared watch.
- Define behavior for response-writer wrappers that cannot support deadlines. If a
  host requests a bound, silently continuing with unbounded writes would violate it.
- Document support across the HTTP protocols and middleware combinations the package
  actually tests, including the unwrapping behavior of `http.ResponseController`.
- Avoid implementing timeout behavior by abandoning a goroutine that remains stuck
  in a write. The operation and its owned resources must terminate.
- Avoid leaving an expired connection deadline behind when a handler returns.

A whole-response `http.Server.WriteTimeout` is not an equivalent substitute: the
stream is intentionally long-lived. A healthy stream must survive many timeout periods
while each individual downstream operation remains bounded.

### Acceptance tests

1. A real socket client stops reading; a sufficiently large pending frame fails within
   the configured bound and the handler releases its subscription.
2. A healthy stream remains open and usable longer than the configured write timeout.
3. Heartbeat and flush failures terminate cleanly without leaking their goroutines.
4. Expiry and authorization withdrawal during blocked delivery have measured teardown
   bounds; another subscriber continues receiving updates.
5. Final-subscriber cleanup cancels the shared upstream, including the blocked-client case.
6. Unsupported deadline transports follow the documented behavior.

Our current socket test and HTTP-level expiry/isolation tests can serve as starting
points. They are consumer evidence, not a complete replacement for transport tests.

## Request 2: lifecycle observations with precise ownership

### Problem

The existing `Observer` reports useful stream/cycle events, resynchronization,
projection suppression, shared overflow and terminal errors. We still needed to
wrap `Backend.Watch` and `Watcher.Stop`, and separately count HTTP handler entry/exit,
to expose operational gauges.

Those wrappers exist only to observe lifecycle transitions already owned by the
library. Different hosts could easily count different things while giving their
metrics the same names.

### Proposed behavior

Please extend the observer contract, or provide an equivalent small statistics API,
with enough information to report the following independently:

| Quantity | Required distinction |
| --- | --- |
| Active HTTP stream requests | Includes requests still resolving identity or authorizing; not synonymous with authorized subscribers |
| Active shared subscriptions | A subscription attached to a shared scope; distinguish it from a request or a snapshot cycle |
| Active upstream watch handles | Backend-owned watch lifetime; distinguish from downstream subscriptions and physical HTTP requests inside an adapter |
| Upstream openings and closures | Includes recycling/recovery; a late cached subscriber must not look like a new upstream watch |
| Authorization check attempts/outcomes | Distinguish initial/cycle checks from periodic checks and allow/deny/error/timeout |
| Consumer resynchronization and overflow | Retain the existing signals and document their relationship to new lifecycle events |

Exact event names are less important than the semantics:

- Every successfully opened lifetime has exactly one matching close observation,
  including EOF, cancellation, authorization failure and recovery paths.
- Failed openings do not decrement a gauge that was never incremented.
- Repeated `Stop` calls cannot double-count closure.
- Events make snapshot cycles, browser reconnects and upstream recycling distinguishable.
- Callbacks remain prompt and safe under concurrent subscribers. Document ordering
  guarantees and whether observers may be called concurrently.
- A lost observation would make an open-minus-close gauge inaccurate. Define whether
  delivery is synchronous/reliable within the process and what hosts must guarantee.
- Outcomes use bounded classifications. Do not introduce credentials, principals,
  object bodies or raw error strings into lifecycle telemetry. Scope data already
  present in observations must not be promoted blindly to metric labels.

Voter can keep its metric names and separate internal Prometheus text listener. We are not asking the
core gateway to require a particular metrics backend or host an HTTP metrics route.
Kubernetes identity-resolution metrics may remain with the optional kube helper or
host because that operation occurs outside the generic gateway's authorization call.

### Acceptance tests

Use concurrent subscriptions, warm-cache joins, denied openings, periodic revocation,
upstream closure, overflow recovery and repeated cleanup calls. Assert balanced
lifetimes and correct distinctions between subscriber and upstream counts. Run under
the race detector. Include an example showing low-cardinality metric mapping.

Host counters should not be advertised as proof of physical API-server watch counts.
Our rehearsal checks Kubernetes metrics independently; that distinction should remain
visible in documentation.

## Request 3: full-subject SelfSubjectReview helper

### Problem

`kube.SubjectAccessReviewAuthorizer` correctly accepts a host-provided `SubjectFor`.
The host still needs to obtain that subject safely. In Voter, login previously retained
only a Kubernetes username for display. Shared cached disclosure requires the full
identity used by Kubernetes authorization, including groups and applicable UID/extras.

Resolving and translating this identity is Kubernetes-specific, reusable work.

### Proposed behavior

Consider a helper in the **kube adapter**, conceptually:

```go
// Illustrative signature, not a proposed stable name.
func ResolveSubject(ctx context.Context, participantClient kubernetes.Interface) (Subject, error)
```

The helper would issue SelfSubjectReview using the supplied client and copy the
returned username, groups, UID and extras into the existing `kube.Subject` shape.
It should honor cancellation and reject an unusable identity rather than invent one.

The supplied client must authenticate as the participant. Calling it with the
service-account client would resolve the service account instead; documentation and
examples should make this distinction prominent. The signature itself cannot prove
that the caller supplied the intended credential.

Please avoid having this helper read tokens from HTTP headers/cookies, load a default
kubeconfig, guess OIDC mappings, perform impersonation or cache identities globally.
The host decides when to resolve, which client to use, how long a resolved identity
is valid and when to end the subscription. A resolved subject is not a refreshed token
or an authorization grant. The helper should not silently perform another review on
every event or periodic SAR check.

### Acceptance tests

Preserve username, groups, UID and multi-valued extras exactly, with independent
returned data where ownership requires it. Cover API failures, cancellation and the
chosen empty-identity behavior. Keep a consumer-boundary test proving browser headers
cannot choose the participant client or override the resolved subject.

## Request 4: tested composition and burst-capacity guidance

The existing shared-backend pattern is a good foundation. We would like a complete,
small example combining shared watches, explicit identity resolution, per-subscriber
SARs, session deadlines, bounded HTTP delivery and lifecycle observations.

The example should visibly construct the shared backend once, use the same configured
cluster for identity resolution/access reviews/data, and preserve personal credentials
for direct writes. Any session mechanism can be minimal and illustrative; it should
not create a dependency on Room Pass, Dex or Voter.

### A failure worth documenting

Our first client configuration allowed 50 QPS with a burst of 100. It passed a run
that opened streams in smaller batches. Opening all 200 simultaneously exposed
client-side throttling: the cohort needs 400 list/watch SARs, and queued reviews could
outlive the five-second authorization deadline.

We changed the service-account client configuration to 100 QPS with a burst of 400.
The simultaneous-opening test then passed. These are consumer settings for a declared
200-subscriber workload, not proposed universal library defaults.

Capacity guidance should distinguish:

- Approximately `2 × subscribers / recheck interval` SAR requests per second at
  steady state: about 13.3 requests/second for 200 subscribers and a 30-second interval.
- Opening and snapshot-cycle bursts, plus the separate SelfSubjectReview cost when
  the host resolves identities at opening.
- Client-side throttling, API-server limits and actual authorization latency.
- Reauthorization timer cadence, waiting for an in-flight delivery, check execution,
  terminal delivery attempts and resource cleanup. A 30-second interval plus a
  five-second callback timeout does not alone prove a 35-second termination bound.
- Upstream watch reduction versus per-browser snapshot bytes, rendering and connection
  costs. Shared watches do not make 200 viewers equivalent to one viewer in every respect.

### Test ownership

Generic transport, lifecycle, sharing, authorization and identity-translation tests
belong upstream. Voter should retain a smaller boundary suite proving its scope,
credential source, session expiry, UI draft preservation and fixture wiring.

Our 200-identity harness should be treated as source material for an upstream
integration/load scenario. Its cookie signing, fixed fixture paths, RBAC setup and
CoffeeConfig-specific assertions should not be copied into a general runtime package.
The full Voter browser suite should not become an upstream dependency.

## Delivery, compatibility and consumer adoption

We suggest delivering this in independently reviewable increments:

1. HTTP write/flush bounds and their transport tests.
2. Lifecycle observation semantics and tests.
3. Optional kube identity helper, followed by the composed example and capacity notes.

Please decide and document zero-value/default behavior for any new option. An opt-in
bound is a straightforward compatibility path, while enabling a finite default would
be a behavior change worth release notes. Observer additions also need clear guidance
for consumers that ignore unfamiliar event kinds.

No wire-protocol, browser store, merge policy or conditional-save API change appears
necessary for these requests. There is no dependency here on replay, SSA, keyed-list
editing or upstream watch continuation work.

Once equivalent public behavior is released and verified, Voter can pin the release,
replace its writer and watch-counting wrappers, adopt the identity helper if provided,
and rerun its boundary/browser/rehearsal checks. We would retain the current local
integration until then; this request is not a reason to introduce a lasting module
replacement or copy library core code into the application.

## Maintainer decisions requested

- Does bounded HTTP delivery belong in the gateway's existing SSE transport, and
  what timeout/default/unsupported-transport contract should it expose?
- Should lifecycle accounting extend `Observer` or use a separate statistics interface?
- Is full-subject resolution a useful public kube helper or better kept in the example?
- Which of these increments would the team accept contributions for, and which test
  and release expectations should the consumer follow?

## Consumer source material

The implementation and evidence are in the Voter working tree; these changes are
not yet a published release. Paths are listed so the relevant patches can accompany
this request without implying that unpublished code is available at a remote URL:

- `voter/stream_runtime.go`: backend construction, lifecycle wrappers, metrics and
  bounded response writer.
- `voter/participant_stream.go`: scope, identity resolution, SAR wiring and deadlines.
- `voter/participant_stream_test.go`: HTTP-level authorization, expiry/isolation and
  blocked-writer tests.
- `voter/stream_rehearsal_test.go`: opt-in 200-identity fixture rehearsal.
- `room-pass/test/browser/live-stream.spec.js`: browser behavior during real RBAC withdrawal.
- `docs/shared-streams.md`: commands, measurements and limits of the local evidence.

The requested outcome is a smaller, clearer host integration with reusable transport
and observation behavior owned by krm-stream, while each application continues to
own its credentials, session policy and authorization scope.
