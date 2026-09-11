# Architecture

Voter is a coffee demo consuming public infrastructure components. Room Pass is an
independent enrollment service being prepared for extraction. krm-stream owns generic
live-resource behavior. This document separates the implementation at `2fecdd5`
(reviewed **2026-09-11**) from the target in [PLAN.md](PLAN.md).

## Ownership

| Component | Owns | Boundary |
| --- | --- | --- |
| Voter Vue app | Coffee screens, cart, field presentation, save intent | Uses library resource state; no custom reconciliation |
| Voter Go backend | Application OIDC session/CSRF, fixed resource scope, participant-token requests, coffee pricing/orders, commit-request orchestration | No fallback participant writes as a ServiceAccount; no generic stream engine |
| krm-stream | Kubernetes watch-to-SSE protocol, projections, snapshots, reconciliation, draft/conflicts, patch generation, generic recovery and optional framework adapters | No coffee semantics, application credentials, login UI or Git commit workflow |
| Room Pass | Room lifecycle, rotating codes, browser enrollment, stable participant identity, bound handoff to Dex | No application grants or token signing; no Voter dependency |
| Dex | OAuth 2.0/OIDC authorization server and OpenID Provider, connectors, codes and signed tokens | Trusts Room Pass assertions only through a protected authproxy integration |
| Kubernetes | Resource storage, token authentication, RBAC, admission and audit | Identity does not itself grant permission |
| ConfigButler | Persisting accepted configuration changes to Git, commit status/history | Request acceptance is distinct from an observed commit |
| Platform GitOps repository | Installed versions, routes, CRDs, RBAC, issuer configuration and deployment | Live application changes arrive through Flux |

Generic recovery/framework APIs in this table are the target ownership, not a claim
that every necessary API is already released. Prefer existing public APIs; contribute
missing behavior upstream with a non-demo example rather than copying it into Voter.

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
    B->>V: Resource request with session cookie
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

Participant reads, writes and watches use the session's token against a fixed API
server with TLS verification. Browser Authorization/Impersonate headers cannot select
another identity. Kubernetes makes the authorization decision. The stream adds a host
scope restriction; today it allowlists CoffeeConfig as a resource type, while the
target also fixes namespace and name. Do not substitute a privileged shared watch
without an explicit authorization model for every subscriber.

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

Application mutations require CSRF proof. Logout clears the Voter session, not Room
Pass enrollment or an issued Dex token. Stopping a Room blocks new enrollment/assertions;
it does not revoke existing tokens. Open Kubernetes watches are not immediately
re-authorized on every event. The target bounds stream lifetime by session expiry
and handles authorization denial when opening/reopening a watch.

## Live editing: implementation versus target

Today `/public/stream` mounts krm-stream gateway/kube 0.2.1. The browser uses npm
`@configbutler/krm-stream` 0.2.1 through `useLiveCoffeeConfig`. Each caller gets an
upstream backend using their token; **there is no shared-watch coalescing in Voter**.
This preserves identity isolation at a cost proportional to active browser streams.

The storefront renders live objects, but AdminScreen takes `live.server` into its old
`reconcileValue` and maintains a separate draft/dirty/conflict state. This is the main
architectural defect. The seven passing browser tests prove delivery and one benign
merge case; they do not prove safe concurrent editing. The new blank-draft guard is
not a substitute for a typed resource contract or a tested merge.

The target has one state owner and one write path:

```mermaid
flowchart LR
    K[Kubernetes] -->|participant watch| G[krm-stream Go gateway]
    G -->|projected snapshot and events| S[krm-stream resource store]
    S -->|draft and field state| U[Vue coffee editor]
    U -->|setValue and conflict resolution| S
    S -->|explicit patch and base version| H[Voter save handler]
    H -->|conditional participant PATCH| K
    H -->|save receipt| U
    H -->|CommitRequest| C[ConfigButler]
    C -->|observed commit status| U
```

The generic Vue binding synchronizes library state with rendering and owns subscription
cleanup. Voter supplies the CoffeeConfig type/scope, editable fields, labels and
business actions. No parallel server copy, dirty registry or handwritten merge exists
in the component. Path addresses use arrays of segments, including numeric indices,
so arbitrary object keys remain unambiguous.

A stream snapshot, a manual reread and conflict recovery must all enter the same store
with one projected KRM contract. Preserve GVK, UID, resourceVersion and JSON semantics.
Use the library's projection for HTTP reads as well; display defaults never mutate the
merge base. Current Go CoffeeConfig DTOs omit UID and use `omitempty` on values whose
absence may differ from zero/false. They can remain useful for coffee business logic,
but must not define a lossy generic editor transport.

Arrays are atomic unless an authoritative structural schema declares associative
keys. Optional keyed behavior uses `withOpenAPIKeyedLists`; Voter does not invent its
own positional reconciliation. Products/vouchers require schema uniqueness and key
semantics before enabling it. Even with keyed merging, RFC 7386 writes arrays whole.

## Save consistency

Three-way merge compares previous server state, local draft and incoming server state.
It protects typing from incoming events. It cannot prevent an unseen concurrent write
between the last event and a save. Optimistic concurrency closes that separate race.

The target captures explicit patch, resourceVersion and UID from the same base. The
host validates scope, editable paths and projection (`gateway.ValidateMergePatch`),
then submits a conditional PATCH using the participant token. A stale version produces
409. Reread and reconcile through the library, preserve edits, and ask the user to
resolve overlaps before another save. Never attach a newly fetched version to the old
patch: that would bypass the protection. A deleted/recreated resource is a new identity.

Use `ProjectionFull` for this integration. Projection may suppress invisible changes,
so an observed version can legitimately be stale: handle that as a conflict/rebase,
not a reason to remove the precondition. A narrow patch alone does not prevent lost
updates to the same field or atomic array. The library's save guidance leaves optimistic
concurrency to the host; Voter deliberately requires it.

Prefer a receipt containing Kubernetes-save and commit-request status, with the normal
watch echo updating the store. If the echo does not arrive, recover with a projected
read. Any HTTP object adoption must preserve edits made during the request and reject
regression behind newer stream state using tested library behavior. Save failure must
leave the form and draft visible. Session expiry, disconnect, forbidden access and
resource deletion are explicit states; a failed stream never masquerades as live.

## Coffee and Git responsibilities

Voter serves the SPA and backend in one image. Current coffee endpoints are
`GET /public/storefront`, `POST /public/orders`, `GET /public/vouchers`,
`GET,PATCH /public/coffeeconfig` and `GET /public/stream`. Pricing and voucher enforcement
remain server-authoritative. Changing maximumUsage affects subsequent orders.

Redemptions are currently in process memory: a restart resets counts and a second
replica would enforce a different tally. Keep one replica until shared atomic
persistence exists. A resource watch is neither order storage nor change history.
The quiz path still contains retired ForwardAuth assumptions; unsupported flows are
candidates for removal, not automatic restoration work.

ConfigButler should own durable Git history and commit completion. The UI needs three
separate facts: Kubernetes saved, CommitRequest accepted, Git commit observed. Current
`committed: true` means only request creation. The demo cluster still lacks the
CommitRequest CRD as of this review, so the commit payoff is not implemented there.

## Release and deployment boundaries

Room Pass will publish its own versioned image/manifests and maintain its own minimal
OIDC fixture. Voter will test against those releases. Voter-specific CoffeeConfig and
live-editor tests move out of Room Pass before its extraction is complete. Generic
merge/transport regression suites live in krm-stream; application tests prove the
integration, authorization and user-visible race handling.

The live deployment is owned by Flux in the private `ConfigButler/k8s` repository:
`external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/`. Publish tested artifacts, update
image references in Git, wait for Flux, then verify deployed digests and the browser
journey. Roll back by reverting the Git change. Do not mutate live application
workloads with kubectl; disposable fixtures use explicit local kubeconfigs.

At the 2026-09-11 check, Flux was Ready at `2cb475d`, running Voter `sha-ee001d6`
and Room Pass `sha-2a90ef2`. Source `2fecdd5` and the target architecture above are
not deployed. [PLAN.md](PLAN.md) holds the remaining acceptance criteria and retained
platform follow-up; design history belongs in Git.
