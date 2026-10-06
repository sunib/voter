# Voter

The demo's domain backend. Its image also serves the compiled Vue frontend. On the one
host it shares:

- **krm-foyer** owns login, sessions, `/k8s` and live streams (`/stream/v1`);
- **Room Pass** owns room enrollment (`/join`, `/bind`, `/logout`);
- **Kubernetes** owns authorization, and admission (`config/admission/`) holds what a
  person may write: a ballot's rules, and spec-only edits;
- **Voter** keeps what is not a Kubernetes object, or not one a person writes: coffee
  pricing and orders, the QR join endpoint, the tally reconciler, and its files.

See [the migration plan](../docs/krm-foyer-migration.md),
[architecture](../ARCHITECTURE.md) (Voter 1.x, until the cutover) and
[authorization and tests](../docs/authorization.md).

## HTTP surface

| Route | Purpose |
| --- | --- |
| `GET /healthz` | Process health |
| `GET /config.json` | The deployment's settings, without a session: namespace, object names, commit link template, participant connector, the GitTargets and CommitRequest namespace a save asks to commit to, and the audience grant's Role |
| `GET /join-room?code=` | The QR code's target: Room Pass's join cookie, then a redirect to krm-foyer's login through Room Pass |
| `GET /public/storefront`, `POST /public/orders` | Coffee menu and order decisions |
| `GET /public/orders`, `GET /public/orders/stream` | The room's order feed, as JSON and as SSE |
| `GET /public/vouchers` | Process-local voucher usage |
| `GET /public/build-info` | Build revision metadata |
| `/` | The built frontend, with an SPA fallback |

**`/public/` must only be reachable behind krm-foyer's identity check.** The edge
sends every `/public/` request through `/auth/check?identity=true` (Traefik
ForwardAuth), which answers for the browser's krm-foyer session, CSRF proof included,
and adds `Krm-Foyer-Identity`: the API server's `userInfo` for that person, with their
display name and connector. Voter trusts that header (`foyer_identity.go`) only
because the edge replaces a browser's copy and a NetworkPolicy admits nothing but the
edge to Voter's port. Without the header, a `/public/` handler answers 401 and logs
that the route is wrong.

Voting, saving the coffee menu and Databases, opening and closing rounds and the
audience grant are the browser's own writes through krm-foyer's `/k8s`. Voting uses
persisted QuizSession/QuizSubmission resources: see the
[demo runbook](../docs/voting-demo.md) and [sample round](config/demo-round.yaml).

## Configuration

The full contract is in [config.go](config.go). Voter has no login of its own, so
there is no issuer, client, cookie key or origin to configure.

| Variable | Default / behavior |
| --- | --- |
| `HOST`, `PORT` | `0.0.0.0`, `8080` |
| `STREAM_KUBECONFIG` | Empty uses in-cluster credentials; an explicit file is Voter's own credential outside a cluster (named for the stream gateway it used to serve) |
| `KUBERNETES_API_SERVER` | The in-cluster server, or the explicit kubeconfig's; an override must match the kubeconfig |
| `KUBERNETES_NAMESPACE` | Pod's mounted namespace, otherwise `voter`; explicit value overrides both |
| `COFFEE_CONFIG_NAME` | `testnet-coffee` |
| `ROOM_NAME` | `demo`; the Room whose join code the operator page shows |
| `PARTICIPANT_CONNECTOR_ID` | `room-pass`; the logins that may vote, published in `/config.json` |
| `AUDIENCE_COFFEE_ADMIN_ROLE` | `voter-audience-coffee-admin`; the Role, and RoleBinding, of the operator's audience grant |
| `CONFIGBUTLER_GIT_TARGET_NAME` | `voter-demo`; empty asks no commit after a menu save |
| `CONFIGBUTLER_DATABASE_GIT_TARGET_NAME` | Empty; the same for a Database save |
| `CONFIGBUTLER_COMMITREQUEST_NAMESPACE` | Application namespace unless overridden |
| `CONFIGBUTLER_CLOSE_DELAY_SECONDS` | `2`; how long a commit window may wait for the write it publishes |
| `AUDIT_TRAIL_COMMIT_URL_TEMPLATE` | Empty; turns a commit sha into a link, with `{sha}` substituted |
| `STATIC_DIR` | Empty locally; image sets `/srv/www` |

Voter acts in Kubernetes only as itself (`service_account.go`), for two things: the
storefront reads the one CoffeeConfig, and the tally reconciler reads rounds and
ballots and writes a round's status. It holds nobody's token. TLS verification is
required.

## Development and verification

```bash
task voter:test
task voter:lint
task voter:build
task image-voter
```

Outside a pod, select an explicit kubeconfig for Voter's own identity:

```bash
cd voter
STREAM_KUBECONFIG=/absolute/path/to/voter-stream.kubeconfig go run .
```

Use a dedicated credential with the voter Role's grants, as in the fixture
(`test/e2e/voter.yaml`). Omit `KUBERNETES_API_SERVER` or set it to the same server as
the file. There is no fallback to `$KUBECONFIG` or `~/.kube/config`. Without krm-foyer
in front, the `/public/` endpoints answer 401: run it behind the e2e fixture's edge
(`task e2e-up`) to use them. Vite can serve the frontend separately; see
[frontend development](../frontend/README.md).

The image build context is the repository root because it includes both Go and
Vue sources. Root Task tasks replace the retired Makefile. To publish to a custom
registry use `task image-voter REGISTRY=<registry> IMAGE_OWNER=<owner> TAG=<revision> PUSH=true`.

Deployment and demo CRDs are maintained in the external platform repository under
`2-gitops/voter-demo/`. The legacy root `k8s/` and `k8s-examples/` resources have
been removed. Room Pass is its own project,
[sunib/room-pass](https://github.com/sunib/room-pass); this repository's
disposable e2e fixture, which runs a released Room Pass, is under `test/e2e/`.
