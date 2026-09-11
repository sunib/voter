# Who may do what?

Reviewed 2026-09-10 against application source and the local external platform
checkout. These are configuration findings, not a fresh audit of the live cluster.

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
| Ordinary `linkedin:<email>` | LinkedIn connector is open | No named grant by default; shared authenticated-user grants still apply |
| Ordinary `github:<email>` | Currently restricted to `koudijs-dev` organization | Matching user/group grants only; no automatic demo membership |
| `github:simonkoudijs@gmail.com` | GitHub connector | Named cluster-admin and Flux Web admin |
| `linkedin:simon@configbutler.ai` | LinkedIn connector | Named cluster-admin and Flux Web admin |

Source files in the external platform checkout:
`2-gitops/voter-demo/participant-rbac.yaml`,
`2-gitops/auth/rbac/humans-rbac.yaml`, and
`1-talos/templates/_authentication-config.tpl`.
The named LinkedIn grant is broader than just Flux UI access. Bare-email operator
subjects and legacy connector acceptance also remain as migration leftovers.

The demo Role grants these operations throughout the `voter` namespace:

| Resource | Verbs |
| --- | --- |
| CoffeeConfigs | get, list, watch, patch, update |
| CommitRequests | create, get, list, watch |
| QuizSessions | get, list, watch |
| QuizSubmissions | create, get, list, watch |

QuizSubmissions are create-only for participants: this Role grants neither update
nor patch on them, and Voter exposes no editing endpoint. Broader additive grants or
administrator access are separate; the CRD does not enforce immutable spec fields.

This Role does not grant Secrets, RBAC changes, impersonation or deletion. It is
not limited to one named CoffeeConfig and it permits reading other submissions.
Voter's current handler addresses one configured CoffeeConfig, but callers with
tokens can call Kubernetes directly within their RBAC grants.

Opening GitHub login to everybody is compatible with granting only the owner
extra rights. It is not implemented in this pass. First verify all clients on the
shared issuer: oauth2-proxy currently uses a broad gmail domain gate; Grafana and
Flux Web have their own rules. No matching explicit grant does not mean literally
zero Kubernetes access: audit shared bindings such as `system:authenticated`.

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

## Shared-stream implementation awaiting release

CoffeeConfig streams use one service-account backend per process. Voter resolves the
subscriber's full Kubernetes identity with their token at stream opening and applies
`SubjectAccessReviewAuthorizer` before cache disclosure and every 30 seconds. Initial
and periodic checks have five-second deadlines, as do individual SSE writes/flushes.
Session/token expiry cancels only that subscription. Failed identity resolution,
denied list/watch, and access-review errors fail closed.

The service account gets named CoffeeConfig reads and SAR creation only. Direct REST
reads, CoffeeConfig PATCH and CommitRequest creation retain participant credentials;
audit watch events now identify the service account while writes identify the person.
The fixture and platform manifests carry the same narrow grants. Production remains
on the release recorded in PLAN until GitOps promotion. See [verification](shared-streams.md).

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
