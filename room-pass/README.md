# Room Pass

Room Pass enrolls a browser with a room code and an unverified display name, supplies
that stable identity to Dex, and lets Kubernetes enforce RBAC. It has no quiz/coffee
dependencies, database, JWT signing key, impersonation permission, or participant grants.

The initial implementation serves **one Room from one replica**. Kubernetes stores the
Room, rolling codes and Participants; a Secret stores the cookie keys. Deployment uses
`Recreate`. Do not increase replicas or introduce another enrollment writer.

## Run the complete local example

From the repository root, in the shared devcontainer:

```sh
task room-pass:test
task room-pass:integration
task room-pass:e2e-up
task room-pass:e2e
task room-pass:load  # optional 300-enrollment rehearsal; cleans up its records
```

Prerequisites: Docker, k3d, kubectl, Go 1.25+, Task, Python 3, OpenSSL,
`controller-gen` and `setup-envtest`. The fixture pins K3s **v1.31.5-k3s1** and Dex
**v2.45.1**. Dex v2.45.0 has an authproxy interface regression fixed in
[v2.45.1](https://github.com/dexidp/dex/releases/tag/v2.45.1).

The dedicated `room-pass-e2e` cluster uses an explicit kubeconfig under
`room-pass/.local/`; setup never selects another cluster or applies to the default
context. It leaves the cluster running for exploration. Images and local CA/keys stay
local. The fixture uses a Docker volume, so it also works with the devcontainer's
sibling Docker daemon. On this host, the runtime inotify instance limit was raised
from 128 to 1,024 to accommodate the additional cluster. Its TLS port is **18443** on the Docker host.

For a browser, resolve `demo.roompass.test` and `login.roompass.test` to the Docker
host (or `127.0.0.1` with a local tunnel forwarding port 18443). Trust the generated
`room-pass/.local/tls.crt` in a dedicated test browser profile, then open:

**https://demo.roompass.test:18443/app/**

Choose **Join the demo**, enter the projected code and a name, then press **Write a
message to Kubernetes**. The tiny example OIDC client uses authorization code + PKCE,
validates the token, and sends it to the real Kubernetes API. It has no ServiceAccount
credential. The demo session lasts five minutes; signing in again in the same browser
reuses Room Pass enrollment. No real email address is collected.

Read only the code needed for projection:

```sh
export KUBECONFIG="$PWD/room-pass/.local/kubeconfig"
kubectl -n room-pass get room demo \
  -o jsonpath='{.spec.title}{"\n"}{.status.joinCode.code}{"\n"}{.status.joinCode.expiresAt}{"\n"}'
kubectl -n demo get configmaps
```

Or project a QR code instead, so nobody has to type anything but a name:

```sh
task room-pass:present BASE=https://voter.koudijs.dev NEXT=/answer/round-1
```

That follows the rotating code and redraws, under your own kubeconfig — a join code is
operator-only credential material, so Kubernetes decides who may see one. The scan lands
on the application's login URL carrying both the code and the page to finish on.

That works through a general Room Pass integration point: **an application sharing the
join host may pre-supply a room code in the `__Host-room-pass-joincode` cookie**, and the
join page then asks only for a display name. Room Pass owns the name and the rules, any
application can use it, and nothing in the channel knows what a QR code is — a QR is just
the most convenient way to get a code into a phone. Room Pass never trusts the value: it
becomes a prefill and is checked against the Room's valid codes through the ordinary
POST. [Joining by QR code](docs/qr-join.md) has the contract and a worked integration.
The typed path is unchanged and still works for anyone who cannot scan.

Six letters may be shown as `BCD-FGH`; case, display hyphens and outer whitespace are
ignored. Defaults rotate every **15 seconds** and expire each code after **30 seconds**:
two overlapping codes, with 15 seconds to finish typing the previous displayed code.
A Room can instead choose a longer overlap, up to four retained codes. Desired-state
changes start a fresh code epoch; a rapid close/reopen cannot revive stale codes. Validity uses
`now < expiresAt`, including when reconciliation is delayed. Rotation never signs out
an enrolled browser. See [requirements.md](requirements.md) for the broader contract.

## What is tested

- Unit/race tests: rotation, exact expiry, restart history, randomness failures and
  collisions; serialized enrollment limits; UID binding; revocation; CSRF; forged
  headers; cross-host binding and one-time handoff replay.
- `integration`: envtest runs a real Kubernetes 1.31 API server and etcd, installs the
  generated CRDs, and checks defaults, immutable fields, irreversible stop/revoke,
  status updates and reconcile idempotence. Ordinary `go test` skips this suite unless
  `KUBEBUILDER_ASSETS` is set; the Task target sets it explicitly.
- `e2e`: real HTTPS Traefik → Room Pass → Dex authorization code + PKCE, cryptographic
  token verification, stable subject after Room Pass restart, device authorization without a local callback listener, native OIDC ConfigMap
  write, denied work Secrets and Room reads, forged callback headers/aliases, direct
  Dex network isolation, audit identity/extras, and grant withdrawal against an
  already-issued token. It restores its temporary RBAC changes.

The test client models a browser's redirects and cookie jars; it is not a mobile browser
rendering test. The fixture demonstrates attribution in Kubernetes audit events. It does
**not** install Flux/ConfigButler, verify a resulting Git commit, migrate Voter/Coffee, or
provide a work identity provider. Those remain platform integration work.

## Kubernetes API and deployment

```sh
# Choose a destination kubeconfig explicitly before deployment.
kubectl apply -f room-pass/config/crd/
kubectl apply -k room-pass/deploy/base/
```

The base intentionally contains local example hostnames/image tags. Supply your image
digest, `JOIN_ORIGIN`, `ISSUER_ORIGIN`, `DEX_UPSTREAM`, `ROOM_NAME`, and
`ALLOWED_RETURN_URLS` through an environment overlay. Apply a Room with a future end
time, a demo-prefixed group, and exact HTTPS return URLs included in that platform
allowlist. `test/e2e/up.sh` contains a runnable Room example. Required cookie Secret:

```sh
umask 077
openssl rand 32 > hash-key
openssl rand 32 > block-key
kubectl -n room-pass create secret generic room-pass-cookie \
  --from-file=hash-key --from-file=block-key
rm hash-key block-key
```

Keep keys out of Git, logs and image layers. Back up the Secret together with Rooms and
Participants using your Kubernetes/etcd backup process. A restart preserves enrollment;
losing or replacing keys signs everyone out. Rotation with multiple verification keys is
not implemented. The local fixture preserves these keys on repeated `e2e-up` runs. Dex itself uses
ephemeral in-memory storage **only in this fixture**; its restart resets signing keys
and unfinished OAuth transactions. Use a supported persistent Dex storage backend for
a deployed event. `e2e-up` restarts the fixture services to load source/config changes.

Build Room Pass independently:

```sh
docker build -t room-pass:dev room-pass/
```

Its multi-stage Dockerfile produces a static binary in a non-root distroless image.
The manifest supplies a read-only root filesystem, dropped capabilities and separate
`/healthz` and `/readyz` probes. Readiness checks live Room configuration, its reconciled generation, and Participant
storage. The ServiceAccount can read Rooms, update Room status, read/create Participants,
and get only the named cookie Secret. It cannot create RBAC, impersonate, or edit apps.

Room and Participant read access is **operator-only**: Room status contains valid
credentials and Participant records contain unverified labels. The fixture's metadata-only
audit policy keeps enrollment codes and cookie Secret bodies out of audit logs.

Regenerate checked-in CRDs and deepcopy implementations with:

```sh
task room-pass:generate
```

CEL and OpenAPI validate bounds, enums, immutability and irreversible transitions at the
API server. `allowedReturnURLs` is a set and conditions are a map keyed by type.

## Dex and routing trust boundary

See [the handoff protocol](docs/handoff.md). **All public issuer traffic must go through
Room Pass**, including callback aliases. Do not expose Dex with another Ingress,
NodePort, LoadBalancer or port-forward. The fixture's Traefik routes send both hosts to
Room Pass, and its NetworkPolicy admits Dex traffic only from Room Pass pods. This
requires a CNI that enforces NetworkPolicy; k3s's network policy controller is enabled.
Treat permission to label/create Room Pass pods or alter these routes/policies as trusted
operator access.

Dex uses one `authproxy` connector with ID `room`. Optional Dex browser sessions remain
disabled; authproxy does not issue refresh tokens. Configure the exact header names in
[test/e2e/dex.yaml](test/e2e/dex.yaml). Room Pass overwrites the entire `X-Remote-*`
contract from fresh Room/Participant reads. No public forward-auth header endpoint exists.

The fixture configures native JWT authentication, `demo:` usernames, Room-selected demo
groups and `configbutler.ai/claims/display-name` / `configbutler.ai/claims/email` audit
extras. Kubernetes uses Dex's opaque subject, not the display name, as identity.
[Structured authentication](https://kubernetes.io/docs/reference/access-authn-authz/authentication/#authentication-configuration-from-a-file)
is independent of Room Pass's cookie session. The generated email is synthetic even
though Dex authproxy marks it verified.

## Limits and failure behavior

| Setting | Default |
|---|---|
| Traefik edge bucket | 200 requests/second, burst 500 per source |
| `JOIN_RATE` / `JOIN_BURST` | 20 attempts/second, burst 150 per serving process |
| `HANDOFF_RATE` / `HANDOFF_BURST` | 20 starts/second, burst 150 per serving process |
| `MAX_HANDOFFS` | 1,000 pending transactions |
| Handoff lifetime | 3 minutes |
| `COOKIE_LIFETIME_SECONDS` | 86,400 (24 hours), maximum 7 days |
| Browser form | 4 KiB maximum; name 1–64 UTF-8 bytes |
| `KUBE_QPS` / `KUBE_BURST` | 100 QPS, burst 200; 8-second individual API timeout |
| Request context | 20 seconds |

Service limits are global, not keyed by forwarded IP: a conference NAT does not become a
single-person quota, and attacker-supplied forwarding headers cannot select a bucket.
Rate limits reject with a retry response; no participant or room is permanently locked.
The local real-cluster rehearsal completed **300/300** enrollments in three batches
of 100 from one source IP, with the final Traefik rate limit enabled: **p50 2.427s, p95 8.812s, max 10.119s**. The initial
50-QPS/10-second configuration achieved only 265/300, which motivated the measured
budget change. This is a local fixture result, not a conference capacity guarantee.
`task room-pass:load` repeats it and removes only its own test participants afterwards.
The separate race test uses a fake Kubernetes client and is not a capacity benchmark. Enrollment reads the current Room, lists retained Participants and
creates one record under a single-process mutex; authorization reads Room + Participant.
Revoked records still consume capacity. Status participantCount is observational only.

API errors fail closed. An uncertain create response is looked up using the original
random name, never retried immediately with a new ID. Requests already in flight may
finish during a stop. Pending logins are in bounded memory and restart after pod
replacement; enrolled identities persist. Signing out clears the cookie, not the
Participant or Dex tokens, and joining again consumes a new slot.

## Operate, stop and reset

```sh
# Close new joins while allowing enrolled browsers to log in again.
kubectl -n room-pass patch room demo --type=merge -p '{"spec":{"enrollment":"Closed"}}'
# Reopen; the controller publishes a fresh code.
kubectl -n room-pass patch room demo --type=merge -p '{"spec":{"enrollment":"Open"}}'
# Permanently stop this Room object.
kubectl -n room-pass patch room demo --type=merge -p '{"spec":{"stopped":true}}'
# Stop already-issued tokens from making new demo writes as well.
kubectl -n demo delete rolebinding demo-editor
```

Room stop blocks new enrollment and identity assertions. It does not revoke a signed Dex
token. Withdraw grants, close routes and drain long-running connections to shut down
platform access. If Flux owns these resources, suspend the owning reconciliation, commit
the stopped desired state and removed grants, then resume. Shared groups share permissions.

Deleting/recreating a Room gives it a new UID and invalidates its old sessions; owner
references allow Kubernetes to garbage-collect its Participants. To reset the local
fixture after an irreversible stop, delete its Room before rerunning `e2e-up`, or rebuild
only this disposable cluster:

```sh
task room-pass:e2e-down
task room-pass:e2e-up
```

On a Docker host with many clusters, `failed to create fsnotify watcher: too many open
files` can mean `fs.inotify.max_user_instances` is exhausted. Check the node's containerd
log; increase that host runtime limit before retrying. Setup does not silently tune host
sysctls or stop unrelated clusters. Local TLS certificates expire after seven days;
recreate the fixture to generate a new CA and trust it in the test browser.

## Observability and browser verification

Room Pass now serves a separate Prometheus endpoint on port 9090. See
[metrics](docs/metrics.md) for meanings, privacy guarantees and troubleshooting queries.

Run `task room-pass:e2e-up` from the root, then `task test-browser` to watch the
room authentication contract exercised in Chromium. The
[browser suite](test/browser/README.md) retains videos and a successful-login screenshot.
