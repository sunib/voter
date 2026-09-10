# Implementation plan

Updated 2026-09-10 after the working-login milestone. Current design:
[architecture](advised_architecture.md). Access contract:
[authorization and tests](../docs/authorization.md).

## Completed baseline

- [x] One Dex issuer with room-pass, GitHub and LinkedIn connectors configured.
- [x] Backend OIDC client, encrypted token cookie, CSRF and session endpoints.
- [x] Participant-token CoffeeConfig GET/PATCH and v1alpha3 CommitRequest creation.
- [x] Delete legacy browser-asserted login and ServiceAccount impersonation code.
- [x] Package Vue and Go together as the Voter image.
- [x] Display Kubernetes' own username via SelfSubjectReview.
- [x] CI tasks for both Go modules, frontend build/lint and Room Pass envtest.
- [x] Add HTTP authorization regression cases and document their limitations.
- [x] Add a CI network-isolation suite with real Dex/pods, positive controls,
  namespace/label denials, and policy removal/restoration in a disposable cluster.
  Accept an actual platform policy file override to validate platform edits.

The user reports working login. The earlier handoff did not establish a completed
LinkedIn login; connector-specific end-to-end verification remains outstanding.

## 1. Make access policy executable

- [ ] Render the actual platform authenticator and evaluate its expressions for
  each connector: valid identity, missing/unknown connector, missing claims,
  forged operator email from Room Pass, non-demo groups and system groups.
  Include the transitional `room` connector while it remains accepted.
- [ ] Use a disposable API server to test actual tokens and RBAC: room attendee,
  unrelated GitHub/LinkedIn user, named owner, and unauthenticated caller.
  Assert allowed CoffeeConfig patch and denied Secret reads, RBAC changes,
  impersonation, resource deletion and writes outside the demo namespace.
- [ ] Audit grants to `system:authenticated` and each other shared group.
- [ ] Decide whether attendees may edit every CoffeeConfig in `voter` (current
  platform Role), read all submissions, and submit to a closed quiz. Enforce
  object/lifecycle rules with appropriate scoped RBAC or admission and test
  direct API calls as well as application requests.
- [ ] Before opening GitHub login, review every Dex client independently.
  Replace oauth2-proxy's broad gmail domain gate and placeholder operator group
  policy with the intended allowlist/group. Verify an unrelated account is denied
  by Voter resource RBAC, Flux UI, Grafana and the proxy-protected applications.

Acceptance: a connector/identity/operation matrix runs against rendered policy
and real authorization. Login eligibility never serves as an implicit admin grant.

## 2. Restore one complete application journey

- [ ] Port storefront and orders, followed by editor reads/watches/history and
  quiz forwarding. Inventory each route's identity, resource/verb, CSRF requirement
  and 401/403 behavior before implementing it.
- [ ] Keep credentials scoped to each request; no fallback server writes.
- [ ] Preserve conditional writes and surface 409 conflicts in the editor.
- [ ] Distinguish Kubernetes save, CommitRequest accepted, and Git commit observed.
- [ ] Decide order/voucher persistence and rollout semantics before adding replicas.

Acceptance: an attendee logs in, orders and edits; an ungranted external account
gets understandable denials; the expected user appears in audit and Git output.

## 3. Exercise the browser and lifecycle

- [x] Add Chromium tests for the Room Pass + Dex fixture: enrollment and stable
  return identity, invalid code, CSRF, cookie flags and closed enrollment. Run in
  CI with retained recordings. Fix the cross-origin form redirect CSP found by it.
- [x] Expose operational metrics with bounded labels and scrape-time handoff gauges.

- [ ] Add browser-driven login through the real Voter app and Room Pass form,
  including browser Origin behavior, returning enrollment, logout and expiry.
- [ ] Test state/nonce mismatch, wrong issuer/audience/signature and successful
  callback replay with a controlled OIDC issuer through the actual callback.
- [ ] Demonstrate that stopping a Room blocks new handoff but an issued token
  remains usable until expiry unless its effective authorization is removed.
- [ ] Preserve unsaved input across re-login; stop/reconnect streams correctly.
- [ ] Put a reliable disposable-cluster/browser smoke test in CI. The existing
  `task test-e2e` fixture is not yet a CI gate or a test of the real frontend.

## 4. Reconcile deployment and operational follow-up

- [x] Retire root `k8s/`, its duplicate `k8s-examples/` overlay, old HTTP examples,
  Makefile and unused join screen. Remove the unused server Kubernetes client.
- [ ] Verify the platform-owned application manifests against this code, including
  rendered routes, identities and permissions. Demo CRDs now live with that deployment.
- [ ] Confirm the prefixed operator identity before removing bare-email bindings.
- [ ] Remove legacy connector acceptance with the next planned authenticator rollout.
- [ ] Resolve the handoff's stalled FluxInstance and verify current reconciliation.
- [ ] Record source revision, image digest, deployment revision and rollback steps.

Use local/disposable fixtures for development. A fresh live verification is needed
before claiming any historical cluster finding has been resolved. No platform
policy changes or deployments were made as part of this documentation/test pass.
