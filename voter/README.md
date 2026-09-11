# Voter

The Go application backend and Dex OIDC client. Its image also serves the compiled
Vue frontend. Room Pass owns room enrollment; Kubernetes owns authorization.
See [architecture](../ARCHITECTURE.md) and
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
| `GET /public/storefront`, `POST /public/orders` | Coffee menu and order decisions |
| `GET /public/vouchers` | Process-local voucher usage |
| `GET /public/stream` | krm-stream CoffeeConfig events |
| `GET /public/rounds` | List voting rounds |
| `GET,POST /public/rounds/{name}` | Read questions plus this participant's `voted` flag, or submit a validated QuizSubmission |
| `GET /public/rounds/{name}/results` | Aggregated counts, averages and shared text answers |

Voting uses persisted QuizSession/QuizSubmission resources. See the
[demo runbook](../docs/voting-demo.md) and [sample round](config/demo-round.yaml).

There is no legacy login mode, ForwardAuth endpoint or impersonation client.
Browser headers cannot select an identity. Direct application API calls use the
session's Dex ID token. Shared watches and access reviews use a separately configured
server credential; Voter enforces each subscriber's access before cache disclosure.

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
| `METRICS_ADDRESS` | `127.0.0.1:9090`; separate unauthenticated listener, never the application route |
| `STREAM_KUBECONFIG` | Empty uses in-cluster credentials; an explicit file enables local shared-client credentials |
| `OIDC_CONNECTOR_ID` | Empty lets Dex choose; use `room-pass` to preselect the room form |
| `OIDC_CONNECTOR_CHOICES` | Comma-separated allowlist for explicit login choices, e.g. `github,linkedin` |
| `PARTICIPANT_COOKIE_NAME` | `__Host-voter-session`; Secure and HttpOnly |
| `SESSION_COOKIE_MAX_AGE_SECONDS` | `43200`, capped by the ID token's expiry |
| `KUBERNETES_API_SERVER` | In-cluster server, or the explicit stream kubeconfig server; an override must match a local kubeconfig |
| `KUBERNETES_NAMESPACE` | Pod's mounted namespace, otherwise `voter`; explicit value overrides both |
| `COFFEE_CONFIG_NAME` | `testnet-coffee` |
| `CONFIGBUTLER_GIT_TARGET_NAME` | `voter-demo`; empty disables CommitRequest creation |
| `CONFIGBUTLER_COMMITREQUEST_NAMESPACE` | Application namespace unless overridden |
| `STATIC_DIR` | Empty locally; image sets `/srv/www` |

TLS trust is taken from the selected cluster configuration; TLS verification is required.
Participant REST clients load no service-account credentials. The process-wide stream
backend uses in-cluster credentials (or an explicit local kubeconfig) and narrow CoffeeConfig read/SAR
grants. Each subscription resolves username/groups/UID/extras with the participant's
SelfSubjectReview, then checks list/watch before disclosure and every 30 seconds.
Writes keep the participant token; there is no impersonation or write fallback.
The application listener returns 404 for `/metrics`. Aggregate metrics are served
on `METRICS_ADDRESS` instead. Fixture/platform manifests bind port 9090 on the pod
network; the public Service and ingress route only port 8080. Port 9090 is
unauthenticated inside that network, and no scraper/ServiceMonitor is configured.
Fixture tests read it through the authenticated Kubernetes pod proxy.
See [shared-stream verification](../docs/shared-streams.md).

## Development and verification

```bash
task voter:test
task voter:lint
task voter:build
task image-voter
```

With the settings above supplied, select an explicit kubeconfig for the shared
reader/access-review identity before running outside a pod:

```bash
cd voter
STREAM_KUBECONFIG=/absolute/path/to/voter-stream.kubeconfig go run .
```

Use a dedicated credential with the named CoffeeConfig list/watch and SAR grants
shown in the fixture. The file's server and CA trust also configure participant
clients, but its token, client certificate, exec plugin and impersonation settings
are never copied into those clients. Omit `KUBERNETES_API_SERVER` or set it to the
same server as the file. There is no fallback to `$KUBECONFIG` or `~/.kube/config`.
The issuer must be reachable for startup discovery.
Use HTTPS at the browser-facing origin because session cookies are always Secure.
Vite can serve the frontend separately; see [frontend development](../frontend/README.md).

The image build context is the repository root because it includes both Go and
Vue sources. Root Task tasks replace the retired Makefile. To publish to a custom
registry use `task image-voter REGISTRY=<registry> IMAGE_OWNER=<owner> TAG=<revision> PUSH=true`.

Deployment and demo CRDs are maintained in the external platform repository under
`2-gitops/voter-demo/`. The legacy root `k8s/` and `k8s-examples/` resources have
been removed. Room Pass's maintained deployment and disposable e2e fixture remain
under `room-pass/`.
