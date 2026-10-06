# Architecture

Voter is a coffee and voting demo built on public infrastructure components. Since
2.0.0 it no longer signs anyone in: **krm-foyer** owns login, the session, `/k8s` and
the live streams on the same host, and the browser reads and writes Kubernetes as the
person using it. Room Pass is an independent enrollment service (`sunib/room-pass`).
The running revision is **Voter 2.0.0 (`b446793`) behind krm-foyer 0.3.0**, live on
`demo.koudijs.dev` since **2026-10-06**. How it got here is
[docs/krm-foyer-migration.md](docs/krm-foyer-migration.md); remaining targets are in
[PLAN.md](PLAN.md).

## Ownership

| Component | Owns | Boundary |
| --- | --- | --- |
| Voter Vue app | Coffee/voting screens, cart, field presentation, save intent; its own writes through `/k8s` (ballots, saves, CommitRequests, round state, the audience grant) | Renders what Kubernetes answers; decides no permission |
| Voter Go backend | The SPA's files, `/config.json`, the QR join endpoint, coffee pricing/orders/vouchers, the QuizSession tally reconciler | Holds nobody's token and has no session, CSRF or OIDC code; learns who is asking only from krm-foyer's identity header |
| krm-foyer | OIDC client (Dex), sealed session cookie, CSRF, the `/k8s` proxy with the person's own token, the `/stream/v1` gateway with shared watches, the identity check for `/public/` | No coffee or voting semantics |
| krm-stream | Watch-to-SSE protocol, snapshots, browser store, drafts/conflicts and patch generation (npm `@configbutler/krm-stream` 0.10.0) | No application credentials, login UI or Git commit workflow |
| Room Pass | Room lifecycle, rotating codes, browser enrollment, stable participant identity, bound handoff to Dex | No application grants or token signing; no Voter dependency |
| Dex | OAuth 2.0/OIDC authorization server, connectors, codes and signed tokens | Trusts Room Pass assertions only through a protected authproxy integration |
| Kubernetes | Resource storage, token authentication, RBAC, audit, and **admission**: `voter-ballot` holds a ballot's rules and `voter-editable-spec` limits a person's edits to `spec` | Identity does not itself grant permission |
| ConfigButler (gitops-reverser) | Persisting accepted changes to Git, commit status/history | Request acceptance is distinct from an observed commit |
| Platform GitOps repository | Installed versions, routes, CRDs, RBAC, admission policies, issuer configuration | Live application changes arrive through Flux |

What using krm-stream has taught us is collected for its maintainers in
[docs/krm-stream-feedback.md](docs/krm-stream-feedback.md); the same for krm-foyer in
[docs/krm-foyer-feedback.md](docs/krm-foyer-feedback.md), and for gitops-reverser, from
operating it rather than consuming it, in
[docs/gitops-reverser-feedback.md](docs/gitops-reverser-feedback.md).

This is a demo that has to be read from the back of a room, so some robustness
was traded away for a model an audience can hold. The places where we looked at a
real failure mode and chose not to defend against it -- what keeps each gap
closed in practice, and how to recover if it bites -- are written down in
[docs/deliberate-simplifications.md](docs/deliberate-simplifications.md). A guard
that is deleted without an entry there is indistinguishable from one nobody
noticed was missing.

## One host, three programs

`demo.koudijs.dev` is split by Traefik IngressRoutes in the GitOps repository's
`voter-demo/ingress.yaml`. Explicit priorities, because each split is a security
boundary.

| Path | Owner |
| --- | --- |
| `/auth/`, `/k8s/`, `/stream/`, `/_foyer/` | **krm-foyer**: login via Dex, session cookie, CSRF, `/k8s` proxy to the API server with the person's own token, `/stream/v1` gateway with shared watches |
| `/join`, `/bind`, `/logout` (exact) | **Room Pass** |
| `/join-room?code=` | **Voter**: the QR target. Access log off, because the query carries a room code |
| `/public/` | **Voter**'s domain backend, behind Traefik ForwardAuth to krm-foyer's `/auth/check?identity=true` |
| everything else | **Voter**: the Vue SPA's files and `/config.json`, ungated |

No Voter route passes cookies on (`no-cookie` middleware): Voter has no session of
its own, and krm-foyer's is krm-foyer's. A NetworkPolicy admits only Traefik to Voter
and to krm-foyer.

The identity check answers for the browser's krm-foyer session, including CSRF and
same-origin rules for a write, and returns one header, `Krm-Foyer-Identity`: base64url
JSON with the API server's `userInfo` for that person, the session's `displayName` and
its `connector`. Traefik removes a browser's copy before adding krm-foyer's. Voter's
`requireIdentity` ([voter/foyer_identity.go](voter/foyer_identity.go)) only reads it,
and refuses a request that arrives without it, which can only mean the deployment is
wrong. It never receives a token, so it learns who someone is and cannot act as them.
It also logs every refusal it gives, because on 2026-09-17 not one refusal was logged.

### Voter's own HTTP surface

| Endpoint | Methods | Identity | Why it exists |
| --- | --- | --- | --- |
| `/healthz` | GET | — | Liveness. No dependencies, so it cannot fail because Kubernetes is unreachable |
| `/config.json` | GET | — | Where this deployment keeps its objects: namespace, CoffeeConfig and Room names, GitTargets, commit URL template, participant connector, audience Role. Settings, not identity, so served without a session |
| `/join-room` | GET | — | Sets Room Pass's join-code cookie and redirects into krm-foyer's login; see below |
| `/public/storefront` | GET | Required | The menu as the shop renders it, priced by the server |
| `/public/orders` | POST | Required | Places an order and prices it server-side. A refusal is a 200 with a failure block, so the screen can say which "no" it was |
| `/public/orders`, `/public/orders/stream` | GET | Required | The bounded in-memory feed, placements and refusals both, and its hand-written SSE. Not krm-foyer's stream: there is no watch to share and no object to authorize against |
| `/public/vouchers` | GET | Required | Redemption counts. Process-local, and the response says so |
| `/public/build-info` | GET | — in Voter; the edge still checks | Commit, build date and dirty flag |
| `/` | GET | — | The built SPA with an SPA fallback. An unknown path returns the app, not a 404 |

`/metrics` is an explicit 404; Voter has no metrics listener. krm-foyer exports
`krm_foyer_*` on its own metrics Service.

There is no Voter endpoint for rounds, results, votes, the CoffeeConfig, Databases,
permissions or the audience grant any more. Those are Kubernetes objects, and the
browser reaches them through `/k8s`.

## Identity flow and Room Pass's product boundary

```mermaid
sequenceDiagram
    participant B as Browser
    participant F as krm-foyer
    participant D as Dex
    participant R as Room Pass
    participant K as Kubernetes
    participant V as Voter
    B->>F: /auth/login?oidc.connector_id=room-pass
    F-->>B: Dex authorization request
    B->>D: Authorization request
    D-->>B: Room Pass connector callback
    B->>R: Browser-bound room enrollment/handoff
    R->>K: Read Room and Participant; enroll if eligible
    R->>D: Trusted identity assertion on connector callback
    D-->>B: Authorization code for the voter client
    B->>F: /auth/callback
    F->>D: Code exchange and token validation
    F-->>B: Sealed HttpOnly session cookie
    B->>F: /k8s and /stream/v1 with the cookie
    F->>K: Request with the person's own ID token
    B->>V: /public/ via the edge's identity check
```

krm-foyer reuses Dex client `voter` with the same redirect URI,
`https://demo.koudijs.dev/auth/callback`, and the same secret. The default connector is
`room-pass`; the operator signs in at `/login?connector=github`, which becomes
`oidc.connector_id=github`. Allowed connectors are room-pass, github and linkedin. The
SPA learns who it is from `/auth/session` (display name, groups, connector, CSRF token
and header, expiry) and `/auth/whoami` (the API server's `userInfo`); login links are
built by `loginURL` in [frontend/src/api/session.ts](frontend/src/api/session.ts).

Logout is krm-foyer's `POST /auth/logout`. A Room Pass participant is then sent to
`/join`, where Room Pass's own "Sign out of this browser" form ends the enrolment.
Neither revokes an issued token, and stopping a Room blocks new enrollment, not
existing tokens.

Room Pass is an **authenticating proxy for Dex**, not an OAuth authorization
server or standalone OpenID Provider. Dex's authproxy connector consumes a trusted
upstream identity; Dex handles the application's OAuth/OIDC exchange. See
[Dex authproxy](https://dexidp.io/docs/connectors/authproxy/) and the distinction between
OAuth authorization and OIDC authentication in the
[OpenID Foundation introduction](https://openid.net/developers/how-connect-works/).
Its product name stays **Room Pass**, “room-code sign-in for applications, powered by
Dex”: applications using it need no CoffeeConfig or Kubernetes client, though Room
Pass itself uses Kubernetes storage. It keeps its API group, object UIDs, cookie keys,
connector ID and subject mapping across releases, so an upgrade never quietly creates
new identities.

The deployed issuer combines `github`, `room-pass` and `linkedin`. Platform
containment derives usernames from Dex's `federated_claims.connector_id` (`demo:`,
`github:`, `linkedin:`) and restricts room identities to demo groups. Display names and
synthetic email are attribution, not verified personal identity. See
[authorization.md](docs/authorization.md) for intended grants; additive platform
bindings, including `system:authenticated`, still need auditing.

Room Pass strips untrusted identity headers before proxying, accepts only configured
return URLs and uses a browser-bound single-use handoff. Every callback alias must
remain protected, with no bypass route to the authproxy assertion endpoint; the edge
strips `X-Remote-*` as well, and a NetworkPolicy keeps anything from dialing Dex
directly. See [handoff.md](https://github.com/sunib/room-pass/blob/main/docs/handoff.md)
for the protocol and
[csp-form-action.md](https://github.com/sunib/room-pass/blob/main/docs/csp-form-action.md)
for browser redirect constraints.

### Joining by QR code

The presenter's QR code encodes `/join-room?code=<current>`. Voter puts the code in
Room Pass's plain `__Host-room-pass-joincode` cookie and redirects to
`/auth/login?return_to=%2F&oidc.connector_id=room-pass`, a fixed target, so the
endpoint cannot be turned into an open redirect. Room Pass's join page then asks only
for a display name ([voter/join_room.go](voter/join_room.go)).

The cookie is a **Room Pass feature**, not a Voter one: Room Pass owns its name,
accepted values and attributes, and Voter is one consumer. It exists because the join
URL is built by Room Pass after Dex from a handoff id nobody else sees, and it is
delivered only because Room Pass's `/join` and Voter share one host. It is untrusted by
construction: it becomes a prefill and is checked against the Room's valid codes
through the ordinary POST, so a forged one is a wrong code and a stale one an expired
code. Voter vouches for nothing it carries and never signs it. The typed path stays for
anyone who cannot scan. The contract is in
[qr-join.md](https://github.com/sunib/room-pass/blob/main/docs/qr-join.md).

A rejected code or name re-renders the join form with the reason on it and the offending
field marked, rather than a dead-end error page: the code an audience mistypes is the
most common failure of the whole demo, and the back button loses both the typed name and
the single-use CSRF token, which is minted per render.

## Writes are the person's own

Every change the SPA makes is a request through `/k8s`, with the person's own token,
carrying `?fieldManager=voter`. The API server's answer -- a 403, a 409, an admission
refusal -- is what the page shows. Nothing falls back to Voter's ServiceAccount, which
is why a ballot or a menu edit reaches Git authored by the human.

### What may I do

The badge in the top bar links to `/me`, which shows the session and a permission grid.
The grid is a `SelfSubjectRulesReview` posted to
`/k8s/apis/authorization.k8s.io/v1/selfsubjectrulesreviews` and flattened in the
browser ([frontend/src/api/authz.ts](frontend/src/api/authz.ts)). Every authenticated
identity may ask -- `system:basic-user` grants it -- and it reports RBAC and only RBAC.
Admission is invisible to it, as it is to `kubectl auth can-i`; see
[talk-checklist.md](docs/talk-checklist.md).

It is not a permission model. Screens use it to EXPLAIN a refusal in advance, never to
gate the attempt: the buttons stay live and the API server produces the real 403.
Where the two disagree, the API server is right. Cookie custody does not make XSS
harmless either: same-origin script can act as the user without reading a token, and
the operator signs in as `cluster-admin` ([decision 2](docs/krm-foyer-migration.md#decision-2-cluster-admin-in-the-browser-accepted-for-now)).

### The operator page

`/room` is the presenter's own screen: the Room's rotating join code as a QR,
open/close for each round, and a switch that widens what the audience may do. It holds
no admin check. It opens the same streams any signed-in browser may ask for and renders
what Kubernetes is willing to send; a participant's RBAC grants nothing on rooms, so a
participant sees a refusal and no code. Opening or closing a round is a merge patch of
the QuizSession's `spec.state` with the caller's own token, so a participant's refusal
there is the API server's own.

The audience switch creates or deletes RoleBinding `voter-audience-coffee-admin`, with
the operator's own token, binding the static Role of that name -- which lives in Git --
to the group in `Room.spec.audienceGroup`. Reading the binding needs `get` on
`rolebindings`, which the audience does not hold, so a participant never sees the
switch, and Kubernetes decided that. The **binding is deliberately not in the Flux
kustomization**: Flux would recreate whatever the switch deletes, and the grant would
stop being revocable. gitops-reverser mirrors it to the audit trail instead, so widening
what a room may do arrives in Git as a reviewable diff authored by whoever did it.
Every phone discovers the change by polling its rules review; nothing is pushed and no
session is re-established.

`/admin` stays reachable by everyone on purpose: the audience may read the CoffeeConfig
and watch it change, and may not save until the operator grants it. The page explains
the gap from the rules review and still lets the save be attempted.

### Admission

Kubernetes now enforces what Voter's handlers used to. Both policies live in
[voter/config/admission/](voter/config/admission/) and are copied into the GitOps
repository. Both match people only (not `system:` users), so Flux, gitops-reverser and
controllers are untouched.

- **`voter-ballot`** reads every QuizSession in the namespace as a parameter. A ballot
  from anyone but a `demo:` (Room Pass) user must carry the label
  `voter.configbutler.ai/cast-by: operator`. A participant's ballot must be named
  `<round>-<lowerAscii(display name)>`, labelled with its round and submitter, and
  pinned to the round's UID and questions digest. The round must be `live`, and the
  pins must match: "This round is not open for voting.", "The round changed. Reload the
  questions before voting." The operator's interlude ballots
  ([voter/config/demo1-b.yaml](voter/config/demo1-b.yaml)) are declared: "I can still
  stuff the ballot box, I just cannot do it quietly." Known gap: a ballot naming a round
  that does not exist passes, and nothing counts it. Design in
  [docs/quiz-admission.md](docs/quiz-admission.md).
- **`voter-editable-spec`**: on CoffeeConfigs and Databases a person may change only
  `spec` -- no labels, annotations, finalizers or ownerReferences -- except, on a
  Database, the intent annotation and `kubectl apply`'s last-applied one, because the
  talk applies a Database request from a terminal. A person's `kubectl apply` of a
  CoffeeConfig is refused.

So one refusal in the demo that used to be the application's is now the cluster's.

### RBAC

Participants (`demo:voter-audience`) may read coffeeconfigs; get/list/watch
quizsessions; **get and create** quizsubmissions (no list or watch); get, list, watch,
create, patch and update databases; and create/get/list/watch commitrequests. Editing
the menu needs the operator's live grant (patch and update on coffeeconfigs).

Voter's ServiceAccount may get `coffeeconfigs/demo-coffee` for storefront prices,
get/list/watch quizsessions, list/watch quizsubmissions, and get/patch
`quizsessions/status` for the tally. Nothing else. Any signed-in person can see the
storefront now, since Voter reads the menu as itself.

## Live streams

The SPA uses `@configbutler/krm-stream` 0.10.0 (`connectResourceStream`) against
krm-foyer's `/stream/v1`. krm-foyer keeps one API-server watch per scope for the whole
room, as `krm-foyer-voter-shared`, which may list/watch coffeeconfigs, quizsessions,
databases, commitrequests and rooms and create SubjectAccessReviews. Each subscriber is
checked with SARs before anything is disclosed and rechecked while it stays connected,
so RBAC decides per person even though the watch is shared. Shared-watch audit events
name that ServiceAccount; writes keep the person's name. Sharing is per krm-foyer
process; more replicas mean more upstream watches. Revocation timing is in
[authorization.md](docs/authorization.md#revocation-timing-and-the-cached-subject).

The fixture rehearsal (`task voter:voteload`) put 200 participants through in 60 s:
200 of 200 signed in, streamed and voted, with 197 concurrent streams on one shared
watch.

## Live editing and save consistency

The storefront uses a read-only store. The coffee and Database editors render the
library's draft, derived changes and conflicts directly, and every edit goes through
store APIs. A stream snapshot is the sole initialization path. Products and vouchers
use atomic array reconciliation, and existing SKU/code inputs are read-only;
RFC 7386 writes arrays whole.

Three-way merge compares previous server state, local draft and incoming server state.
It protects typing from incoming events. It cannot prevent an unseen concurrent write
between the last event and a save; optimistic concurrency closes that separate race.

A save calls `store.captureSave(uid)` and sends a merge patch through `/k8s` with
`metadata.uid` and `metadata.resourceVersion` as preconditions
([frontend/src/api/kube.ts](frontend/src/api/kube.ts)), so a replaced or moved-on object
is a real 409. Only `spec` is sent; admission holds the same line for anyone writing
by hand. A Database's intent note rides in the same patch as the
`platform.configbutler.ai/intent` annotation. On a 409 the editor re-reads; if the
winning edit did not touch the same fields it re-sends, at most three times, and a
genuine field conflict still stops and shows its markers. On 2026-09-17 the room
landed twenty-six saves in six minutes, three pairs within the same second, so a bare
collision is the ordinary case on stage.

After the patch the browser creates the CommitRequest (`configbutler.ai/v1alpha3`) as
the person. Those are two Kubernetes writes, so a failed commit request is reported as
partial success rather than an error. The watch echo updates the store, and one guarded
read recovers a missing echo. A failed stream never masquerades as live: session
expiry, disconnect, forbidden access and resource deletion are explicit states, and
deletion prunes the draft rather than carrying it to a replacement UID.

## Coffee and Git responsibilities

Voter serves the SPA and backend in one image. Pricing and voucher enforcement remain
server-authoritative. Changing `maximumUsage` affects subsequent orders.

Orders are deliberately NOT Kubernetes objects, and the demo says so out loud on
`/admin/orders`: they are an append-only ring buffer in the process, fanned out over
hand-written SSE. An order is an event, not configuration -- nothing reviews it,
reconciles toward it, or would benefit from a commit per coffee. The feed carries
display names only, because every signed-in participant can read it.

Redemptions and orders are both in process memory: a restart resets counts and
forgets the feed, and a second replica would enforce a different tally. Keep one
replica until shared atomic persistence exists.

ConfigButler owns durable Git history and commit completion. The UI needs three
separate facts: Kubernetes saved, CommitRequest accepted, Git commit observed.
gitops-reverser mirrors the namespace through four GitTargets into the private
`ConfigButler/k8s-trail`; a participant's vote and an operator's RoleBinding commit
with the human as Author and the bot as Committer. The editor follows its
CommitRequest's status to the commit it became
([frontend/src/api/commitStatus.ts](frontend/src/api/commitStatus.ts)), linked through
`commitURLTemplate`.

## Voting rounds

Voting and coffee are peers in the SPA: `/` shows the signed-in identity, lists the
rounds and links to the coffee bar at `/coffee`. Rounds are QuizSessions read live from
the `quizsessions` stream.

A vote is a QuizSubmission the participant creates through `/k8s`
([frontend/src/api/ballot.ts](frontend/src/api/ballot.ts)). It is named
`<round>-<lowerAscii(displayName)>`, labelled `voter.configbutler.ai/round` and
`/submitter`, and pins `spec.roundUID` and `spec.questionsDigest` from the round's
`status.questionsDigest`, which Voter's reconciler publishes; on a brand-new round the
page waits up to 8 s for it. "Already voted" is a GET of the participant's own ballot
name, and a second create is a 409 AlreadyExists: "You have already voted". The browser
checks answers first for immediate feedback; that copy is the courtesy, not the
control. Admission holds who, what name, which round and whether it is live.

QuizSubmissions are create-only for participants: their RBAC has no update, patch or
delete. This is not a CRD-wide immutability guarantee against identities with broader
grants. Reopening a round preserves votes; create a new named round for another vote.

A round's result is a field on the round. [voter/quiz_reconciler.go](voter/quiz_reconciler.go)
runs in the Voter process as the ServiceAccount, watches QuizSubmissions, and patches
`QuizSession.status` with the tally -- filed, counted, per-question counts, sums, the
latest 25 free-text answers with their total, and `lastTallyTime` so a stale tally
cannot look live. A ballot counts only if its pins match the round and its answers pass
`validateQuizAnswers` ([voter/quiz_tally.go](voter/quiz_tally.go)). It writes the
`status` subresource only, so it can publish the result without being able to edit the
questions. Because it watches the API rather than a write path, a ballot written with
`kubectl` moves the tally exactly as one typed on a phone does.

The results screen reads that status off the stream it already has open; there is no
results endpoint. Just after a vote the page says "Counting your vote…" for the second
the tally takes.

A ballot is selected by `spec.sessionRef`, which the CRD requires. The round label is
gitops-reverser's filing key; admission requires it on a participant's ballot, but the
tally does not count by it. Status never reaches Git: gitops-reverser rebuilds each
mirrored document from an allowlist that has no `status` on it. See
[docs/live-results-design.md](docs/live-results-design.md).

Round definitions come from GitOps; see [voter/config/demo-round.yaml](voter/config/demo-round.yaml).
Editing the questions after voting starts changes the digest: new ballots for the old
questions are refused, and existing ones stop counting.

This is an audience demo, not an anonymous voting or election system. The UI explains
that answers are shared, and they are committed to Git under the voter's name. One
enrollment is one identity, not proof of one physical person.

## Release and deployment boundaries

The live deployment is owned by Flux in the private `ConfigButler/k8s` repository:
`external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/`. Flux image automation commits
Voter's and Room Pass's new 2.x images to Git on release; a new major is deployed by
hand. See [AGENTS.md](AGENTS.md) and [docs/ci.md](docs/ci.md#releases). Roll back by reverting the Git change. Do not mutate
live workloads with kubectl; disposable fixtures use explicit local kubeconfigs.

**Three objects are exceptions, and editing them in Git does nothing.**
`CoffeeConfig/demo-coffee` and both `QuizSession`s carry
`kustomize.toolkit.fluxcd.io/ssa: IfNotPresent`, so Flux creates them once and
never applies them again -- otherwise the room's voucher edit would be reverted
within the reconcile interval, and a round opened on stage would close itself.
Re-seeding means deleting the object. The runtime RoleBinding behind the audience
switch is a fourth exception in the other direction: not in the kustomization at
all, because Flux would recreate it. Both are explained in the runbook's "Two
repositories".

## Current release

| | |
| --- | --- |
| Voter | `ghcr.io/sunib/voter:2.0.0@sha256:5e9f79b7…`, commit `b446793` |
| krm-foyer | `0.3.0@sha256:6b620d65…` |
| Room Pass | `2.2.0@sha256:6571a631…` |
| GitOps | `ConfigButler/k8s` commit `2ebda2e` |

The rounds are `demo1` and `evaluation`, both seeded by GitOps and then left alone.

Evidence: on the k3d fixture krm-foyer runs beside Voter behind the same edge, with an
operator signed in through Dex's mock callback under the `github` id, and the browser
specs in [test/browser/](test/browser/) pass -- voting, operator, live-stream,
join-and-logout and boundaries. The production smoke after cutover covered a QR join
from another origin, session and whoami as a `demo:` user, rounds and menu streaming,
and the storefront through the identity check.

Still open: phone QR login in Safari; the operator's GitHub login and `/auth/whoami` on
production; a real vote and an operator save appearing in `k8s-trail`; the Vite dev loop
against the fixture; and an envtest suite for the admission policies in CI. The Room's
menu-editing grant has been on since 2026-09-17; turn it off on `/room` before a talk.
