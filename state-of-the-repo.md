# State of the repository — 2026-09-10

Login is working, as reported by the user. The repository now uses Dex OIDC and
participant tokens. The previous review described the deleted impersonation
model; its historical content remains in Git history.

This update inspected local source and platform configuration. It did not inspect
or change the live cluster. The [dated handoff](room-pass/state-2026-09-10.md)
records the earlier deployment checks and operational findings.

## Working foundation

- One Voter image contains Vue and its Go backend/OIDC client.
- Room Pass supplies room-code enrollment to one Dex issuer alongside GitHub and
  LinkedIn. GitHub is still organization-restricted in the recorded configuration.
- The application stores a signed, encrypted token cookie, enforces CSRF, and
  sends participant tokens to Kubernetes without impersonation or SA fallback.
- Kubernetes' SelfSubjectReview provides the displayed username.
- CoffeeConfig GET/PATCH is ported, including v1alpha3 CommitRequest creation and
  explicit partial success when the second write fails.
- CI exists: lint, Go tests, frontend type-check/build, Room Pass envtest and images.

## Cleanup completed

The unused server Kubernetes client and kubeconfig/ServiceAccount credential loading
are removed. Namespace discovery now uses `KUBERNETES_NAMESPACE`, the mounted pod
namespace, or `voter` as a standalone fallback. The unused impersonation-extras
setting, legacy session cache types, old HTTP examples, orphaned JoinScreen and
duplicate Makefile are also removed. Maintained commands use Task.

## What needs attention

1. **Login is ahead of the app.** Storefront, orders and editor endpoints were
   removed with legacy auth; frontend callers remain. Quiz forwarding also needs
   restoration. A complete demo journey is not established.
2. **Access needs deeper proof.** New HTTP tests cover local gates and upstream
   denials; rendered authenticator and real RBAC tests across three connectors
   remain to be built. Chromium now tests the local Room Pass/Dex login fixture
   in CI, including the cross-origin form redirect that exposed a CSP bug. The real
   Voter journey still needs separate browser coverage.
3. **Deployment has one owner.** The legacy root `k8s/` and `k8s-examples/`
   manifests have been removed. The OIDC application deployment and demo CRDs
   live in the external platform checkout; rendered deployment verification remains
   necessary. Room Pass component manifests and local test fixtures remain here.
4. **External login requires per-client authorization.** The named LinkedIn owner
   has cluster-admin, not just Flux UI access. Other users have no named grants
   by default, but shared authenticated-user permissions still need auditing.
   Opening GitHub login remains a proposal, with oauth2-proxy/Grafana/Flux gates
   to review first.
5. **Save does not prove Git completion.** `committed: true` means a CommitRequest
   was created; it does not establish a resulting Git commit.
6. **Historical operational findings need rechecking.** The handoff records a
   stalled FluxInstance, transitional subjects/connector IDs and incomplete
   LinkedIn end-to-end verification. None was reverified in this pass.

Room closure stops new identity handoff, not issued tokens. App logout clears
only the app cookie. These lifecycle limits are part of the access model.

## Reading and next work

Start with [authorization and tests](docs/authorization.md), then follow the
[implementation plan](room-pass/implementation_plan.md). The
[architecture](room-pass/advised_architecture.md) and
[login explanation](room-pass/login-explained.md) describe the current model.
The [documentation index](docs/README.md) separates current guidance from history.

## Room Pass observability and browser checks

Prometheus metrics are available on the separate `:9090/metrics` listener, covering
HTTP outcomes/latency, enrollments, detailed rejections, Dex transport failures and
handoff pressure. The component manifests expose a metrics Service port; platform
monitoring adoption remains separate. See [metrics](room-pass/docs/metrics.md).

Chromium tests record the real room-authentication flow through the local fixture,
including returning enrollment and denial cases. They found and verified a fix for
CSP blocking a form redirect from the join origin to the configured issuer. See
[browser tests](room-pass/test/browser/README.md).
