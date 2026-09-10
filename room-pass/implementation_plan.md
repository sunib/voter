# Application OIDC integration plan

Status: proposed implementation sequence, no runtime changes completed.
Written 2026-09-09. Architecture: [advised_architecture.md](advised_architecture.md).

## Outcome and scope

Voter/Coffee logs in through Dex and Room Pass using its backend as the OIDC client.
The browser holds an encrypted HttpOnly token cookie; participant Kubernetes
operations carry the participant's Dex ID token. No oauth2-proxy or session database
is added. Browser-selected identities, application join codes and impersonation are
retired after their replacements pass verification.

Implement in `auth-service/`, `frontend/` and the authoritative application manifests
in `k8s/`. These documents live in `room-pass/` at the user's request; Room Pass
runtime changes are not part of this improvement. Raise any discovered Room Pass
defect separately. Add application integration coverage alongside the existing local
fixture without treating the fixture's tiny client as proof the real app works.

Use small reviewable changes. Keep the current demo runnable during development,
but make legacy and OIDC authentication explicit, mutually exclusive deployment
modes during migration. OIDC mode must never fall back to legacy identity or
impersonation. Keep the legacy deployment closed to public traffic until retired.
Do not access an existing remote cluster or create `platform/` or `demo-state/`.

## 1. Verify the application surface and configuration contract

- Re-read the alignment brief and current code before editing. Record each finding
  as verified open, already fixed, or contradicted by code.
- Inventory every route in `auth-service/http_handlers.go` and
  `auth-service/coffee_handlers.go`, frontend Kubernetes requests, background work,
  watches, token caches and all ServiceAccount/impersonation users.
- Record method/path, authentication, CSRF requirement, Kubernetes resource/verb,
  proposed participant or server identity, and intended denial behavior per operation.
  Include quiz submission and operations that mutate only in-memory state.
- Define configuration for issuer, confidential client ID/secret, exact callback,
  application origin, allowed return paths, application cookie keys/lifetime and
  Kubernetes endpoint/TLS trust. Specify matching accepted audience and claim mapping
  as platform deployment inputs, without applying them to a remote cluster.
- Inspect the existing Go dependencies and choose maintained OIDC/OAuth libraries
  compatible with the module. Keep the existing cookie library unless a concrete
  incompatibility requires a change.

Acceptance: a complete route/credential inventory and explicit configuration contract
make it possible to account for every legacy path at removal time.

## 2. Implement backend OIDC login and encrypted token sessions

- Add login start, callback, session-info and CSRF-protected logout endpoints.
  Use authorization code with S256 PKCE, state/nonce verification, exact callback
  configuration and bounded single-use browser-bound transactions.
- Validate issuer, signature, audience, expiry, nonce and applicable authorized-party
  constraints before issuing a cookie. Bound discovery/exchange requests with timeouts.
- Introduce a versioned ID-token cookie payload and reject old self-asserted identity
  cookies in OIDC mode. Enforce expiry, flags and encoded-size limits from the advice.
- Load independent persisted application keys from a pre-created Secret. Remove
  runtime creation permission when the deployment supplies that Secret; validate key
  sizes at startup and fail closed on missing or unusable configuration.
- Expose only verified display identity and session metadata to the frontend. Add
  explicit CSRF protection for application mutations and redact credentials in logs.

Acceptance: tests cover successful login, state/nonce mismatch, transaction replay,
wrong issuer/audience/signature, expired token, forged or legacy cookie, unsafe return
URL, missing CSRF proof and oversized cookie. A session survives backend restart with
the same keys; an unfinished login can restart cleanly. Posting chosen identity
fields cannot establish or alter authenticated identity in OIDC mode.

## 3. Integrate the frontend login lifecycle

- Replace browser identity generation and `/public/login` identity submission with
  backend session discovery and top-level login navigation.
- Keep same-origin cookie requests and attach CSRF proof to mutations. Remove frontend
  token storage and identity-header assumptions where present; do not expose the token.
- Handle unauthenticated, login-failed, expired-session, forbidden and logout states
  distinctly. API authentication failures return structured 401 responses, not a
  redirect chain inside fetch calls. Kubernetes authorization failures remain 403.
- Preserve unsaved editor input across reauthentication where practical and require
  deliberate retry when a previous mutation's outcome is unknown. Reconnect streams
  after login and terminate authenticated streams when their session expires.

Acceptance: browser coverage proves login, logout and expiry/re-login with the same
enrolled subject; requests carry cookies and CSRF proof without exposing tokens to
JavaScript. No browser-supplied ID, email or group can influence server identity.

## 4. Forward participant tokens across the endpoint surface

- Add request-scoped participant Kubernetes clients with clean credentials and fixed
  destination/TLS configuration. Explicitly exclude ServiceAccount token files,
  certificates, exec/auth providers and impersonation. Keep server clients separate.
- Migrate CoffeeConfig writes and CommitRequests together, then every other inventory
  entry, including the existing quiz submission forwarding route. Remove any ability
  to inject identity through browser headers. Avoid a general-purpose API proxy.
- Re-check the pinned CommitRequest CRD version and fields before migrating writes;
  the alignment brief identifies a `v1alpha1` versus `v1alpha3` discrepancy.
- Document each server-owned read/watch/background operation and its minimal grants.
  Namespace quiz listing before narrowing its RBAC. Ensure participant-facing cached
  data does not disclose resources unavailable to that participant.
- Report CoffeeConfig success followed by CommitRequest failure as partial success;
  do not falsely report atomicity or blindly replay an edit after token expiry.

Acceptance: concurrent-user tests prove credentials cannot cross requests; no failed
participant request retries as the ServiceAccount. Real Kubernetes tests identify
the participant in coffee and quiz audit events, verify relevant denied operations,
and exercise token expiry between the two coffee write operations.

## 5. Retire legacy authentication and narrow deployment permissions

- After all inventory entries pass, delete browser identity generation, the legacy
  login path, application join-code generation and `X-Join-Code` handling.
- Delete impersonation clients/grants, the obsolete ServiceAccount token cache and
  TokenRequest permissions once no consumers remain. Remove the migration switch.
- Restrict Secret access to the named application Secret, or use mounted keys without
  API reads. Retire Secret creation/update permissions that are no longer necessary.
- Supply confidential Dex client registration, accepted Kubernetes audience, exact
  callback/origin, independent secrets and coherent same-origin routing in deployment
  examples. Preserve all Room Pass issuer restrictions and direct-Dex isolation.
- Document pre-creating secrets, restart behavior, deliberate key replacement, cookie
  size limits, expiry/re-login, application logout and platform shutdown semantics.

Acceptance: searches plus rendered RBAC show no surviving legacy login, enrollment,
impersonation or participant-token minting path. A normal deployment works with only
the inventoried grants and rejects expired or unauthorized participant credentials.

## 6. Verify the real application end to end

Use a disposable local k3d fixture with real Dex, Room Pass, Traefik and Kubernetes,
with an explicit local kubeconfig. Integrate the actual application backend and
frontend, rather than only rerunning the existing example OIDC client.

- Exercise fresh enrollment, returning enrollment, backend restart, cookie tampering,
  token expiry, logout, denied authorization and forged identity headers.
- Verify coffee and quiz writes, audit subject/group/claim extras, unavailable
  work resources, and refusal to use ServiceAccount credentials after a denial.
- Check callback/handoff isolation, CSRF, token-free browser responses, actual cookie
  size and browser behavior at desktop/mobile viewports.
- Verify the lifecycle distinction: Room stop denies new authorization; withdrawing
  grants denies new writes with an already-issued token. Test stream termination too.
- Record separately whether ConfigButler/Git attribution was tested. Kubernetes audit
  success alone is not evidence that a resulting Git commit has the intended author.

Run focused checks during each change, then the applicable repository checks:

```sh
task lint
task test
task test-integration
task room-pass:e2e-up
task test-e2e
```

Currently `task test-integration` runs Room Pass envtest coverage and `task test-e2e`
runs the Room Pass fixture. Extend Task targets to include the new application tests;
these commands alone do not currently prove Voter/Coffee integration. Put reusable
test logic in Task targets, and report local e2e results separately from CI status.

Acceptance: report verified behavior, commands/results, remaining limitations and
any source-document contradictions. Do not mark a phase complete solely because
tests for an unrelated component pass.

## Related alignment work

Track the remaining gaps from the alignment brief as separate reviewable changes:
bounded PATCH bodies and retained orders with reset, optimistic concurrency and a
visible conflict state, neutral naming for shared coffee editing, and deployment
manifest consolidation with digest-pinned images. CommitRequest compatibility and
namespace/RBAC corrections are included above where required for token migration.

Input bounds, concurrency and truthful partial-failure handling remain release
readiness concerns even when OIDC works. This plan establishes the identity
architecture; completion must not be reported as closing the entire alignment brief
unless those separate gaps have also been verified and resolved.
