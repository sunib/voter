# Voter

The Go application backend and Dex OIDC client. Its image also serves the compiled
Vue frontend. Room Pass owns room enrollment; Kubernetes owns authorization.
See [login explained](../room-pass/login-explained.md) and
[authorization and tests](../docs/authorization.md).

## Current HTTP surface

| Route | Purpose |
| --- | --- |
| `GET /healthz` | Process health |
| `GET /public/build-info` | Build revision metadata |
| `GET /auth/login` | Start Dex login; optional allowlisted `connector` choice |
| `GET /auth/callback` | Verify and complete OIDC login |
| `GET /auth/session` | Display identity, expiry and CSRF token; never the ID token |
| `POST /auth/logout` | CSRF-protected application logout |
| `GET /public/coffeeconfig` | Read the configured object using the participant token |
| `PATCH /public/coffeeconfig` | CSRF-protected patch and optional CommitRequest |

Storefront, orders, editor streams and quiz forwarding still need restoration.
There is no legacy login mode, ForwardAuth endpoint, impersonation client or
server-owned Kubernetes credential. Browser requests cannot select their identity
by supplying headers. The Kubernetes client uses only the session's Dex ID token.

## Configuration

The full contract is in [config.go](config.go). Supply these OIDC settings:

- `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`
- `OIDC_REDIRECT_URL`: exact callback URI registered with Dex
- `APP_ORIGIN`: public application origin for CSRF validation
- `APP_COOKIE_HASH_KEY`, `APP_COOKIE_BLOCK_KEY`: independent base64-encoded
  32-byte keys, persisted by the deployment

Other settings:

| Variable | Default / behavior |
| --- | --- |
| `HOST`, `PORT` | `0.0.0.0`, `8080` |
| `OIDC_CONNECTOR_ID` | Empty lets Dex choose; use `room-pass` to preselect the room form |
| `OIDC_CONNECTOR_CHOICES` | Comma-separated allowlist for explicit login choices, e.g. `github,linkedin` |
| `PARTICIPANT_COOKIE_NAME` | `__Host-voter-session`; Secure and HttpOnly |
| `SESSION_COOKIE_MAX_AGE_SECONDS` | `43200`, capped by the ID token's expiry |
| `KUBERNETES_API_SERVER` | `https://kubernetes.default.svc` |
| `KUBERNETES_NAMESPACE` | Pod's mounted namespace, otherwise `voter`; explicit value overrides both |
| `COFFEE_CONFIG_NAME` | `testnet-coffee` |
| `CONFIGBUTLER_GIT_TARGET_NAME` | `voter-demo`; empty disables CommitRequest creation |
| `CONFIGBUTLER_COMMITREQUEST_NAMESPACE` | Application namespace unless overridden |
| `STATIC_DIR` | Empty locally; image sets `/srv/www` |

TLS uses the mounted Kubernetes CA when available, otherwise system trust.
No kubeconfig or ServiceAccount token is loaded. Identity extras are mapped by
Kubernetes from the Dex claims; there is no application flag to impersonate extras.

## Development and verification

```bash
task voter:test
task voter:lint
task voter:build
task image-voter
```

With the settings above supplied through your development environment, run
`cd voter && go run .`. The issuer must be reachable for startup discovery.
Use HTTPS at the browser-facing origin because session cookies are always Secure.
Vite can serve the frontend separately; see [frontend development](../frontend/README.md).

The image build context is the repository root because it includes both Go and
Vue sources. Root Task tasks replace the retired Makefile. To publish to a custom
registry use `task image-voter REGISTRY=<registry> IMAGE_OWNER=<owner> TAG=<revision> PUSH=true`.

Deployment and demo CRDs are maintained in the external platform repository under
`2-gitops/voter-demo/`. The legacy root `k8s/` and `k8s-examples/` resources have
been removed. Room Pass's maintained deployment and disposable e2e fixture remain
under `room-pass/`.
