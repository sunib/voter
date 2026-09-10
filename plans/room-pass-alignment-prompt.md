# Brief: bring the application in line with Room Pass

**For an agent working in `sunib/voter`.** Written 2026-09-09, against `main` at
`0dba0c1`. Read this whole file before changing anything.

Room Pass now exists under [room-pass/](../room-pass/) and is verified against a real
Kubernetes API server, a real Dex v2.45.1 and a real Traefik in a local k3d fixture. The
**application has not moved at all**: `frontend/`, `auth-service/` and `k8s/` still run
the pre-Room-Pass design — a self-asserted browser identity, a signed session cookie, and
Kubernetes writes performed by impersonation. This brief is the gap between the two.

Your job is to close that gap in the application. It is *not* to change Room Pass, and it
is *not* to build the cluster.

## Sources of truth, in this order

| Document | What it settles |
|---|---|
| [room-pass/requirements.md](../room-pass/requirements.md) | The first-version contract: scope, event/session behavior, Dex integration, acceptance checks |
| [room-pass/README.md](../room-pass/README.md) | What is actually implemented, the measured limits, how to run the local fixture |
| [room-pass/docs/handoff.md](../room-pass/docs/handoff.md) | The cross-host handoff protocol and its trust boundary |
| [state-of-the-repo.md](../state-of-the-repo.md) | The source review of the current application, with the defects located |
| `external/k8s/conference-demo-identity.md` | The identity review — §5 claim pipeline, §6 impersonation, §7 authorization, §8 lifecycle. **Private repo, gitignored checkout** |
| `external/k8s/demo-platform-implementation-plan.md` | Directory ownership, hostnames, deployment inputs. **Private repo, gitignored checkout** |

If a claim in this brief and a claim in the code disagree, **the code wins** — say so and
correct the brief. Several statements here are dated observations, not guarantees.

## Ground rules

1. **Verify before you change.** Every gap below names a file and a line or symbol. Open
   it first. If the defect is already fixed, record that and move on.
2. **Do not modify `room-pass/`** unless you find a genuine defect in it, and then raise
   it separately rather than folding it into an application change.
3. **Do not create `platform/` or `demo-state/`.** Those belong to the private platform
   repository's plan and to a different piece of work.
4. **Keep the current demo runnable at every commit.** The existing cookie login is what
   works today; do not delete it before its replacement passes a test.
5. **No cluster access.** Everything here is verifiable with `task test`,
   `task test-integration`, `task lint`, and the local k3d fixtures.
6. **Small reviewable PRs**, one gap or one coherent pair of gaps at a time.
7. **Ask before choosing** on the one decision marked *open* below. Do not pick it
   silently.

## The contract Room Pass offers

The application is a **consumer of ID tokens**, nothing more. It does not enroll anyone,
does not see room codes, and does not read `Room` or `Participant` resources — those are
operator-only, because Room status contains working enrollment codes and Participant
records contain unverified display names.

```text
browser → Room Pass (join code + display name) → Dex → ID token → the app and Kubernetes
```

What arrives in the token, and where each value comes from:

| Value | Source | What the app may assume |
|---|---|---|
| `sub` | Dex's opaque `GenSubject(userID, connectorID)` | Stable per participant per connector. **Not** the participant ID Room Pass minted, and not readable |
| Kubernetes username | `sub` with an API-server-enforced `demo:` prefix | Structurally unforgeable. Use it as the identity key |
| `name` | `X-Remote-User` — the display name | A **presentation label**. Never an authorization key. Escape it |
| `email` | `<participant-id>@demo.invalid` | Git author formatting. **Not** a verified mailbox, despite `email_verified` being true |
| `groups` | The Room's `audienceGroup`, always `demo:`-prefixed | The RBAC input. Never accepted from client input |

Two consequences the application code must respect:

- **The `profile` scope is required** for `name` and `preferred_username`. Request
  `openid profile email groups`.
- **Kubernetes needs no OAuth client registration** — it validates tokens. Only flow
  initiators (the SPA, the CLI) get registered, and the token's audience must be one the
  cluster's authenticator accepts. Distinct SPA and CLI clients mean listing multiple
  accepted audiences, not forcing one shared `client_id`.

## The gaps

Ordered by what unblocks the most. Each gap states the defect, why it matters, and the
check that proves it closed.

### 1. The browser invents its own identity — *open decision blocker, do first*

`frontend/src/lib/demoIdentity.ts:11` generates a six-digit ID with `Math.random()`, and
`auth-service/http_handlers.go:39` (`/public/login`) accepts `stableId`, `displayName`
**and `email`** from that request body. Signing the resulting cookie makes the claim
authentic to the issuer; it does not establish who supplied it. Six digits also collide in
a room of 300.

Replace this with an OIDC authorization-code + PKCE flow against the demo issuer, using an
established client library rather than hand-rolled code. Validate issuer, signature,
audience, expiry, and the flow's `state`/`nonce`. Never the implicit flow.

**Open decision — ask, do not choose alone:** does the token live as a bearer token in the
browser, or server-side behind an HttpOnly cookie? Both are valid OIDC architectures with
different risks. The identity review leaves this open deliberately (§12).

*Done when:* no client-supplied identity value reaches a server-side identity decision;
the SPA obtains a token through PKCE; and a test asserts that posting a chosen `stableId`,
`email` or group cannot influence the identity the server uses.

### 2. Every coffee write is an impersonated write — *the open architectural decision*

`frontend/src/api/coffee.ts` → `PATCH /public/admin/coffeeconfig` →
`auth-service/coffee_handlers.go:119` → `patchCoffeeConfig` → `impersonatedDynamic`
(`auth-service/kube_client.go`), and `createCommitRequest` likewise. **Handing the SPA a
token changes none of this** — the write still happens in the backend under impersonation.

Three ways to close it, from the identity review §6:

1. **Forward the participant's token.** The backend keeps its orchestration role — change
   history, CommitRequest, validation — but attaches the caller's validated bearer token
   outbound. Its own ServiceAccount is reserved for genuinely server-side operations.
2. **Move the write to the client.** Simplest authorization story. The change-history
   entry and CommitRequest then have to move or become separate calls.
3. **Keep a narrowed impersonation path**, documented as accepted rather than fixed.

Option 1 is the plan's stated direction. **Confirm it before implementing.**

Whichever you pick, inventory *all* the handlers — `coffee_handlers.go` has ten routes and
the quiz submission path is separate. Forwarding two write methods does not migrate the
endpoint surface.

*Done when:* every write path either carries the participant's own token or is explicitly
documented as a server-side operation under the service's own identity, and the audit
event for a coffee edit names the participant.

### 3. The impersonation grant permits arbitrary usernames

`k8s/audience-impersonator-rbac.yaml` grants `impersonate` on `users` with **no
`resourceNames`** — the group is pinned to `voter-audience` and the two claim extras are
enumerated, but the username is not bounded, and `k8s/auth-service-rbac.yaml` has the same
shape. RBAC matches exact strings, not a
`demo:` prefix, so "only demo identities" is not expressible. Pinning the *group* does not
help — access obtained through a privileged username bypasses it.

**Removing a caller does not shrink this.** The radius shrinks when the grant does. Note
that Room Pass does not inherit the problem: its ServiceAccount has no `impersonate` verb
at all.

Delete the impersonation grants, the token cache and the TokenRequest permission **only
after** every path using them has migrated and passed tests. Until then, keep the old
deployment closed to public traffic. If an impersonation path must survive, the bounded
option is a finite pool of pre-generated usernames enumerated in `resourceNames`, at the
cost of a fixed room size.

*Done when:* either the grants are gone, or they are narrowed to an enumerated pool and
the residual authority is written down as accepted.

### 4. CommitRequest API version

`auth-service/kube_client.go` writes `configbutler.ai/v1alpha1`. The pinned reverser's
CRD (`external/gitops-reverser/config/crd/bases/configbutler.ai_commitrequests.yaml`)
serves **`v1alpha3` only** — checked, not remembered. Re-check it against whatever release
is pinned when you do the work, match the field names too, and add a test that fails when
they drift.

*Done when:* the client's group/version/fields match the pinned release, verified against
the CRD rather than against memory.

### 5. Unbounded input and unbounded state

- `auth-service/coffee_handlers.go:131` reads the CoffeeConfig PATCH body with
  `io.ReadAll` and no cap.
- Orders are retained in an unbounded slice, with no explicit demo reset.

Room Pass already bounds its own equivalents — a 4 KiB form, a 1–64 byte name, bounded
code history and bounded pending handoffs. The application has no such limits.

*Done when:* request bodies are capped with a clear error, retained order state is
bounded, and there is an explicit reset for the demo.

### 6. Concurrent edits silently overwrite

The coffee editor writes without the resource version it read, so two people editing at
once means last-write-wins. Two people editing concurrently is a **normal** case here —
collaboration on one shared CoffeeConfig is the intended demo, not an edge case.

*Done when:* the write is conditional on the version the editor actually read, a conflict
surfaces in the UI as a conflict, and a test covers the interleaving.

### 7. `listQuizSessions` lists cluster-wide

`auth-service/kube_client.go` calls `.Resource(gvr).List(...)` with no `.Namespace(...)`.
Unlike the Secret grant below, this one needs a code change before RBAC can be narrowed.

*Done when:* the call is namespaced and the corresponding RBAC no longer needs a
cluster-scoped list.

### 8. The Secret grant is broader than the code needs

`k8s/auth-service-rbac.yaml` grants `get`/`create`/`update` on **all** Secrets in the
namespace. The code touches one fixed name (`auth-session-cookie-keys`,
`auth-service/session_cookie.go`). `get`/`update` can be pinned with `resourceNames`
today, no code change. `create` cannot be name-scoped by RBAC at all — pre-create the
Secret and drop the verb. Room Pass already does exactly this: its Role can `get` one
named Secret and nothing else.

*Done when:* the Role names the one Secret, `create` is gone, and the deployment
documents pre-creating it.

### 9. Two code authorities, once Room Pass is in

`auth-service/join_codes.go` generates its own access codes. Room Pass's controller
publishes rolling codes in `Room` status. **Do not run both.** Once enrollment moves to
Room Pass, delete the application's code authority rather than leaving a second door — and
do not have the application read `Room` resources to display a code, because Room read
access is deliberately operator-only.

Also remove the `X-Join-Code` header path in `frontend/src/api/kube.ts` when its server
side goes.

*Done when:* exactly one component decides who is enrolled.

### 10. Deployment coherence

`state-of-the-repo.md` §2 reports two copies of the deployment manifests that have drifted
in opposite directions, and a release process that pushes a mutable `:coffee` tag from a
laptop. CI now publishes `ghcr.io/sunib/voter` and `ghcr.io/sunib/room-pass` and reports a
digest to pin.

Verify the current state, then: one authoritative manifest set in `k8s/`, images
referenced by **digest**, one coherent hostname/routing configuration, and the mutable-tag
laptop push retired.

*Done when:* `kubectl kustomize k8s` renders one deployment that matches what CI
publishes, with no second disagreeing copy in the tree.

### 11. "Admin" names a privilege that does not exist

The coffee admin screen and the `/public/admin/*` routes imply a separate privilege level.
Everyone enrolled may edit the intended coffee configuration — that is the demo. Rename so
the UI and the routes describe what is actually true.

*Done when:* no screen or route name implies an authorization boundary that RBAC does not
enforce.

## What not to do

- **Do not weaken the trust boundary Room Pass establishes.** No second Ingress to Dex, no
  public endpoint that returns identity headers because a caller supplied a nickname, no
  new callback alias. `room-pass/docs/handoff.md` explains why each hop exists.
- **Do not give the audience blanket `edit`, Secrets, workload creation, RBAC, token
  minting or impersonation.** Name the resources. Deny by default. A quota is a resource
  guardrail, not an authorization boundary.
- **Do not enforce in the UI what must be enforced at the API server.** A hidden submit
  button is not a rule; a CLI user ignores it. Where "the owner field equals this user" or
  "only these fields may change" matters, that is admission, not RBAC and not JavaScript.
- **Do not describe the Git result as proof of a person.** It is attribution to an
  enrolled demo identity with an unverified display name and a synthetic `.invalid` email.
  Say the accurate sentence.
- **Do not log the ID token** — in access logs, error messages or a debug endpoint.
- **Do not raise `room-pass` replicas** or add a second enrollment writer.

## Verifying your work

```sh
task lint              # Go, Dockerfiles, workflows, frontend
task test              # unit tests for every component
task test-integration  # envtest — real API server, no cluster
task test-e2e          # k3d + Traefik + Dex; slow, the one that proves login
```

The e2e suite is **not** in CI yet — it is item 1 of the backlog in
[.github/README.md](../.github/README.md). So "CI is green" does not currently mean a
participant can log in. Run `task test-e2e` yourself for anything touching the identity
path, and prefer adding a check to a `Taskfile` over writing logic in workflow YAML.

## Report back with

1. Which gaps you verified as still open, which were already fixed, and which you closed.
2. The two decisions in gaps 1 and 2, as posed to the user, with their answer.
3. Anything in the design documents that the code contradicts — those documents are
   revisable, and a contradiction found in code is worth more than one asserted in prose.
4. What you deliberately left open, and why.
