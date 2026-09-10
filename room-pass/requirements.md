# Room Pass requirements

Status: initial implementation contract. See [README.md](README.md) for implemented behavior, verification, and remaining platform work.

Implementation decision (2026-09-09): default `validFor` is **30s** with **15s** rotation (two overlapping codes), per the requested typing grace period. The 60s/four-code examples below remain supported configurations, not the shipping default.

Room Pass lets people join a demo with a room code and a display name. It assigns each
participant a stable demo identity and supplies that identity to Dex. Dex issues the
tokens; Kubernetes decides what those participants may do.

**Room Pass is controlled declaratively through Kubernetes `Room` and `Participant` CRDs and a
reconciling controller from its first version.** Kubernetes is the source of truth for
event configuration and lifecycle; there is no parallel event-management database.

This document defines the first version. The wider cluster, apps, and repository layout
are covered by the platform implementation plan, which lives in the private
`ConfigButler/k8s` repository.

## 1. What the first version should feel like

The presenter applies a `Room` resource and reads its current rolling code from
operator-readable Room status, for example `BCD-FGH`. They show
the join URL and code to the room. A participant opens the URL, enters the code and a
display name, and returns to the demo ready to participate.

No account, email address, GitHub membership, or software installation is required for
browser participation. Prepared CLI users can authenticate through Dex using the same
Room Pass enrollment. Reauthentication in the same enrolled browser preserves identity.

The participant page should be phone-friendly and explain three things plainly:

- Enter the room code and choose a display name.
- The name is a demo label, not a verified personal identity.
- Demo changes may appear in Git with that name and a generated email address.

Do not show issuer URLs, Kubernetes groups, token details, or other implementation
settings in the normal join flow.

## 2. Scope and ownership

| Room Pass owns | Other components own |
|---|---|
| Event enrollment and room-code generation | Dex: OAuth/OIDC protocol, tokens, signing keys, discovery |
| Participant IDs, nicknames, synthetic author emails | Kubernetes: token validation, RBAC, admission |
| Browser sessions and login handoff transactions | Traefik: TLS routing and approved ingress paths |
| Authenticated identity headers for Dex authproxy | Voter/Coffee: application logic and OIDC clients |
| Opening, closing enrollment, and stopping authorization | Platform configuration: grants, route shutdown, quotas and cleanup |

Keep Room Pass in `room-pass/` within this repository. Start with Go, a small web UI,
controller-runtime, Kubernetes-backed participant records, one replica, and Traefik examples. Use the shared root devcontainer
and Taskfile conventions as those are introduced.

The first version includes two namespaced CRDs, `Room` and `Participant`. A deployment
initially serves one configured Room reference, while the API permits additional Rooms
without redesigning the resource. It does not include a separate Session CRD, participant
namespaces, an impersonation adapter, its own JWT issuer, other ingress controllers, or
a general-purpose user directory. Its ServiceAccount has scoped Room/Participant/status access and access to its cookie-key Secret,
not permission to create participant RBAC or impersonate users.

### Kubernetes API: `Room`

API identity for implementation: `roompass.configbutler.ai/v1alpha1`, kind `Room`, plural `rooms`,
namespaced scope. This is the intended API contract, not an installed/generated CRD yet.

```yaml
apiVersion: roompass.configbutler.ai/v1alpha1
kind: Room
metadata:
  name: configbutler-demo
  namespace: room-pass
spec:
  title: ConfigButler demo
  endsAt: "2026-09-16T16:00:00Z" # illustrative; choose the actual event time
  enrollment: Open
  stopped: false
  maxParticipants: 300
  audienceGroup: "demo:configbutler-20260916"
  allowedReturnURLs:
    - https://demo.configbutler.ai/coffee/
    - https://demo.configbutler.ai/vote/
  joinCode:
    rotateEvery: 15s
    validFor: 60s
    length: 6
```

| Desired field | Contract |
|---|---|
| `title` | Required, nonempty, bounded display text |
| `endsAt` | Required timestamp, editable by operators to accommodate schedule changes; shortening ends access sooner and extending can reopen an expired, non-stopped Room |
| `enrollment` | `Open` or `Closed`; defaults to `Closed` |
| `stopped` | Defaults to false; transition to true is irreversible for this object |
| `maxParticipants` | Required positive bounded integer; lowering below the enrolled count blocks new joins without deleting participants |
| `audienceGroup` | Required, immutable, allowed demo prefix; only trusted operators may configure this authority-bearing value |
| `allowedReturnURLs` | Required bounded set of exact HTTPS destinations; immutable initially; platform allowlist further restricts it |
| `joinCode` | Rolling-code settings; defaults proposed as `rotateEvery: 15s`, `validFor: 60s`, `length: 6`; immutable per Room initially, with schema bounds to limit retained history |

The Kubernetes object UID is the internal event identity. Bind every participant, session,
and handoff to it, not merely to the Room name. Deleting and recreating the same name must
not revive old sessions. **Audience groups may be shared across Rooms intentionally.**
They select the same Kubernetes RBAC permissions; Room Pass neither enforces uniqueness
nor creates a webhook to police this choice. A new UID separates enrollment identities,
not group permissions. Operators own the group-to-permission mapping.

`status` is controller-owned through the status subresource and contains:

- `observedGeneration`: the spec generation processed by the controller.
- Conditions using `metav1.Condition`, keyed by type, including `Ready`,
  `EnrollmentOpen`, and `AuthorizationAllowed`, with reason, message, and observed generation.
- `joinCode`: the newest published code and its `issuedAt`/`expiresAt`.
- `validJoinCodes`: bounded unexpired history, including the current code, for overlap
  and restart recovery. Codes are never desired spec or Git configuration.
- `participantCount`: a periodically reconciled observation, not the enforcement counter.

`Ready` means configuration and required state are prepared, not that enrollment or
Kubernetes access is allowed. A stopped Room can be reconciled successfully while
`AuthorizationAllowed=False`. These conditions do not claim to observe platform RBAC
revocation. Conditions and return-URL lists need explicit map/set schema semantics for
server-side apply. Validate enums, bounds, immutability and transitions in the generated
schema; do not depend on a CLI doing validation. Provide printer columns and meaningful
`kubectl explain` descriptions. [Kubernetes custom resources](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/).

### Rolling codes: reuse the existing behavior

Follow [the original join-code design](../plans/join-code-plan.md) and
[the current implementation](../voter/join_codes.go): generate codes periodically,
resolve a code to a Room, and let a valid enrolled browser continue without another code.
Rotation does not end participant sessions.

The existing defaults are 15-second rotation, four characters and a two-hour code TTL.
Keep rotation at 15 seconds. Proposed Room Pass defaults are six characters and a
60-second validity window, so someone typing the previous displayed code can still join.
This overlap is intentional; rotation does not immediately invalidate the previous code.
Use the fixed alphabet `BCDFGHJKLMNPQRSTVWXZ` with cryptographic randomness.
`length` counts code characters, not separators; six characters display as `BCD-FGH`.
Accept lowercase, optional display hyphens and outer whitespace, then normalize before
comparison. At steady state, 15-second rotation with 60-second TTL permits **four**
valid codes. Use `now < expiresAt` and check collisions across the whole retained set. Fail rather than publish a collision or substitute predictable bytes
if randomness fails. Bound retries and retain only unexpired history. Code length, the
number of accepted codes, validity, and online attempt limits jointly bound guessing;
rotation alone does not make guessing harmless. Record concrete rate-limit and burst
settings before the load-test checkpoint.

One deployment serves one Room initially, so no distributed cross-Room uniqueness
mechanism is required. A future shared code-only entry point for multiple Rooms must
resolve collisions centrally or ask for Room context; do not silently choose a match.

The controller publishes the code/history in Room status using optimistic concurrency.
Generate only when a rotation is due, not on every reconcile. Publish successfully before
accepting a new code; after restart, use the persisted unexpired history. Use timestamps
for validity even if expiry reconciliation is delayed. Do not backfill missed rotations.
Closing enrollment clears accepted codes; reopening starts a fresh code. Stopping,
expiry and deletion deny enrollment regardless of stale status.

**Room read access grants knowledge of working enrollment codes.** Kubernetes RBAC does
not hide status fields from someone who can read a Room. Keep Room get/list/watch access
operator-only; the public join/status page returns a sanitized view without codes. Do not
copy these Rooms into broad audience reader roles, logs, Git or publicly exposed audit
bodies. Use metadata-level audit for these resources where bodies would disclose codes.
The room code is deliberately shared on the projector; a separate Secret per code is not
needed. Only cookie-signing/encryption keys remain in a Kubernetes Secret. For projection,
show a dedicated view of this Room’s title, current code and expiry rather than a full
YAML dump or an all-Room watch.

Request handlers read current Room and Participant state before enrollment or Dex identity
assertion; missing/deleting/stopped/expired/revoked resources deny access. Uncached reads
are the initial fail-closed baseline; API unavailability denies new authorization. Do not
use status counters or stale informer state as an authorization decision. Requests already
in flight can complete; stopping is not an instantaneous distributed barrier.

### Kubernetes API: `Participant`

Use `roompass.configbutler.ai/v1alpha1`, kind `Participant`, plural `participants`,
namespaced in the same namespace as its Room. Create one per enrolled browser identity,
not per request, token refresh, or OIDC transaction.

```yaml
apiVersion: roompass.configbutler.ai/v1alpha1
kind: Participant
metadata:
  name: p-<server-generated-random-id>
  namespace: room-pass
spec:
  roomRef:
    name: configbutler-demo
    uid: <actual-room-uid>
  displayName: Ramon
  revoked: false
```

Room Pass creates the Participant with a same-namespace Room owner reference. Its name
contains at least 128 bits of randomness; its Kubernetes UID binds the browser cookie to
that exact enrollment. Room reference and display name are immutable; eligibility follows
the Room’s current `endsAt`, so extending a talk does not require rewriting Participants. Revocation
is irreversible for the object. Derive the author email and group from server-owned
identity/Room data rather than accepting them in a participant payload. Audience users
cannot directly create, read, patch or list Participants through Kubernetes.

A signed/encrypted HttpOnly cookie references the Room and Participant UIDs and expiry.
It contains no bearer token accepted without cookie verification. Listing Participants
must not enable impersonating them. Persist cookie keys in one protected Secret so a pod
restart preserves sessions. No Session CRD or application database is required.

The initial single serving replica serializes enrollment per Room, counts existing
Participant records and creates the new record before returning a cookie. Rebuild this
view before readiness after restart; an uncertain API create must be resolved using its
original generated name, not retried under a new identity. Multiple serving replicas and
other enrollment writers are out of scope until distributed limit enforcement exists.
`status.participantCount` is an observation, not an atomic reservation. Count retained
records for the event, including revoked records, to keep the event's enrollment bounded.

Participant deletion or revocation invalidates the cookie when checked. Garbage collection
removes Participants when the Room is deleted; the Room’s current end time still applies
before cleanup runs. Keep Participant records until Room deletion in the first version,
so extending an expired Room does not lose them to an expiry cleanup job.
Recreating a Room or Participant name cannot revive a cookie bound to the previous UID.
Keep short-lived, one-time OIDC handoffs in bounded process memory: a restart requires
restarting an unfinished login, but preserves enrolled identities. There is no per-login
Kubernetes resource or heartbeat write. No finalizer is needed for these local resources.

## 3. Event behavior

Each event is declared by a `Room` with title, end time, participant limit, server-owned
audience group, and explicitly allowed application return destinations. Changing the identity of
an event requires a new event; a stopped event must not silently reopen on restart.

| Event state | New enrollment | Existing participant may authorize a new Dex login |
|---|---|---|
| Open | Yes, with the current code | Yes |
| Enrollment closed | No | Yes |
| Stopped or expired | No | No |

Requirements:

1. Automatically roll the code while enrollment is open, using the bounded overlap above.
2. Expose codes to the presenter through Room status, not the participant-facing API.
3. Keep enrolled participant identities unchanged across rotations.
4. Enforce end time and enrollment limits server-side; stale status never extends access.
5. Preserve desired state, valid code history, participant identities, and cookie keys
   across pod restart. A stopped event cannot reopen through restart or reconciliation.

The room code proves possession, not physical attendance or one-person-one-vote.

## 4. Participant identity and session

| Value | Requirement |
|---|---|
| Participant ID | At least 128 bits of cryptographic randomness, assigned by the server and immutable within the event |
| Display name | Participant-supplied, validated and escaped; never used as an authorization key |
| Author email | Server-generated `<participant-id>@demo.invalid`; never supplied by the participant |
| Group | Taken from event configuration, never from client input |
| Session | Signed/encrypted cookie referencing persisted Room and Participant UIDs; no separate Session CRD |

Validate display names using the existing Git-safe rules where suitable: reject control
characters and Git author delimiters; impose a documented length bound. Do not collect
a real email address. For the first version, the nickname remains fixed after enrollment.

The production session cookie must be Secure, HttpOnly, explicitly SameSite, and
host-only. State-changing browser requests need CSRF protection. Use a configurable
cookie lifetime (initial default: 24 hours), independent of the scheduled end time.
Every authorization checks both cookie expiry and the Room’s current state/end time.
A cookie remaining on disk after the talk does not override a stopped or expired Room.
Extending the Room preserves identity while the cookie remains valid; extending an event
does not resurrect an expired cookie.

**Sign out clears the browser cookie; it does not revoke the Participant.** Revocation is
an explicit operator action through `Participant.spec.revoked`. Clearing the only cookie
also clears the browser’s proof of enrollment: joining again creates a new Participant
and consumes another slot. We do not recover identity from a nickname or add a recovery
credential just to hide this tradeoff. Existing retained records count toward the cap;
operators can raise `maxParticipants` when needed. A copied valid cookie remains usable
until its expiry, Participant revocation, or Room shutdown.

A returning valid session reuses its participant ID. Clearing cookies or switching to
another browser can create another identity; cross-device account recovery is out of
scope. Device-flow users preserve identity only when they authenticate in the same
enrolled browser. Signing out does not revoke already-issued Dex tokens.

## 5. Dex and Traefik contract

Room Pass authenticates the browser request to the Dex authproxy connector callback.
It must not expose a public endpoint that returns trusted identity headers merely
because a caller supplied a nickname or participant ID.

The initial identity contract is:

```text
X-Remote-User-Id:    <participant ID>
X-Remote-User:       <display name>
X-Remote-User-Name:  <display name>
X-Remote-User-Email: <participant ID>@demo.invalid
X-Remote-Group:     <configured event group>
```

`X-Remote-User` becomes Dex's `name` claim. Dex encodes the participant and connector
IDs into its opaque `sub`; Kubernetes adds the demo username prefix. Clients request
`openid profile email groups`. Authproxy marks email verified despite the synthetic
address; the product must not describe this as mailbox verification.

The integration must:

- Remove client-supplied identity headers and set the complete contract from stored
  session/event data. Restrict the Dex upstream destination to configuration.
- Protect all routes that can reach the authproxy identity assertion, including aliases
  in the pinned Dex release. Do not block unrelated required OIDC endpoints.
- Preserve Dex's connector transaction state separately from the application's OAuth
  state. Room Pass does not exchange authorization codes on behalf of the SPA or CLI.
- Reject expired, reused, mismatched, or unknown handoff transactions and unapproved
  return destinations. Bind each handoff to the original Dex transaction and the browser
  that enrolled/authorized it, with short expiry and one-time consumption.
- Support the planned join host `demo.configbutler.ai` and issuer host
  `login.demo.configbutler.ai` without assuming a host-only cookie reaches both.
  No shared-domain cookie is required by this contract.

Configure exactly one group header value in this version. If multiple groups are added,
explicitly match the pinned Dex connector’s delimiter/header configuration and test it.

Pin and test the Dex release. For the initial deployment, disable optional Dex browser
sessions so new login authorization revisits Room Pass. Authproxy provides no refresh
token. Browser session lifetime and Dex ID-token lifetime are different configuration
choices; reauthentication must preserve the same participant identity.

## 6. Operator controls and shutdown

Use Kubernetes as the operator interface:

| Action | Kubernetes operation |
|---|---|
| Start an event | Apply a Room with `enrollment: Open` and an appropriate future end time |
| Inspect | Get/describe the Room and its conditions |
| Read code | Read/watch `status.joinCode` with operator credentials |
| Rotate code | Automatic according to `spec.joinCode.rotateEvery`; no periodic Git commits or manual counter |
| Close/reopen enrollment | Set `spec.enrollment` to `Closed`/`Open` while not stopped or expired |
| Change the schedule | Edit `spec.endsAt`; no Room recreation or participant reset |
| Revoke a participant | Set `Participant.spec.revoked: true` |
| Stop permanently | Set `spec.stopped: true` |
| Remove event | Delete the Room; old UID-bound sessions remain invalid |

An optional future CLI is a Kubernetes client, not a second management API. Operators
need Room access to see the code; audience identities get no Room/Participant reads.
Changing audience groups or return destinations is privileged configuration, not a
self-service participant capability. The controller must not create grants from arbitrary
Room contents. Its own RBAC is scoped to the configured Room namespace and owned resources.

Flux manages Room desired state under `platform/`. Make routine changes there so Flux
will not undo an imperative patch. The emergency-stop procedure must suspend the owning
reconciliation, apply the stop, then commit the stopped desired state before resuming.
Room schema validation also prevents reverting `stopped: true` on the same object.

**Stopping Room Pass authorization is not the same as ending Kubernetes access.** An
OIDC transaction already authorized by Dex may still complete, and issued ID tokens
remain usable while accepted by Kubernetes and covered by grants.

The platform stop procedure must therefore withdraw the intended event access grants,
close relevant routes, verify denied new requests, and terminate/drain existing
connections. Its Git desired state must prevent Flux from restoring those grants.
When a group is shared, removing its binding affects every holder, including other
Rooms. Operators choose whether to stop that shared access or leave it available; stopping
one Room alone does not isolate its already-issued tokens from the shared group.
Room Pass reports that authorization has stopped; it must not report that all cluster
access is revoked merely because its own state changed.

## 7. Persistence, limits, and operations

Persist Rooms, rolling-code status, and Participants in Kubernetes. Persist cookie keys
in a Secret. There is no SQLite database or application PVC. Kubernetes/etcd recovery
covers these resources; losing cookie keys invalidates browser sessions. Seamless cookie-key
rotation is outside the first version. Replacing the keys deliberately signs everyone out;
perform it between events. Multiple verification keys can be added if later needed.

Use one serving replica with Deployment strategy `Recreate`, accepting brief downtime
on rollout. Do not add leader election merely for this version. Enrollment limits are
enforced by serialization during normal single-writer operation, not a distributed
transaction under forced pod replacement or manually introduced extra writers.
Use reconciliation retries and resource-version conflicts correctly. Reject enrollment
and authorization if required state cannot be read or persisted. Bound request bodies,
nicknames, code history, participant counts and outstanding in-memory handoffs. Document
that unfinished login transactions restart after pod replacement.

Rate-limit code attempts and transaction creation in the service as well as at the
edge. Treat forwarded client IPs as trustworthy only from configured proxies. Shared
conference NAT must be included in testing; one IP must not automatically mean one
participant. Exact limits are configurable and will be chosen from load tests.

Expose liveness and readiness separately. Readiness must reflect usable configuration
and persistence. Metrics may include counts, outcomes, and latency, but never label
individual participants or include credential material. Define backup/restore and
restart behavior before production use.

## 8. Acceptance checks

These checks define completion; they are not claims that anything has passed yet.

| Check | Expected result |
|---|---|
| Fresh mobile browser, valid code and nickname | One enrollment and successful return to the intended app |
| Invalid code, invalid name, missing fields | Clear error; no enrollment or identity assertion |
| Room full | “This room is full. Please ask the presenter.” |
| Room stopped/expired | “This demo has ended.” |
| Enrollment closed | “Joining is closed. If you already joined, return to the demo.” |
| Room end extended with a valid cookie | Same Participant remains usable under the new schedule |
| Intentional shared audience group | Both Rooms may use it; no uniqueness rejection |
| Sign out then join again | New Participant; no promise of nickname-based identity recovery |
| Client chooses an ID, email, or group | Cannot influence server-owned identity values |
| Two concurrent joins at the participant limit | Limit remains enforced |
| Code rotation / enrollment closure | Old code works only until its TTL; closure denies all new joins; existing identities persist |
| Service restart | Room state, unexpired status codes, Participants and cookie keys preserved |
| Repeated reconcile / crash during rotation | Persisted unexpired history reused; no unpublished code is accepted |
| Invalid Room update | Schema rejects invalid enums, bounds, immutable-field changes, and stop reversal |
| Delete/recreate Room with same name | New UID; old sessions and handoffs remain invalid |
| Kubernetes API unavailable | New enrollment and identity assertion fail closed |
| Operator vs participant RBAC | Only operators can read Room codes; public users cannot manipulate Participant resources |
| Token expiry and re-login | Same session produces the same Dex subject and Git author fields |
| Forged headers with a fresh valid Dex transaction | Verified resulting token contains only the gate-authorized identity |
| Direct callback access from public edge or untrusted pod | Cannot assert an identity outside the approved Room Pass path |
| Cross-host handoff tampering, replay, CSRF, open redirect | Rejected without completing an unauthorized login |
| Event stop/end time | No new Room Pass authorization, including from previously valid sessions |
| Storage unavailable | Enrollment/authorization fail closed; readiness fails |
| Prepared kubelogin device flow | Browser enrollment works; no local CLI callback listener required |
| Full deployment test | Demo write succeeds, work-resource access fails, audit extras and Git author match |
| Full platform shutdown | Existing participant token cannot make new demo writes; work services remain usable |

Initial load-test target: 300 enrolled participants, including 100 simultaneous join
attempts behind a shared IP. This is a rehearsal target, not a capacity guarantee.
Direct Kubernetes reads/creates are part of this burst: measure their latency and client
QPS throttling before changing the design. Start with bounded concurrency and explicit
client budgets. A watch cache is a later optimization only with a defined freshness and
failure policy; resourceVersion alone does not prove that no newer change exists.
Measure successful join latency and storage/rate-limit behavior, then document supported
capacity and configuration before claiming conference readiness.

## 9. Implementation order

1. **Kubernetes API and controller:** Kubebuilder API types, generated Room CRD/RBAC,
   Participant types, rolling-code status, idempotence and transition tests using envtest.
2. **Join and persistence:** Participant creation, UID-bound cookies, mobile form, CSRF,
   limits, and fresh Room/Participant state checks.
3. **Dex adapter:** cross-host handoff and Traefik example, tested with a real pinned Dex.
4. **End-to-end fixture:** local Kubernetes authentication, restricted RBAC, browser/CLI
   re-login, and callback bypass tests.
5. **Deployment and operations:** image, cookie-key Secret, probes, network policy, shared-cluster
   integration, load rehearsal, and documented stop/reset procedures.

Before step 2, settle initial configurable limits and the request-time API-read budget. Before step 3,
write down the exact handoff protocol, routes, state bindings, and cookie behavior.
Before deployment, confirm cookie-key management, Dex/client versions, event duration, and
retention policy. These choices can be resolved during implementation without expanding
the first version into a generic identity platform.
