# Shared CoffeeConfig streams

Status: deployed to production on 2026-09-11 as `85de0c0`, promoted through GitOps
`2d770a1`. Verified first in the disposable fixture, then on the cluster: the narrowed
service-account grants authorize named list/watch on Kubernetes 1.36.1, public
`/metrics` returns 404, and the ready pod matches the published digest. An
authenticated 200-attendee production rehearsal has not been run.

## Implementation

Voter constructs one `gateway.SharedBackend` at process startup over a Kubernetes
service-account backend. Every subscriber uses that same instance. Scope remains
fixed to the configured CoffeeConfig group/version/namespace/name. Both service-account
and participant clients use the same configured API destination.

Before any cache disclosure, Voter resolves the participant token using Kubernetes
SelfSubjectReview. It retains the returned username, groups, UID and extras for that
subscription; it does not use browser headers or the display identity in the cookie
for authorization. `kube.SubjectAccessReviewAuthorizer` checks both list and watch.
Resolution and initial checks have a five-second deadline. Library reauthorization
runs every 30 seconds with a five-second deadline, independently per subscriber.

The earlier session/token expiry bounds each subscription. SAR rechecks detect
RBAC changes for the captured subject; they do not refresh IdP group membership or
issued token claims. See [revocation timing](authorization.md#revocation-timing-and-the-cached-subject). Disconnecting one viewer
leaves the shared watch running for the others; the last departure releases it.
Bounded HTTP delivery is the library's since krm-stream 0.4.0: `WriteTimeout` deadlines
each write plus flush, refuses a transport that cannot support that before a stream
opens, and poisons the sink on failure so a queued heartbeat cannot revive it. A
participant that stops reading is released within five seconds. Library bounded queues,
fan-out, cached snapshots and overflow recovery are reused without a Voter event queue.

Service-account RBAC is limited to list/watch of `demo-coffee` and creating
SubjectAccessReviews. No impersonation or CoffeeConfig writes are granted. Direct
REST reads, PATCH and CommitRequest creation still carry the participant's token.
Consequently, watch audit events name the service account and write audit events name
the participant. Existing encrypted session cookies remain compatible.

## Metrics

`GET /metrics` on a **separate listener** returns Prometheus text without identity
or resource labels. The application listener returns 404 for this path. The default
metrics address is loopback `127.0.0.1:9090`; fixture and platform manifests bind
`0.0.0.0:9090` on the pod network, without adding that port to the public Service
or ingress. This internal endpoint is unauthenticated; no scraper is configured.
Tests query it via the authenticated Kubernetes pod proxy.

| Metric | Meaning |
| --- | --- |
| `voter_stream_subscribers` | Active SSE requests, including requests still authorizing |
| `voter_stream_upstream_watches` | Active upstream backend watch handles |
| `voter_stream_upstream_watch_starts_total` | Upstream backend openings, including recycling |
| `voter_stream_resyncs_total` | Library consumer resynchronizations |
| `voter_stream_overflows_total` | Library shared-subscriber queue overflows |
| `voter_stream_access_review_failures_total` | Failed identity resolution or authorization checks, including denials/timeouts |

Backend watch handles are not a substitute for API-server evidence. The rehearsal
also checks the change in Kubernetes `apiserver_longrunning_requests` for CoffeeConfigs.
A browser reconnect normally gets a new snapshot without opening another upstream
watch. At 200 subscribers the periodic checks budget about 13.3 SARs/second, plus
opening and snapshot-cycle bursts; the service-account client allows 100 QPS/400 burst.
The burst covers the 400 reviews needed for 200 simultaneous openings within the
five-second deadline. The opt-in test opens the full cohort concurrently.

## Local verification

Final checks passed: Go race tests, vet/lint, 21 frontend tests, frontend
type-check/build/lint, container build and platform Kustomize rendering. All 13
browser tests passed in 41.9 seconds against the rebuilt fixture. The simultaneous
200-identity rehearsal passed in 45.0 seconds.

Go tests cover exact scope rejection before identity/API access, full identity
forwarding, ignored forged headers, denial of list and watch separately against a
warm cache, review errors/timeouts, independent session expiry under continuing
updates, revocation and final cleanup. A socket test stops reading entirely and
verifies that the blocked writer is released by its deadline.

The browser suite covers real Dex/Room Pass login, conditional saves and conflicts,
plus real RBAC withdrawal from one of two viewers, denied warm-cache reconnection,
preserved unsaved input and continued updates for the remaining viewer.

The opt-in load test is restricted to `room-pass/.local/kubeconfig` and requires its
context to be `k3d-room-pass-e2e`. Run it separately from the browser suites:

```bash
task room-pass:e2e-up
task test-browser
task voter:test-stream-rehearsal
```

The harness creates 200 independently authenticated Kubernetes service-account
identities and puts their real tokens into sessions signed with the fixture's known
cookie keys. It uses verified fixture TLS through Traefik to the actual Voter gateway
and Kubernetes API. It creates/deletes only its unique namespace and RoleBinding,
and restores the fixture CoffeeConfig field it edits. Tokens/cookies are not logged.
This exercises the authenticated streaming boundary; it does not load-test Dex or
simulate 200 browser renderers. The separate browser suite covers the OIDC/UI journey.

Final measured run with all 200 opening simultaneously, under the fixture's 1 CPU / 256 MiB Voter limits:

| Observation | Result |
| --- | --- |
| 200 sessions reached synced state | 132 ms |
| Actual API-server watch increase | 1 |
| All 200 observed one update | 13.0 ms from PATCH dispatch |
| Snapshot plus update SSE payload across 200 viewers | 378,200 bytes |
| 50 reconnects | 36 ms; no new upstream watch |
| One grant withdrawn among 200 | Closed in 30.00 seconds; 199 remained connected |
| Last client disconnected | Backend and actual API-server watch returned to baseline |
| One metrics-server resource sample | 0.02 millicores, 34.2 MiB; sampled during the idle withdrawal wait, so not representative of load |

These are observations from a short local run, not latency percentiles or production
capacity guarantees. The fixture uses k3s 1.31.5 and exercises the library's
list-then-watch compatibility path. A final production-equivalent rehearsal still
needs longer sampling, access-review traffic/error measurement, slow-client/overflow
load, upstream recycling and attendee OIDC/browser behavior under event conditions.

## Release handoff

The fixture has its own `voter` service account and matching narrow grants. The same
grants were promoted to the platform repository (a separate Git checkout) in commit
`2d770a1`, together with the image pin and `METRICS_ADDRESS`. That change also removed
the old unused `get` verb and QuizSession/QuizSubmission service-account read grants,
which no code path used.

Verified after promotion: Flux applied `2d770a1`, the ready pod matches the published
digest, `/public/build-info` reports `85de0c0`, and on Kubernetes **1.36.1** the
service account is allowed named `coffeeconfigs/demo-coffee` list and watch while
unnamed list, `get`, `patch`, quiz resources, Secrets and impersonation are all denied.
That closes the named-authorization gate on the production API version.

Still open: **no attendee has opened a stream against the deployed build**, so every
production stream counter is zero and the authenticated journey, actual shared-watch
count under load, and the `Recreate` rollout/reconnect cycle remain unverified in
production. Keep the production capacity gate in PLAN open until that evidence exists.
Room Pass extraction, k8s-front prototyping, CI artifact promotion and ConfigButler
commit observation remain separate changes.

The [upstream request](krm-stream-shared-host-request.md) proposes moving generic
HTTP deadline handling, lifecycle observations and identity-resolution helpers into
krm-stream. It is a discussion document, not a new release dependency.

## Review follow-up and pending upstream release

Host fixes include private metrics routing, explicit local kubeconfig support with
participant credential isolation, synchronized subject initialization, and a single
startup wiring assertion instead of runtime nil/authentication fallbacks. The 0.3.0
module's `sharedScope.pump` explicitly defers `watcher.Stop()` on every exit; the
counter wrapper is used only below that shared backend. Full lifecycle observations
and transport error propagation remain pending upstream improvements. No unreleased
APIs or module overrides are used.

Both the platform and fixture Deployment manifests already specify 1 CPU / 256 MiB
limits. Production-version verification of named list/watch authorization remains a
promotion gate. The single-replica `Recreate` rollout drops all open streams; the
concurrent-opening rehearsal does not prove a full deployment/reconnect cycle.

Review disposition:

| Feedback | Disposition |
| --- | --- |
| Public metrics | Separate listener; public `/metrics` returns 404. Fixture tests use the authenticated Kubernetes pod proxy. |
| Broken local development | Explicit `STREAM_KUBECONFIG`, shared cluster trust, and tests that participant clients never inherit shared credentials. |
| Repeated nil guards | Replaced with one registration-time wiring assertion. |
| Watch counter cleanup | Verified the actual pinned 0.3.0 module's deferred `Stop`; retain this scoped wrapper until upstream observations ship. |
| Unchecked principal assertion | Checked directly at the point of use. |
| Subject synchronization | Initialization and reads now use a host-owned mutex; concurrent resolution is tested. |
| Discarded flush failures | Resolved upstream in 0.4.0: `WriteTimeout` owns the deadline and the sink propagates flush errors. The Voter response-writer wrapper is deleted. |
| Named list/watch on production version | Still a promotion gate; fixture verification is not substituted for it. |
| Resource limits and rollout | Both manifests already have matching limits, including platform HEAD before this change. Recreate/reconnect testing remains a production gate. |
| Orphaned UI files | Removed both screens and their unused API, EventSource, type and formatting helpers; retain redirects for old URLs. |
| Mixed commit scope | Landed as two commits: UI housekeeping first, then the shared-stream host and RBAC work. |
| IdP versus RBAC revocation | Documented the distinct bounds in authorization and architecture docs. |
