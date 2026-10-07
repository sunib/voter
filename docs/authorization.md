# Who may do what?

Reviewed 2026-09-10 against application source and the local external platform
checkout, and the platform rows re-checked against the **live cluster on
2026-09-11** with `kubectl auth can-i --as`. Impersonation establishes what a
username/group combination may do; it does not prove a real provider token
carries those claims. The application rows remain configuration findings.
The Voter rows were rewritten for 2.0.0 (2026-10-06), when Voter moved behind
[krm-foyer](krm-foyer-migration.md): the browser now writes to Kubernetes itself,
as the person, and admission holds the rules Voter's handlers used to.

## Three separate questions

1. **Can I log in?** Room Pass checks room enrollment; GitHub/LinkedIn establish
   provider identities through Dex.
2. **Who does Kubernetes see?** The authenticator maps Dex's connector claim into
   a distinct username prefix and validates groups.
3. **May I do this operation now?** RBAC grants verbs/resources/namespaces.
   Admission must enforce any additional object or lifecycle restrictions.

An application route named `admin` grants nothing. Neither does choosing GitHub
in the login URL. A successful Dex login can still produce a Kubernetes 403.

## Checked-in platform policy

| Identity | Login eligibility | Explicit application/platform grants |
| --- | --- | --- |
| No krm-foyer session | No session | krm-foyer's 401 on `/k8s`, `/stream` and every `/public/` route; only the SPA files, `/config.json` and `/join-room` are served |
| Room attendee, `demo:<sub>` in `demo:voter-audience` | Valid room code/enrollment and active Room at handoff | Demo Role in `voter` |
| Ordinary `linkedin:<email>` | LinkedIn connector is open to any LinkedIn account | Verified live: nothing beyond `system:basic-user` and discovery. Secrets, pods, namespaces, Rooms and nodes all denied |
| Ordinary `github:<email>` | Restricted to the `koudijs-dev` organization | Matching user/group grants only; no automatic demo membership |
| `koudijs-dev:the-specific-group` (cohort) | GitHub connector, org team | Deployer Role in `simon`: pods, Services, Ingresses, IngressRoutes, middlewares. The group is a placeholder no real team maps to, so this is latent, not reachable |
| `github-actions:ConfigButler/k8s` (CI) | GitHub Actions OIDC, repository claim only | `get`/`list` on nodes. The former Flux patch grant was removed 2026-09-11 |
| `github:simonkoudijs@gmail.com` | GitHub connector | Named cluster-admin and Flux Web admin |
| `linkedin:simon@configbutler.ai` | LinkedIn connector | Named cluster-admin and Flux Web admin |

Source files in the external platform checkout:
`2-gitops/voter-demo/participant-rbac.yaml`,
`2-gitops/auth/rbac/humans-rbac.yaml`, and
`1-talos/templates/_authentication-config.tpl`.
The named LinkedIn grant is broader than just Flux UI access. The pre-migration
bare-email operator subjects are **gone** as of 2026-09-11; both named operator
bindings now list only the prefixed `github:` and `linkedin:` usernames.

**Logging in is not the same gate everywhere.** Kubernetes is the strict one: the
connector prefix decides the username namespace and an unbound prefix gets
nothing. The web front ends each decide for themselves, so audit them separately:

| Gate | Who gets in |
| --- | --- |
| Kubernetes API | Prefixed username must match a binding; ordinary LinkedIn and GitHub identities match none |
| Grafana | `role_attribute_strict` with a single Admin mapping for the owner's email; everyone else is denied rather than silently a Viewer |
| oauth2-proxy (Prometheus, Alertmanager, podinfo, `p<N>` participant apps) | An explicit two-address allowlist as of 2026-09-11 |

oauth2-proxy previously gated on `email_domains = ["gmail.com"]`, which any
LinkedIn account with a verified Gmail address satisfied. It now uses
`authenticatedEmailsFile`; the domain list is emptied, because oauth2-proxy ORs
the two and the chart default `["*"]` means allow-all. Group membership could not
express this: no `koudijs-dev` team means "operator" — they are all cohorts — and
the LinkedIn connector emits no groups, which would have locked out
`linkedin:simon@configbutler.ai`. Cohort members must be added to that allowlist
to reach their own `p<N>` app URL.

The demo Role (`voter-audience`, bound to `demo:voter-audience`) grants these
operations throughout the `voter` namespace:

| Resource | Verbs |
| --- | --- |
| CoffeeConfigs | get, list, watch |
| CommitRequests | create, get, list, watch |
| Databases | get, list, watch, create, patch, update |
| QuizSessions | get, list, watch |
| QuizSubmissions | get, create |

There is no list or watch on QuizSubmissions: no page needs them, and they would
hand everyone everybody's answers. The vote page `get`s its own ballot's name to
ask "have I voted?"; results are the round's `status`, which Voter's tally
reconciler writes.

**CoffeeConfigs became read-only here on 2026-09-15.** `patch` and `update` moved
to a second Role, `voter-audience-coffee-admin`, which exists in Git but is
**not bound**. A participant can therefore open the menu editor, read the menu,
watch it change — and be refused by the API server on save. That refusal is the
starting state of the demo, not a fault.

The operator hands the grant out live from `/room`: a switch there creates a
RoleBinding of that Role to the same group (named in `Room.spec.audienceGroup`),
from the operator's browser through krm-foyer's `/k8s`, with the operator's own
token, and deletes it again on the way back. The binding is deliberately absent from the
Flux kustomization — Flux would recreate whatever the switch deletes — and
`gitops-reverser` mirrors it into the audit trail, so the grant is recorded in
Git as a commit authored by whoever pulled the switch.

Two consequences worth stating plainly:

- The grant is to the **group**, so it reaches every enrolled participant at
  once. There is no per-person coffee permission and none is intended.
- While bound, any participant may `patch` the CoffeeConfig. They still cannot
  `create` or `delete` it: the object belongs to GitOps and they only amend it.

Every identity may ask what it holds. `selfsubjectrulesreviews` is granted to
`system:authenticated` by the stock `system:basic-user` ClusterRole, so the
browser can POST a SelfSubjectRulesReview through `/k8s` for any participant —
no RBAC change was needed to render the permission table on `/me`. The table is the API server's answer about the caller's own token; the
application does not compute it and cannot disagree with it.

The CommitRequest CRD was absent when this was first written, leaving that row
dormant. It was installed 2026-09-11 and gitops-reverser is running, so the grant
is **live**: `create commitrequests.configbutler.ai -n voter` now returns `yes`
for an audience identity.

QuizSubmissions are create-only for participants: this Role grants neither update
nor patch on them. Broader additive grants or administrator access are separate;
the CRD does not enforce immutable spec fields.

Neither Role grants Secrets, RBAC changes, impersonation or deletion
(`delete quizsubmissions` is denied). Participants cannot read RoleBindings
either, which is why the grant switch simply does not appear on a participant's
`/room` — Kubernetes withholds it, not the page. Neither Role is limited to one
named CoffeeConfig. A participant can `get` another ballot whose name they guess,
but cannot list them.

### Admission: the rules RBAC cannot say

RBAC decides verbs on resources; it cannot say "only your own ballot" or "only
the spec". Two ValidatingAdmissionPolicies in `voter/config/admission/` (copied to
`2-gitops/voter-demo/admission/`) say it, for people only — `system:` users and
service accounts (Flux, gitops-reverser, Voter's reconciler) are not matched:

- **`voter-ballot`**, on every QuizSubmission create, with every QuizSession in
  the namespace as a parameter. A ballot from anyone but a `demo:` (Room Pass)
  user must carry the label `voter.configbutler.ai/cast-by: operator`. A
  participant's ballot must be named `<round>-<display name, lower case>`, carry
  the `voter.configbutler.ai/round` and `/submitter` labels, and pin the round's
  `uid` and `status.questionsDigest`. The round must be `live`, and any pin must
  match. The display name comes from the token's display-name extra, which no
  request can set for itself, so the fixed name is also the one-ballot-per-person
  rule: a second create is the API server's 409.
- **`voter-editable-spec`**, on CoffeeConfig updates and Database creates and
  updates: a person may change `spec` and nothing else — no labels,
  annotations, finalizers or ownerReferences — except, on a Database, the
  `platform.configbutler.ai/intent` annotation and `kubectl apply`'s
  last-applied annotation. A person's `kubectl apply` of a CoffeeConfig is
  therefore refused.

A third policy is not about who: **`voter-coffee-price`** refuses any
CoffeeConfig create or update that sets a product over €10 (`priceCents` above
1000; a price already stored stays until changed), from anyone, Flux included, as a 422 with "Nobody pays more than €10 for
a coffee: <product>." Whether a person may change the menu is RBAC's question;
whether the change makes sense is this one's.

**There is still no bound on how much an audience token may write.** The `voter`
namespace has no ResourceQuota and no LimitRange. One ballot per person per round
is enforced; Database requests are not counted. A ballot that names a round
which does not exist passes `voter-ballot` vacuously — nothing counts it, since
the tally only counts ballots for rounds it tallies. `simon` has a
`participant-quota`; `voter` does not.

The pages address one configured CoffeeConfig, but a person's session can send
anything through `/k8s` within their RBAC grants and these policies.

Opening GitHub login to everybody is compatible with granting only the owner
extra rights. It is not implemented in this pass. It now depends only on the Dex
connector's `orgs` restriction, since the three front-end gates above were each
narrowed to identity. No matching explicit grant does not mean literally zero
Kubernetes access, so shared bindings were audited: the only ClusterRoleBindings
naming `system:authenticated` are the stock `system:basic-user`,
`system:discovery` and `system:public-info-viewer`, which is what an unbound
prefix resolves to.

## Situations and expected behavior

| Situation | Result |
| --- | --- |
| Missing, forged or expired krm-foyer session | krm-foyer's 401; nothing reaches Kubernetes or Voter's `/public/` routes |
| Valid session, missing/wrong CSRF on a write | krm-foyer's 403 before contacting Kubernetes |
| Forged `Krm-Foyer-Identity` header | Signed out: krm-foyer's 401. Signed in: replaced by krm-foyer's own at the ForwardAuth |
| Room Pass form with `Origin: null` | Room Pass accepts with matching signed-cookie proof |
| Kubernetes rejects credentials | 401, passed back to the page as the API server sent it |
| RBAC or admission denies | 403 with the API server's reason, which for a policy is its own message ("This round is not open for voting.") |
| Second ballot for the same round | 409 AlreadyExists; the page says "You have already voted" |
| Config patch on a stale `resourceVersion` | 409; the editor re-sends non-overlapping edits up to three times |
| Config patch succeeds, CommitRequest denied/expired | Saved config, explicit partial-success message |
| Room stopped/expired or Participant invalid | Room Pass rejects new identity handoff |
| Room stopped after a Dex token was issued | Token remains valid until expiry; Room stop is not token revocation |
| Logout | krm-foyer's `POST /auth/logout` ends its session; a participant is then sent to Room Pass's `/join` to end enrollment. Neither revokes the Dex token |

Removing a RoleBinding removes that grant from existing tokens too; other
matching bindings may still grant access. Existing krm-foyer sessions do not
recheck Room state on each operation. If immediate room-wide shutdown is required, design
and test an authorization mechanism for that explicitly.

## Automated evidence

| Test file | What it establishes |
| --- | --- |
| [Boundaries in the browser](../test/browser/boundaries.spec.js) | From a real participant session, through `/k8s`: a ballot in someone else's name and an unpinned ballot are refused with `voter-ballot`'s messages, the participant's own ballot lands once and the second is a 409, a label on the menu is refused by `voter-editable-spec` even while the menu grant is on |
| [The editor in the browser](../test/browser/live-stream.spec.js) | With the menu grant on, a €12.50 coffee saved from the editor is refused by `voter-coffee-price`, once, with the policy's sentence in the page and nothing stored |
| [The operator in the browser](../test/browser/operator.spec.js) | A participant is refused the operator page and never sees a join code; the operator (Dex `github` id, cluster-admin) sees the code, opens and closes a round, and grants and revokes the menu |
| [Voter in the browser](../test/browser/) | Real Chromium through Traefik, krm-foyer, Dex and a released Room Pass: QR join and logout, voting, live streams; CI retains video |
| [Voter's identity check](../voter/participant_storefront_test.go) | `/public/` routes refuse a request without `Krm-Foyer-Identity` |

Room Pass's own evidence lives with Room Pass, in
[sunib/room-pass](https://github.com/sunib/room-pass), and runs in that
project's CI, not this one's:

| Test file | What it establishes |
| --- | --- |
| [Room Pass server](https://github.com/sunib/room-pass/blob/main/internal/server/server_test.go) | Enrollment lifecycle, stopped/expired Room, tampered cookie, handoff replay and CSRF |
| [Room Pass API](https://github.com/sunib/room-pass/blob/main/test/integration/api_test.go) | CRD API behavior using envtest |
| [Dex network boundary](https://github.com/sunib/room-pass/blob/main/test/network/network_test.go) | Real Dex, allowed/denied pod matrix, Service and Pod IP, policy removal/restoration |
| [Browser login](https://github.com/sunib/room-pass/blob/main/test/browser/room-auth.spec.js) | Chromium enrollment/return flow, invalid code, CSRF, closed enrollment and cookie flags |
| [Room Pass e2e](https://github.com/sunib/room-pass/blob/main/test/e2e/e2e_test.go) | Local Dex/Kubernetes fixture with a minimal OIDC client |

Here, run `task voter:test`, and `task e2e-up && task test-browser` for the browser layer.
For concurrent credential checks run `cd voter && go test -race ./...`.

The browser specs run against the fixture's copy of the RBAC and admission
policies, not against production. Still owed: an envtest suite for the admission
policies in CI, real-token RBAC tests for all three connectors, and audit-to-Git
acceptance on the cluster.

See [network suite details](https://github.com/sunib/room-pass/blob/main/test/network/README.md) for testing the
actual platform policy file and the distinction between local k3s and live Cilium.

## Shared streams

Live views read through krm-foyer's `/stream/v1`, not through Voter. krm-foyer
holds one API-server watch per scope for the room, as its own shared identity
(`krm-foyer-voter-shared`: list and watch on CoffeeConfigs, QuizSessions,
Databases, CommitRequests and Rooms, plus SubjectAccessReview create). Before it
discloses anything to a subscriber it asks a SubjectAccessReview for that
person's own identity, and it asks again periodically while the stream is open;
a denied or failed check closes that subscription. The load rehearsal held 197
concurrent streams on one shared watch. The design before 2.0.0, with Voter as
the gateway, is in [shared-streams.md](shared-streams.md).

Writes never go through the shared identity: a vote, a menu save, a Database
request and a CommitRequest are the person's own request through `/k8s`, so
audit events — and gitops-reverser's commits — name the person. Voter's own
ServiceAccount no longer streams anything; its grants are the tally reconciler's
and `get` on `coffeeconfigs/demo-coffee` for the storefront.

### Revocation timing and the cached subject

The shared watch is authorized as krm-foyer's identity; each subscriber is held
to their own RBAC only by the SubjectAccessReviews. Periodic checks reevaluate
**RBAC for the subject captured when the stream opened**, so a removed
RoleBinding is detected on a later check. The interval is krm-foyer's setting;
it has not been re-measured on this cluster since the cutover.

An IdP group-membership change or account disablement does not rewrite already-issued
token claims, and the periodic SAR does not re-resolve the subject or contact Dex.
Such identity changes are therefore **not covered by the RBAC recheck**.
Existing streams are bounded by the earlier session/token expiry. A new
stream resolves identity again, but the same still-valid token may retain old claims.
Do not describe a periodic RBAC check as universal identity revocation.
