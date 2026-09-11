# Architecture

Voter is a coffee and voting demo consuming public infrastructure components. Room Pass
is an independent enrollment service being prepared for extraction. krm-stream owns
generic live-resource behavior. The **krm-stream 0.3.0 integration with shared streams
is deployed as `85de0c0`**, verified on **2026-09-11**. Remaining targets are tracked
in [PLAN.md](PLAN.md).

## Ownership

| Component | Owns | Boundary |
| --- | --- | --- |
| Voter Vue app | Coffee/voting screens, cart, field presentation, save intent | Uses library resource state; no custom reconciliation |
| Voter Go backend | Application OIDC session/CSRF, fixed resource scope, participant-token requests, coffee pricing/orders, QuizSubmission validation/aggregation, commit-request orchestration | No fallback participant writes as a ServiceAccount; no generic stream engine |
| krm-stream | Kubernetes watch-to-SSE protocol, shared-watch cache/fan-out, projections, snapshots, reconciliation, draft/conflicts, patch generation, generic recovery and optional framework adapters | No coffee semantics, application credentials, login UI or Git commit workflow |
| Room Pass | Room lifecycle, rotating codes, browser enrollment, stable participant identity, bound handoff to Dex | No application grants or token signing; no Voter dependency |
| Dex | OAuth 2.0/OIDC authorization server and OpenID Provider, connectors, codes and signed tokens | Trusts Room Pass assertions only through a protected authproxy integration |
| Kubernetes | Resource storage, token authentication, RBAC, admission and audit | Identity does not itself grant permission |
| ConfigButler | Persisting accepted configuration changes to Git, commit status/history | Request acceptance is distinct from an observed commit |
| Platform GitOps repository | Installed versions, routes, CRDs, RBAC, issuer configuration and deployment | Live application changes arrive through Flux |

0.3.0 exports managed recovery and save-capture/reconciliation primitives. The Vue
binding and conditional-save controller are tested, copyable examples, not package
exports. Voter may own thin binding/request glue while the core owns reconciliation.
Proposal 0005 is discussion; 0006 sequences follow-ups. Neither makes upstream watch
continuation, replay, version notifications or a generic writer released capabilities.

## Identity flow and Room Pass's product boundary

```mermaid
sequenceDiagram
    participant B as Browser
    participant V as Voter OIDC client
    participant D as Dex
    participant R as Room Pass
    participant K as Kubernetes
    B->>V: Sign in
    V-->>B: Dex authorization request with PKCE
    B->>D: Authorization request
    D-->>B: Room Pass connector callback
    B->>R: Browser-bound room enrollment/handoff
    R->>K: Read Room and Participant; enroll if eligible
    R->>D: Trusted identity assertion on connector callback
    D-->>B: Authorization code for Voter
    B->>V: OIDC callback
    V->>D: Code exchange and token validation
    V->>K: SelfSubjectReview with participant token
    V-->>B: Encrypted HttpOnly application session cookie
    B->>V: REST read or mutation with session cookie
    V->>K: Request with participant's own ID token
```

Room Pass is currently an **authenticating proxy for Dex**, not an OAuth authorization
server or standalone OpenID Provider. Dex's authproxy connector consumes a trusted
upstream identity; Dex handles the application's OAuth/OIDC exchange. See
[Dex authproxy](https://dexidp.io/docs/connectors/authproxy/) and the distinction between
OAuth authorization and OIDC authentication in the
[OpenID Foundation introduction](https://openid.net/developers/how-connect-works/).

Keep the product name **Room Pass** with a description such as “room-code sign-in for
applications, powered by Dex.” The combination supports ordinary OIDC applications;
those applications need no CoffeeConfig or Kubernetes client. Room Pass itself still
uses Kubernetes storage. Naming it `room-oauth` would imply protocol responsibilities
it currently delegates to Dex. Native OIDC issuance would require a separate design
and a mature provider implementation; it is not part of extraction.

Room Pass already has a separate Go module, Docker build, controller, CRDs and tests.
Remaining coupling includes the module's Voter repository path, parent CI/tasks, the
new Voter browser fixture, and demo-specific group/email assumptions. Extraction
preserves the API group, object UIDs, cookie keys, connector ID and subject mapping;
a new repository must not unexpectedly create new identities.

## Trust boundaries

Voter owns `/auth/login`, `/auth/callback`, `/auth/session` and `/auth/logout`.
It uses established OIDC libraries. The signed/encrypted, Secure HttpOnly cookie
contains the ID token; JavaScript receives identity metadata and a CSRF token only.
Persistent keys preserve established sessions across restarts; pending logins are
process-local. Cookie custody does not make XSS harmless: same-origin script can
still perform actions as the user even though it cannot read the token.

Direct REST reads and writes use the session's token against a fixed API server with
TLS verification. The new shared watch uses a narrowly authorized service account;
`kube.SubjectAccessReviewAuthorizer` asks Kubernetes whether each subscriber may list and watch the
fixed scope before serving cached data. Its identity mapping uses the authenticated
session token's Kubernetes SelfSubjectReview result at stream opening, including UID/extras, not
browser headers or guessed prefixes. The host must enforce that verdict before disclosure.
The service account needs read and SubjectAccessReview creation permissions, not
impersonation or application write grants. Shared watch audit events identify the
service account; CoffeeConfig writes and CommitRequests retain participant attribution.
The stream policy fixes namespace and name as well as the resource type.

The deployed issuer combines `github`, `room-pass` and `linkedin`. Platform
containment derives usernames from Dex's `federated_claims.connector_id` and restricts
room identities to demo groups. Display names and synthetic email are attribution,
not verified personal identity. Voter obtains the Kubernetes username through
SelfSubjectReview. See [authorization.md](docs/authorization.md) for intended grants;
additive platform bindings, including `system:authenticated`, still need auditing.

Room Pass strips untrusted identity headers before proxying, accepts only configured
return URLs and uses a browser-bound single-use handoff. Every callback alias must
remain protected, with no bypass route to the authproxy assertion endpoint. The
standalone fixture routes issuer traffic through Room Pass; the shared platform also
routes other connectors through Traefik. Preserve each topology's header sanitization,
callback routing and network isolation instead of treating their manifests as interchangeable.
See [handoff.md](room-pass/docs/handoff.md) for the protocol and
[csp-form-action.md](room-pass/docs/csp-form-action.md) for browser redirect constraints.

A participant may also arrive by scanning the presenter's QR code, which encodes
`/auth/login?code=<current>&return=<path>`. The destination rides in the Voter login
transaction, server-side, as any `return` does. The room code uses a **Room Pass
feature**, not a Voter one: an application sharing the join host may pre-supply a code
in the plain `__Host-room-pass-joincode` cookie, and the join page then asks only for a
display name. Room Pass owns that cookie's name, accepted values and attributes; Voter is
one consumer, and nothing in the channel knows what a QR code is. The cookie exists
because the join URL is built by Room Pass after Dex from a handoff id Voter never sees,
and it is delivered only because the join origin and the application origin are one host.
It is untrusted by construction: it becomes a prefill and is checked against the Room's
valid codes through the ordinary POST, so a forged one is a wrong code and a stale one is
an expired code. Voter vouches for nothing it carries, holds none of Room Pass's keys,
and never signs it. Room Pass keeps the typed path for anyone who cannot scan. The
contract is in [qr-join.md](room-pass/docs/qr-join.md).

Application mutations require CSRF proof. Logout clears the Voter session, not Room
Pass enrollment or an issued Dex token. Stopping a Room blocks new enrollment/assertions;
it does not revoke existing tokens. Open Kubernetes watches are not immediately
re-authorized on every event. The deployed implementation bounds each subscription by session/token
expiry. The new shared-stream integration uses 0.3.0's authorization checks at snapshot cycles plus a 30-second
`ReauthorizationInterval` and five-second `ReauthorizationTimeout`. Timed checks pause
that subscriber's delivery; denial, timeout or changed projection policy terminates it.
The target withdrawal-to-termination bound is 60 seconds, including check and sink
scheduling, subject to cancellable callbacks and bounded writes. At 200 subscribers,
periodic list/watch SARs add about 13.3 requests/second plus opening/cycle checks.
The captured principal does not refresh session validity; Voter owns its expiry deadline. RBAC withdrawal
is rechecked for that captured subject; IdP membership changes do not update the
subject or already-issued token claims. Those changes remain bounded by session/token
expiry, not the 30-second SAR interval. See the [revocation timing](docs/authorization.md#revocation-timing-and-the-cached-subject).
Tests must prove
that expiry or denial ends the affected subscription and rejects reconnects while
other authorized viewers continue. The shared upstream stops when its last subscriber
leaves, not when the first attendee's session expires.

## Live editing: implementation versus target

Voter pins krm-stream gateway/kube and npm `@configbutler/krm-stream`
to **0.3.0**, used through `useLiveCoffeeConfig`. Production revision `85de0c0`
enables the library's documented
[SharedBackend integration](https://github.com/ConfigButler/krm-stream/blob/main/docs/auth.md#two-things-that-are-easy-to-confuse).
One process-wide backend instance maintains one upstream watch per scope. The first
subscriber opens it; later authorized subscribers receive a snapshot from its cache
and subsequent events. Bounded live-event queues isolate slow viewers and trigger
library resynchronization. No custom fan-out/cache is added to Voter.

With one replica and one CoffeeConfig scope, 200 subscriptions therefore share one
steady-state Kubernetes watch while retaining 200 browser SSE connections and individual
access reviews. Sharing is process-local; more replicas mean more upstream watches.
Per-browser drafts remain independent. This integration is required alongside the editor
refactor, in a separate reviewable change. A 200-session rehearsal must verify actual
watch count, convergence, resource usage, reconnect bursts, expiry and isolation;
sharing support alone is not evidence that the deployed system meets the capacity goal.

The storefront uses a read-only library store. AdminScreen now renders the library's
draft, derived changes and conflicts directly. Every edit and row operation goes
through store APIs. Products/vouchers use atomic array reconciliation; existing SKU/code
inputs are read-only. Display fallbacks do not modify the resource or its base.

The diagram describes the deployed editor/save and shared-watch implementation:

```mermaid
flowchart LR
    K[Kubernetes] -->|one service-account watch per scope| B[krm-stream SharedBackend]
    B -->|cached snapshot and live events| G[Gateway per subscriber]
    G -->|SubjectAccessReview before disclosure| K
    G -->|projected SSE per authorized browser| S[krm-stream store per browser]
    S -->|draft and field state| U[Vue coffee editor]
    U -->|setValue and conflict resolution| S
    S -->|explicit patch and base version| H[Voter save handler]
    H -->|conditional participant PATCH| K
    H -->|save receipt| U
    H -->|CommitRequest| C[ConfigButler]
    C -->|observed commit status| U
```

A thin Vue binding follows the upstream example and owns subscription cleanup for
one fixed UID; the connection creator owns closing the connection. Snapshot discovery
precedes editor mounting, and replacement identities receive new editor instances.
Use `connectManagedResourceStream` over same-origin fetch with the existing HttpOnly
cookie. Its states cover connecting, syncing, live, retrying, closed, terminal and
exhausted; Voter maps error codes to session/access presentation and login actions.
No token moves into JavaScript. The host save controller follows the tested example,
using exported `captureSave` and `captureReconciliation`; it owns HTTP/CSRF/receipts,
not merging or retry scheduling. Voter supplies the CoffeeConfig type/scope,
editable fields, labels and business actions. No parallel server copy, dirty registry or handwritten merge exists
in the component. Path addresses use arrays of segments, including numeric indices,
so arbitrary object keys remain unambiguous.

A stream snapshot is the sole initialization path. Manual and conflict-recovery GETs
use `captureReconciliation` on the same store, preserving GVK, UID, resourceVersion and
JSON value types. REST reads use `gateway.Project(ProjectionFull)` and no-store caching.
Save responses are receipts, not resource objects. The wrapper checks resource identity
and basic shape; it retains unknown resource fields instead of rebuilding business DTOs.
Redactions render as separate indicators. CoffeeConfig Secret references are name/key
references, not payloads; Voter does not resolve them or invent GET redaction counters.

Arrays are atomic unless an authoritative structural schema declares associative
keys. Optional keyed behavior uses `withOpenAPIKeyedLists`; Voter does not invent its
own positional reconciliation. Products/vouchers require schema uniqueness and key
semantics before enabling it. Keep existing SKU/code fields read-only initially;
create/delete are explicit actions. A future rename must explain replacement identity
and handle references. Even with keyed merging, RFC 7386 writes arrays whole.

## Save consistency

Three-way merge compares previous server state, local draft and incoming server state.
It protects typing from incoming events. It cannot prevent an unseen concurrent write
between the last event and a save. Optimistic concurrency closes that separate race.

The editor calls `store.captureSave(uid)` before any await, capturing a detached
patch, resourceVersion and UID from the same base. The
host validates scope, editable paths and projection (`gateway.ValidateMergePatch`),
then submits a conditional PATCH using the participant token. A stale version produces
409. Capture `store.captureReconciliation(uid)` before a most-recent, uncached,
projected reread. Accepted reads advance the base and preserve local edits. A rejected
guard can mean a newer event/read, snapshot recovery, missing UID or unknown redaction
metadata; its boolean is not a reason taxonomy. Follow the example's conservative
read recovery before enabling another write. Never attach a newly fetched version to the old
patch: that would bypass the protection. A deleted/recreated resource is a new identity.

Use `ProjectionFull` for this integration. Projection may suppress invisible changes,
so an observed version can legitimately be stale: refresh/reconcile on rejection,
not a reason to remove the precondition. A narrow patch alone does not prevent lost
updates to the same field or atomic array. The delivered resourceVersion is genuine
but identifies the last delivered revision, not guaranteed current state; keep it opaque.
Full projection can suppress bookkeeping-only changes; spec projection also suppresses
status changes. An accepted GET refreshes the base, but subsequent churn can reject a
new save again. Keep conditional merge patch as Voter's baseline. SSA ownership is a
different host policy, not per-user stale-read protection or a drop-in use of these helpers.
0.3.0 provides the client primitives; Voter retains mandatory precondition enforcement
and actual Kubernetes mutation. A client helper cannot enforce
policy against callers that bypass it.

The save response is HTTP 200 with a receipt containing Kubernetes-save and
commit-request status, **without a resource object**. This preserves partial-success
reporting that a bare 204 cannot express. The normal watch echo updates the store;
a missing echo recovers through a projected read. Remove Voter's save-object adoption
path. Library synchronization must preserve edits made during the request and prevent
a delayed read from regressing newer state. Save failure leaves the form and draft visible. Session expiry, disconnect, forbidden access and
resource deletion are explicit states; a failed stream never masquerades as live.

The implemented UI distinguishes a stale version (“Configuration refreshed; review and save again”)
from actual conflicting fields, recovering state, and an unavailable/replaced object.
Serialize saves; require live state, the original UID and resolved conflicts. The
example's `saved` outcome means Kubernetes accepted the write; pending synchronization
and Git receipt states remain separate. Never automatically retry a stale payload.
`takeTheirs` resolves a conflict; a reviewed keep-local action can resolve then reapply
the chosen value as a new store edit, with no intervening await and boundary tests.
There is no exported keep-local primitive in 0.3.0.

Deletion is an exception to draft retention: store pruning removes the old draft.
Offer recovery of unsaved text from a host-owned in-memory copy captured before pruning,
with explicit missing-object presentation. Never transfer it to a replacement UID;
clear it on disposal/logout/identity change. This recovery copy is not another merge base.
Cross-login draft restoration is not implemented: navigating away discards the copy.
A reconnect within the mounted editor preserves its draft; reauthentication navigation
needs a separate identity-bound recovery flow before claiming preservation across login.

0.3.0 reconnects with snapshots, not downstream replay. Routine upstream watch closure
also resnapshots subscribers; a warm shared cache saves watches, not browser bytes.
Measure resets by cause and snapshot traffic during the 200-attendee rehearsal. Upstream
checkpoint continuation is a separate 0006 follow-up, not a second Voter watch engine.

## Coffee and Git responsibilities

Voter serves the SPA and backend in one image. Current coffee endpoints are
`GET /public/storefront`, `POST /public/orders`, `GET /public/vouchers`,
`GET,PATCH /public/coffeeconfig` and `GET /public/stream`. Pricing and voucher enforcement
remain server-authoritative. Changing maximumUsage affects subsequent orders.

Redemptions are currently in process memory: a restart resets counts and a second
replica would enforce a different tally. Keep one replica until shared atomic
persistence exists. A resource watch is neither order storage nor change history.
Voting is restored in the deployed increment described below; unsupported admin-order
and history screens remain separate work.

ConfigButler should own durable Git history and commit completion. The UI needs three
separate facts: Kubernetes saved, CommitRequest accepted, Git commit observed. The deployed receipt uses `commitRequested` for request acceptance and does not
claim an observed commit. The previous revision called that flag `committed`.
The previous cluster verification found no CommitRequest CRD; installing it and proving
the Git payoff remain platform work.

## Release and deployment boundaries

Room Pass will publish its own versioned image/manifests and maintain its own minimal
OIDC fixture. Voter will test against those releases. Voter-specific CoffeeConfig and
live-editor tests move out of Room Pass before its extraction is complete. Generic
merge/transport regression suites live in krm-stream; application tests prove the
integration, authorization and user-visible race handling. Adopt published 0.3.0
npm and Go artifacts; the duplicate editor has been replaced using its existing APIs.
Both Go modules require Go 1.27.1; validate the consuming module, CI and image builds
outside upstream's workspace. Main consumes published packages and production consumes
validated releases. Track 0006's contract and watch-continuation work independently;
neither a new Vue package nor a generic save controller is a migration prerequisite.

The live deployment is owned by Flux in the private `ConfigButler/k8s` repository:
`external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/`. Publish tested artifacts, update
image references in Git, wait for Flux, then verify deployed digests and the browser
journey. Roll back by reverting the Git change. Do not mutate live application
workloads with kubectl; disposable fixtures use explicit local kubeconfigs.

On **2026-09-11**, CI for `d7d38ba` passed all jobs, including browser tests and both
image publications. GitOps commit `5d3d176772587193571d557f93547839478a4b6b` deployed
The first rollout used Voter and Room Pass `sha-d7d38ba` with explicit digest pins. At
that verification, Flux `voter-demo` was Ready
at that revision; both running pod image IDs match the published artifacts.
The public build-info endpoint reports `d7d38ba` with a clean build, and a Chromium
smoke check reached the Room Pass enrollment form through the public application.
The user also confirmed the deployed app works. Authenticated editing was exercised
in the disposable fixture; the production smoke test stopped at login.

The library-store migration, conditional saves and shared streaming are all deployed;
an authenticated production smoke test of the shared stream is still outstanding.
[PLAN.md](PLAN.md) records release digests, rollback,
remaining acceptance criteria and platform follow-up. Design history belongs in Git.

## Earlier voting release verification

Voter `14dffd4` was deployed through platform GitOps commit `ef45717`, pinned to
`sha256:9f82a3157b7c160506f6332aaaae6ae9d538a5fb8f218491d87ea505d8b23e89`.
Room Pass remains on `d7d38ba`. Full CI run `34584101156` passed. Flux applied the
GitOps revision, the ready pod uses the matching digest, and public build metadata
reports the new revision. The sample `demo-round-1` is live; production `/vote`
reaches the enrollment form. Authenticated voting was tested with two independent
browser identities in the disposable fixture; no production QuizSubmissions were created.

## Current release verification

[CI run 34593086461](https://github.com/sunib/voter/actions/runs/34593086461) passed
all jobs for Voter `55e287d`. Platform GitOps commit `59fc828` deployed image
`ghcr.io/sunib/voter:sha-55e287d@sha256:ccffb1ca260f78e46af1316e639faabea73b93a160b056a0938d86dd0ce4d186`.
Flux reports Ready at that revision, the rollout completed, and the ready pod image ID
matches the published digest. Public build metadata reports `55e287d` and `gitDirty: 0`.
Room Pass remains on `d7d38ba` because its source was unchanged.

The live sample quiz, “How do you change Kubernetes configuration today?”, is available
at [demo-round-1](https://voter.koudijs.dev/answer/demo-round-1), with
[results](https://voter.koudijs.dev/answer/demo-round-1/results) and the
[coffee editor](https://voter.koudijs.dev/admin) using the same room login.
Room `demo` is open through **2026-09-30 18:00 UTC** at this verification.
Chromium checked all three routes through the room-code form without page errors.
Authenticated editing and voting were covered in the fixture and CI; this deployment
check created no production QuizSubmissions. Revert platform commit `59fc828` to restore
the previous Voter image while retaining the quiz.

## Voting rounds

The restored voting flow uses the same Dex application session as coffee. `/vote`
reads QuizSessions through `/public/rounds`; `/answer/:session` reads a fresh round
and posts answers with its UID/resourceVersion and the session CSRF token. The
backend rereads the round, requires `state: live`, checks the question version,
required answers, answer types, choices and numeric/text bounds, then constructs a
QuizSubmission with the participant token. Namespace, timestamp and QuizSubmission name
are server-owned. No ForwardAuth route or browser-supplied identity remains.

QuizSubmissions are create-only through Voter: drafts can change before submission,
but there is no update, patch or delete endpoint for submitted answers. Participant
RBAC grants create/read access without update or patch. This is not a CRD-wide
immutability guarantee against administrators or other identities with broader grants.
Kubernetes stores QuizSubmissions durably. A deterministic hash of round UID and OIDC
subject supplies the create-only QuizSubmission name, making duplicate tabs/retries one
vote per enrollment in that round. Reopening a round preserves votes; create a
new named round for another vote. Drafts are scoped to participant and round UID.
`/public/rounds/:name/results` reads QuizSubmissions selected by round UID and returns
counts, numeric averages and text answers without QuizSubmission metadata. The browser
refreshes results on demand; this flow opens no extra watches or polling loops.

Round definitions come from GitOps; see `voter/config/demo-round.yaml`. Keep the
question set unchanged after voting starts: changing it can invalidate existing
answers, which results exclude. A close takes effect when the submission handler
reads the round; a submission already in flight can finish afterward because a
round read and QuizSubmission create are separate Kubernetes operations.

This is an audience demo, not an anonymous voting or election system. Participants
can read submissions through their Kubernetes grants; the UI explains that answers
are shared. Validation and duplicate rules are enforced by the application, not
admission, and direct API writers can bypass them. One enrollment is one identity,
not proof of one physical person. Stronger rules would require a separately scoped
admission/policy design. Existing coffee shared-stream and merge work is independent
of restoring voting.
