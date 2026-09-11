# Who may do what?

Reviewed 2026-09-10 against application source and the local external platform
checkout, and the platform rows re-checked against the **live cluster on
2026-09-11** with `kubectl auth can-i --as`. Impersonation establishes what a
username/group combination may do; it does not prove a real provider token
carries those claims. The application rows remain configuration findings.

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
| No valid app cookie | No app session | CoffeeConfig handler returns 401 |
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

The demo Role grants these operations throughout the `voter` namespace:

| Resource | Verbs |
| --- | --- |
| CoffeeConfigs | get, list, watch, patch, update |
| CommitRequests | create, get, list, watch |
| QuizSessions | get, list, watch |
| QuizSubmissions | create, get, list, watch |

The CommitRequest CRD was absent when this was first written, leaving that row
dormant. It was installed 2026-09-11 and gitops-reverser is running, so the grant
is **live**: `create commitrequests.configbutler.ai -n voter` now returns `yes`
for an audience identity.

QuizSubmissions are create-only for participants: this Role grants neither update
nor patch on them, and Voter exposes no editing endpoint. Broader additive grants or
administrator access are separate; the CRD does not enforce immutable spec fields.

This Role does not grant Secrets, RBAC changes, impersonation or deletion
(`delete quizsubmissions` is denied). It is not limited to one named CoffeeConfig
and it permits reading other submissions.

**There is no bound on how much an audience token may write.** The `voter`
namespace has no ResourceQuota and no LimitRange, and the cluster has no
ValidatingAdmissionPolicy at all, so nothing limits QuizSubmission object count
or checks that a submission refers to a real session. `simon` has a
`participant-quota`; `voter` does not.

Voter's current handler addresses one configured CoffeeConfig, but callers with
tokens can call Kubernetes directly within their RBAC grants.

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
| Missing, forged, legacy or expired app session | 401; no participant operation |
| Valid app session, missing/wrong CSRF on mutation | 403 before contacting Kubernetes |
| Valid CSRF, foreign Origin | 403 before contacting Kubernetes |
| Valid CSRF, absent Origin | Voter permits the request to reach authorization |
| Valid CSRF, `Origin: null` | Voter rejects; Room Pass's form accepts with matching signed-cookie proof |
| Kubernetes rejects credentials | 401, no server-identity retry |
| Kubernetes denies permission | 403, no server-identity retry |
| Config patch succeeds, CommitRequest denied/expired | Saved config, `committed: false`, explicit partial-success message |
| Room stopped/expired or Participant invalid | Room Pass rejects new identity handoff |
| Room stopped after a Dex token was issued | Token remains valid until expiry; Room stop is not token revocation |
| App logout | Clears app cookie; does not revoke Dex token or Room Pass enrollment |

Removing a RoleBinding removes that grant from existing tokens too; other
matching bindings may still grant access. Existing app sessions do not recheck
Room state on each operation. If immediate room-wide shutdown is required, design
and test an authorization mechanism for that explicitly.

## Automated evidence

| Test file | What it establishes |
| --- | --- |
| [Voter authorization](../voter/authorization_test.go) | Real handler gates, request-scoped credentials, ignored forged headers, upstream 401/403/409, partial save |
| [Voter OIDC](../voter/oidc_test.go) | Cookie tampering/expiry/version, CSRF, return paths, callback browser binding/expiry/rejected-attempt replay |
| [Room Pass server](../room-pass/internal/server/server_test.go) | Enrollment lifecycle, stopped/expired Room, tampered cookie, handoff replay and CSRF |
| [Room Pass API](../room-pass/test/integration/api_test.go) | CRD API behavior using envtest |
| [Dex network boundary](../room-pass/test/network/network_test.go) | Real Dex, allowed/denied pod matrix, Service and Pod IP, policy removal/restoration; `task test-network` runs in CI |
| [Browser login](../room-pass/test/browser/room-auth.spec.js) | Chromium enrollment/return flow, invalid code, CSRF, closed enrollment and cookie flags; CI retains video |
| [Room Pass e2e](../room-pass/test/e2e/e2e_test.go) | Local Dex/Kubernetes fixture; separate from CI and the real app browser |

Run `task voter:test`, `task room-pass:test`, and `task test-integration`.
For concurrent credential checks run `cd voter && go test -race ./...`.

The HTTP upstream in Voter tests is a controlled stand-in: it establishes that
Voter preserves a decision, not that deployed RBAC makes the correct decision.
The next layer is rendered platform CEL and real-token RBAC tests for all three
connectors, followed by browser and audit-to-Git acceptance. The implementation
plan tracks those remaining proofs.

See [network suite details](../room-pass/test/network/README.md) for testing the
actual platform policy file and the distinction between local k3s and live Cilium.

## Shared streams

CoffeeConfig streams use one service-account backend per process. Voter resolves the
subscriber's full Kubernetes identity with their token at stream opening and applies
`SubjectAccessReviewAuthorizer` before cache disclosure and every 30 seconds. Initial
and periodic checks have five-second deadlines, as do individual SSE writes/flushes.
Session/token expiry cancels only that subscription. Failed identity resolution,
denied list/watch, and access-review errors fail closed.

The service account gets named CoffeeConfig reads and SAR creation only. Direct REST
reads, CoffeeConfig PATCH and CommitRequest creation retain participant credentials;
audit watch events now identify the service account while writes identify the person.
The fixture and platform manifests carry the same narrow grants. Deployed as `85de0c0`
through GitOps `2d770a1`; on Kubernetes 1.36.1 the service account is allowed named
`coffeeconfigs/demo-coffee` list and watch and denied everything else checked.
See [verification](shared-streams.md).

### Revocation timing and the cached subject

Voter now enforces cached stream disclosure using Kubernetes SAR verdicts; the API
server authorizes the shared watch as the service account. This differs from a
participant-token watch, where Kubernetes directly authorizes that person's watch.
Scope checks, trusted identity resolution and SARs must precede every cache disclosure.

Periodic checks reevaluate **RBAC for the subject captured when the stream opened**.
A removed RoleBinding is detected on a subsequent check (30-second interval,
five-second check timeout; the end-to-end target is 60 seconds including delivery
and scheduling). The local rehearsal observed approximately 30 seconds.

An IdP group-membership change or account disablement does not rewrite already-issued
token claims, and the periodic SAR does not re-resolve the subject or contact Dex.
Such identity changes are therefore **not covered by the RBAC recheck bound**.
Existing streams are bounded by the earlier application-session/token expiry. A new
stream resolves identity again, but the same still-valid token may retain old claims.
Do not describe a 30-second RBAC check as universal identity revocation.
