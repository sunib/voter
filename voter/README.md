# Voter

The Go application backend. Its image also serves the compiled Vue frontend. Room Pass
owns room enrollment, krm-foyer owns login, sessions, `/k8s` and live streams on the
same host, and Kubernetes owns authorization. Voter is partway through moving onto
krm-foyer: see [the migration plan](../docs/krm-foyer-migration.md).
See [architecture](../ARCHITECTURE.md) and
[authorization and tests](../docs/authorization.md).

## Current HTTP surface

| Route | Purpose |
| --- | --- |
| `GET /healthz` | Process health |
| `GET /public/build-info` | Build revision metadata |
| `GET /config.json` | The deployment's settings (namespace, object names, commit link template, participant connector), without a session |
| `GET /join-room?code=` | The QR code's target: Room Pass's join cookie, then a redirect to krm-foyer's login through Room Pass |
| `/auth/login`, `/auth/callback`, `/auth/session`, `/auth/whoami`, `/auth/rules`, `/auth/logout` | Voter's own OIDC login, **no longer routed**: krm-foyer answers `/auth/` on the shared host. Deleted in step 5 |
| `GET /public/coffeeconfig` | Read the configured object using the participant token |
| `PATCH /public/coffeeconfig` | CSRF-protected patch and optional CommitRequest |
| `GET /public/storefront`, `POST /public/orders` | Coffee menu and order decisions |
| `GET /public/vouchers` | Process-local voucher usage |
| `GET /public/rounds` | List voting rounds |
| `GET,POST /public/rounds/{name}` | Read questions plus this participant's `voted` flag, or submit a validated QuizSubmission |
| `GET /public/rounds/{name}/results` | Aggregated counts, averages and shared text answers |

Voting uses persisted QuizSession/QuizSubmission resources. See the
[demo runbook](../docs/voting-demo.md) and [sample round](config/demo-round.yaml).

The `/public/*` handlers still read Voter's own session cookie and use its Dex ID
token, so behind krm-foyer they answer 401 until steps 3 to 5 replace them. Live
streams are krm-foyer's `/stream/v1`; Voter no longer serves a stream or `/metrics`.

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
| `STREAM_KUBECONFIG` | Empty uses in-cluster credentials; an explicit file is Voter's own credential outside a cluster (named for the stream gateway it used to serve) |
| `PARTICIPANT_CONNECTOR_ID` | `room-pass`; the logins that may vote, published in `/config.json` |
| `ROOM_NAME` | `demo`; the Room whose join code the operator page shows |
| `AUDIT_TRAIL_COMMIT_URL_TEMPLATE` | Empty; turns a commit sha into a link, with `{sha}` substituted |
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
Participant REST clients load no service-account credentials. Voter's own credential
(`service_account.go`) is used by the tally reconciler alone, to read rounds and
ballots and write a round's status. Writes keep the participant token; there is no
impersonation or write fallback.

## Development and verification

```bash
task voter:test
task voter:lint
task voter:build
task image-voter
```

With the settings above supplied, select an explicit kubeconfig for Voter's own
identity before running outside a pod:

```bash
cd voter
STREAM_KUBECONFIG=/absolute/path/to/voter-stream.kubeconfig go run .
```

Use a dedicated credential with the reconciler's grants shown in the fixture. The file's server and CA trust also configure participant
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
been removed. Room Pass is its own project,
[sunib/room-pass](https://github.com/sunib/room-pass); this repository's
disposable e2e fixture, which runs a released Room Pass, is under `test/e2e/`.
