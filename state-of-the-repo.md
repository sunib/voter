# State of the Voter Repo

Written 2026-09-09 against `main` at `dfb37a4` (clean working tree) and the pinned
checkout at `external/gitops-reverser` (release 0.44.0).

This is a source and local-build review, not a live deployment audit. Nothing was
deployed, pushed, or inspected in a running cluster. It consolidates two independent
reviews of the same tree into a single document and describes only what exists **now**.

## Verification performed

| Check | Result |
|---|---|
| `npm ci` + `eslint .` + `vue-tsc -b && vite build` (frontend) | **Pass.** 480.15 kB JS / 117.39 kB gzip, 221 modules |
| `npm audit` (frontend) | 15 advisories — 1 low, 4 moderate, 10 high (all dev-time deps) |
| `kubectl kustomize k8s` | **Pass.** 989 lines rendered |
| Docker `COPY *.go *.tmpl` glob with no `.tmpl` present | **Not a failure** — BuildKit tolerates it as long as one pattern matches |
| Go build / vet / test | **Not run** — no Go toolchain on this machine |
| Container image build, browser automation, cluster/audit/Git end-to-end | Not run |
| Registry and deployed-image inspection | Deliberately not performed |

A stale `node_modules` will fail the build with `Cannot find module
'@vitejs/plugin-basic-ssl'`. The dependency *is* declared in `package.json` and
`package-lock.json`; the failure is local install staleness, not a repo defect. Run
`npm ci` and it passes.

---

## 1. What you have

One trunk. One application. Two demo journeys inside it.

| | |
|---|---|
| Branches that matter | `main` |
| CI workflows | **0** |
| Deployable manifest sets | **2**, in two repos, disagreeing |
| Go source (`auth-service`) | 2,812 non-test lines + 2,081 test lines |
| Frontend | Vue 3, Vite 7, Pinia, PrimeVue 4, Tailwind 4 |
| Frontend tests | **0** |
| Release process | `make build-push` from a laptop to `zot.z65.nl`, tag `:coffee` |

The application is a coffee storefront (order screen, admin editor, orders view, commits
view) plus the surviving questionnaire screens (`/answer/:session`, `/thanks`). Both are
fronted by the same auth-service and the same identity model.

### The impersonation chain — the part worth publishing

```
browser cookie  →  server-derived identity  →  impersonated Kubernetes user
                →  API-server RBAC + audit  →  attributed Git commit
```

The ServiceAccount Traefik attaches (`voter-audience-impersonator`) has **no** CRUD
rights on anything. Its ClusterRole grants only `impersonate`. The real identity is
derived server-side from the session cookie into `demo:<stableID>` in group
`voter-audience`, with display name and email carried as impersonation extras under
`configbutler.ai/claims/…` — which gitops-reverser then reads off the audit event to
author the Git commit. Traefik's `strip-client-auth` middleware drops every
client-supplied `Impersonate-*` and `Authorization` header *before* forward-auth runs,
and the auth-service rejects inbound `Authorization` unconditionally as its first action.

That whole chain is about 200 lines of Go and two RBAC files
([k8s/audience-impersonator-rbac.yaml](k8s/audience-impersonator-rbac.yaml),
[k8s/auth-service-rbac.yaml](k8s/auth-service-rbac.yaml)). It is the thesis of the talk,
expressed as running code, and as far as either review could tell it is not written up
anywhere as a reusable component. **That is the open-source story.**

The design is careful in the right places: the audience Role deliberately withholds
`patch`/`update` on `coffeeconfigs` from the auth-service SA so there is no silent
fallback-to-SA path, empty impersonation usernames are a hard error rather than a skip,
and the middleware ordering carries an ORDER-MATTERS comment explaining why. The
comments in those files read like someone who thought about the failure modes.

---

## 2. The repo you edit is not the repo that deploys

There are two copies of the deployment and they have drifted in opposite directions.

| | `voter/k8s/` (this repo) | `gitops-reverser` `demo-only/voter-gitops/` |
|---|---|---|
| Frontend + `/auth` host | `voter.z65.nl` | `demo-test` / `demo.configbutler.ai` |
| Kubernetes API host | `coffee.z65.nl` | same host as frontend ✅ |
| `FORWARD_SA` | `voter-audience-impersonator` | **`quiz-access`** ❌ |
| Impersonation RBAC | present | **absent** ❌ |
| `authResponseHeaders` | `Authorization` + all `Impersonate-*` | **`Authorization` only** ❌ |
| `strip-client-auth` middleware | present | **absent** ❌ |
| Rate limit / `maxResponseBodySize` | **absent** ❌ | present |
| Image tags | `:coffee` (mutable) | `:coffee` (mutable) |

Two independent problems fall out of that table.

**In-repo, the SPA cannot talk to its own API.** The frontend is a static SPA that calls
`/apis/…` and `/public/…` **same-origin**. `k8s/ingress-frontend.yaml` and
`k8s/ingress-auth.yaml` bind `voter.z65.nl`; `k8s/ingress-kubeapi.yaml` binds
`coffee.z65.nl`. Every Kubernetes API call from the loaded page hits a Traefik router
with no matching rule, and the frontend catch-all may return HTML where the client
expects JSON. `kustomize build` passes — Kustomize can assemble semantically
inconsistent hostnames all day. A schema validator will not catch this either.

**In the external copy, the manifests do not match this binary.** They select `:coffee`
images but still configure `FORWARD_SA=quiz-access` with the old direct-CRUD RBAC, copy
only `Authorization` out of forward-auth, and never install the impersonator account or
the audience group RoleBinding. Against the current auth-service, server-side
CoffeeConfig patches lack the impersonation authorization they need, and direct API
requests through Traefik lose participant attribution entirely. Both overlays render
successfully — again, evidence that Kustomize works, not that the contract holds.

Mutable tags mean you also cannot determine which source revision a running cluster is
serving from these files alone.

> **This is the recurring time sink.** Every demo starts with an archaeology session
> because there is no single artifact that is both authoritative and known-good. Fixing
> this one thing recovers more of your time than anything else in this document.

The fix is a single versioned application deployment base in *this* repo — service
accounts, RBAC, routes, image identities — with the external project supplying only
environment values and demo-specific Flux/ConfigButler resources.

---

## 3. Security findings

These are grouped by whether they are a decision you have already made, a real defect,
or a boundary that needs documenting.

### 3.0 Intentional: every participant is an admin

**This is by design and is not a leak.**

`requireAdminMiddleware` ([auth-service/coffee_handlers.go:254](auth-service/coffee_handlers.go#L254))
delegates straight to `requireSessionMiddleware`, and the `voter-audience` Role grants
`patch`/`update` on `coffeeconfigs`
([k8s/auth-service-rbac.yaml:99-101](k8s/auth-service-rbac.yaml#L99-L101)). Both layers
agree: anyone who joins the coffee demo can edit prices, menu items and vouchers.

That is the point of the demo. The audience is *supposed* to be able to change the
storefront, because the payoff is that every change lands in Git attributed to the person
who made it. Locking the editor down would remove the thing the talk is demonstrating.
The RBAC and the handler are consistent with each other, so this is a coherent policy
rather than a gap — worth saying out loud in the README so a reader does not file it as a
vulnerability.

**What is genuinely wrong here is the vestigial config.** `ADMIN_PASSWORD` (committed
default `testnetcoffee`), `ADMIN_COOKIE_NAME` and `ADMIN_SESSION_MAX_AGE_SECONDS` are
declared in [auth-service/config.go:17-19](auth-service/config.go#L17-L19) and **no code
reads any of them**. They look like a control and are not one. Delete all three fields
and rename `requireAdminMiddleware` to something honest like `requireParticipantMiddleware`,
or rename the routes from `/public/admin/*` to `/public/editor/*`. A password that exists
only in config is worse than no password, because the next reader will assume it works.

Note for whenever this *does* need a real operator role: adding a password check to the
handler alone would not be enough. The direct Kubernetes API route is also open to
audience identities with write permissions, so the boundary has to be enforced in both
the handlers and in Kubernetes authorization.

### 3.1 HIGH — Impersonation privileges are broader than the comments imply

Both impersonation ClusterRoles grant unrestricted `impersonate` on `users`. The `demo:`
prefix is enforced by **application code**, not by RBAC. The group *is* pinned to
`voter-audience` via `resourceNames`, which is good, but a leaked impersonator-SA token
combined with a direct RoleBinding lets the holder choose any username they like.

The comment in [k8s/audience-impersonator-rbac.yaml](k8s/audience-impersonator-rbac.yaml)
says "if this SA token leaks, the holder can impersonate audience users but cannot do
anything as the SA itself." The second half is true. The first half is not — nothing at
the RBAC layer confines the impersonated username to audience identities. Kubernetes
authorizes impersonated attributes separately and supports exact `resourceNames`
restrictions on usernames; the shipped rules do not use them
([upstream docs](https://kubernetes.io/docs/reference/access-authn-authz/user-impersonation/)).

For a deliberately isolated demo cluster this is an acceptable, explicit constraint. For
a published reusable component it is a release blocker. Either narrow the rule (which
means committing to an enumerable username set) or document the blast radius plainly.

### 3.2 HIGH — Identity is self-asserted

The browser generates a six-digit ID with `Math.random()`
([frontend/src/lib/demoIdentity.ts](frontend/src/lib/demoIdentity.ts)) and sends it, plus
display name and email, to `/public/login`. The server validates their *shape* and signs
them into a cookie. Anyone with the demo code can submit another participant's ID or an
arbitrary well-formed email. Signing a claim protects it after login; it does not
establish who supplied it. The six-digit space also allows accidental collisions in a
room of a few hundred.

This is fine for an anonymous attribution demonstration and should be labelled as such.
It is not verified identity, not collision-resistant issuance, and not a foundation for
per-person privileges. For the reusable component: issue a sufficiently random
**server-owned** subject, mark display name and email as unverified, and put a verified
identity provider behind the same interface as a second adapter.

### 3.3 HIGH — No rate limiting on the login path

Join codes are 4 characters from a 36-character alphabet — 1,679,616 possibilities — and
`JOIN_CODE_TTL_SECONDS` defaults to `7200s`, so one code stays valid for two hours
([auth-service/config.go:21-23](auth-service/config.go#L21-L23)). Code generation itself
is correct (`crypto/rand`, [auth-service/join_codes.go:151-164](auth-service/join_codes.go#L151-L164));
the problem is that nothing throttles guesses.

Nothing in this repo rate-limits anything. The `api-ratelimit` middleware exists **only**
in the external demo copy, it is attached to the Kubernetes API route rather than to
`/public/login`, and its `average: 10000` is not a login policy by any reading.

Rate-limit the actual login route. Keep short hand-typed codes only with deliberate
expiry, and avoid permanent lockouts — one audience member should not be able to lock the
room out.

### 3.4 HIGH — Unbounded request bodies and unbounded order retention

The CoffeeConfig PATCH handler reads the whole body with `io.ReadAll`
([auth-service/coffee_handlers.go:131](auth-service/coffee_handlers.go#L131)) with no
`http.MaxBytesReader`. Login and order decoding have no explicit cap either. Orders
accumulate in an unbounded slice for the process lifetime, rejected orders included
([auth-service/coffee_runtime.go:69-70](auth-service/coffee_runtime.go#L69-L70)).

These are concrete resource paths to bound before a room of users — or hostile traffic —
reaches the app. Cap bodies, cap retained order records, and validate duration/size
settings at startup.

Worth correcting a plausible misreading: the external `maxResponseBodySize: 65536` bounds
the **auth server's response** to Traefik, not incoming application request bodies
([Traefik ForwardAuth docs](https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/forwardauth/)).
It does nothing for this finding.

### 3.5 HIGH — The editor detects conflicts visually but does not prevent lost updates

`AdminScreen.vue` saves the entire draft `spec` and sends no `metadata.resourceVersion`
precondition. The backend reads the current object to build its history entry, then
forwards the merge patch as a separate unconditional call —
`ResourceVersion` appears in `kube_client.go` only on the **watch**
([auth-service/kube_client.go:475](auth-service/kube_client.go#L475)), never on a write.

Two browsers can load the same config, edit different fields, and both save before the
SSE updates arrive. The second full-spec save overwrites the first. Products and vouchers
are arrays, which widens the amount silently replaced by a stale save. The field-level
markers are good UX; they are not server-enforced optimistic concurrency.

Given §3.0 — everyone is an editor on purpose — this is the finding most likely to bite
you live, in front of the audience. Carry the edited baseline's version into a
conditional write and return Kubernetes conflicts as HTTP 409. Note that `writeKubeError`
currently preserves only `NotFound` and maps everything else to 500, so both sides need
work.

### 3.6 HIGH — auth-service container runs as root on a Debian base

The binary is built `CGO_ENABLED=0` and then dropped into `debian:bookworm-slim` purely
for CA certificates ([auth-service/Dockerfile](auth-service/Dockerfile)). No `USER`, and
`k8s/app.yaml` sets no `securityContext` at all — so the pod runs as root with a writable
root filesystem, for an internet-facing component. `GOARCH=amd64` is also hardcoded.

`gcr.io/distroless/static` or `scratch` with the CA bundle copied in removes an entire
OS worth of CVE surface. Add `runAsNonRoot`, drop capabilities, and set a read-only root
filesystem in the manifests.

The frontend image already does this right — `nginxinc/nginx-unprivileged:stable-alpine`.
Only auth-service is the outlier.

### 3.7 MEDIUM — 15 npm advisories in the frontend toolchain

`npm audit` reports 1 low, 4 moderate and 10 high, including a `vite` entry and a `yaml`
stack-overflow advisory. These are dev-time dependencies, not shipped in `dist/`, so the
exposure is to your build machine rather than to the audience. Still worth an
`npm audit fix` pass before anyone else clones the repo.

### 3.8 MEDIUM — Coordinates and blast radius are compiled in

`examples.configbutler.ai`, `quizsessions`, `coffeeconfigs` and the shape of the session
route are string constants across
[auth-service/kube_client.go](auth-service/kube_client.go) and
[auth-service/session_ref.go:25](auth-service/session_ref.go#L25). Fine for a demo; a hard
blocker for anyone adopting the component, and it means the auth-service's broad
namespace-secret permissions and cluster-wide demo reads cannot be narrowed without a
code change.

### 3.9 MEDIUM — Kubernetes client libraries are a year behind

`go.mod` declares `go 1.25.0` while pinning `k8s.io/api`, `apimachinery` and `client-go`
at `v0.31.0` (August 2024). Nothing is broken today, but publishing a security-adjacent
component on an old client-go invites the first issue someone files.

### 3.10 Not a security finding — readiness proves almost nothing

The readiness probe proves the process serves `/healthz`. It does not prove that
impersonated writes work, that the impersonator token can be minted, or that Git commits
land. Keep liveness simple and add a separate preflight command that checks dependencies
and permissions.

Related: the external APF policy (`demo-only/vote/apf.yaml`) pins `auth-service` and
`quiz-access` in namespace **`vote`**. It gives no protection to the
`voter-test`/`voter-production` identities the coffee demo actually runs as.

---

## 4. Features lost, or surviving only in history

Nothing here is broken — these are capabilities that were deliberately or incidentally
dropped and are now recoverable only from Git. Worth a conscious decision rather than
quiet attrition.

### 4.1 The `kubectl` path for tech-savvy audience members — the one worth reconsidering

Removed in `aaf3865` ("Simpify voter architecture"), planned in
[plans/remove-audience-kubectl-access.md](plans/remove-audience-kubectl-access.md).

What went away:

| Piece | Recover from |
|---|---|
| `GET /public/kubeconfig` (public as `/auth/kubeconfig?code=XXXX`) | `git show aaf3865^:auth-service/http_handlers.go` |
| `auth-service/kubeconfig.tmpl` — 24-line kubeconfig template | `git show aaf3865^:auth-service/kubeconfig.tmpl` |
| `TokenReview` bearer-passthrough branch in `/private/forward-auth-decision` | `git show aaf3865^:auth-service/http_handlers.go` |
| `quiz-access` ServiceAccount / Role / RoleBinding | `git show 0eb378d:k8s/quiz-rbac.yaml` |
| `KUBECONFIG_SA`, `KUBECONFIG_SA_NAMESPACE` plumbing | `git show aaf3865^:auth-service/config.go` |
| `plans/audience-kubectl-access.md` — the original 332-line design | `git show 0eb378d:plans/audience-kubectl-access.md` |
| The `/public/kubeconfig` test cases (~130 lines of `main_test.go`) | `git show aaf3865^:auth-service/main_test.go` |

The demo moment it enabled:

```sh
export KUBECONFIG=<(curl -s "https://<host>/auth/kubeconfig?code=XXXX")
kubectl get quizsessions.examples.configbutler.ai -n voter
```

The template also shipped its own crib sheet in comments, and the token was minted with a
~10-minute TTL.

**Why it was removed, honestly:** it directly contradicted the security invariant the
impersonation model is built on — *no user-supplied bearer token ever reaches the API
server*. Worse, the config had a fallback: if `KUBECONFIG_SA` was unset, the endpoint
minted the **impersonator SA's** token and handed it to a browser. That is the footgun
the removal plan exists to close, and removing it was right.

**But the feature is worth having back**, and it can come back without reopening any of
that. It is the single most quotable moment in the demo — an audience member with a
laptop reaching a real API server, seeing real RBAC, in one line — and it is a strictly
better story now than it was then, because with impersonation you can hand out a
kubeconfig that authenticates *as that person* rather than as a shared service account.

Two ways to do it safely, in increasing order of effort:

1. **Per-identity `TokenRequest` with a bound audience.** Mint a short-lived token for a
   per-participant ServiceAccount (or an impersonation-bound proxy credential), scoped to
   read-only RBAC, and never for the impersonator SA. Serve it from a route that requires
   an established session, and keep `strip-client-auth` on the browser route untouched —
   the kubectl path gets its **own** ingress route with its own middleware chain, so the
   two trust models never share a router.
2. **OIDC.** You already have [plans/auth-service-as-oidc-issuer.md](plans/auth-service-as-oidc-issuer.md).
   `kubectl oidc-login` against the auth-service is the version of this that generalizes,
   needs no token handout at all, and makes the identity story identical for browser and
   CLI. It is also considerably more work, and becoming an OIDC *issuer* is a separate
   product commitment from merely *accepting* identity from a provider.

Either way: a separate route, read-only RBAC, per-identity subject, and an explicit
non-negotiable that the impersonator SA token is never returned to a client.

### 4.2 The old questionnaire entry URLs

The questionnaire *screens* are still here, but the routes changed shape:

| Then (`0eb378d`) | Now |
|---|---|
| `/` → redirect to `/join` | `/` → coffee order screen |
| `/join/:session?` | `/login` (`/join` redirects, but drops the `:session` param) |
| `/s/:session/answer` | `/answer/:session` |
| `/s/:session/thanks` | `/thanks` |
| catch-all → `/join` | catch-all → `/` |

Any printed QR code or shared link in the old format now hits the catch-all and lands on
the coffee order screen. Decide which QR formats you support, add redirects for the rest,
and test them explicitly.

### 4.3 `JoinScreen.vue` is orphaned

[frontend/src/screens/JoinScreen.vue](frontend/src/screens/JoinScreen.vue) is no longer
imported by the router or any component — the only remaining references are in docs and
plans. It is either the seed of a per-session questionnaire entry route (see §4.2) or
dead code. Pick one.

### 4.4 Hardening that exists only in the external fork

`rateLimit`, `maxResponseBodySize`, and the APF `FlowSchema` /
`PriorityLevelConfiguration` pair live only in `gitops-reverser`'s demo copy and have
never been in this repo. See §2 and §3.3 — they belong here, retargeted at the
identities and routes this app actually uses.

### 4.5 Stale references left behind

- `auth-service/Dockerfile` still does `COPY *.go *.tmpl ./`, and there is no longer any
  `.tmpl` file. Harmless (BuildKit accepts it while another pattern matches — verified),
  but misleading.
- `k8s/ingress-auth.yaml:26` still carries the comment "*probably not the 'long' term
  solution since kubectl needs access to /apis…*", describing a path that no longer
  exists.
- `docs/nieuwe-login.md` is still in Dutch, after `e1b5465` ("getting the last dutch out
  of here").

---

## 5. The save-message integration targets the wrong ConfigButler API

Demo blocker for the "save with my message" story, and the one finding that is a plain
version mismatch rather than a judgment call.

[auth-service/kube_client.go:413](auth-service/kube_client.go#L413) constructs
`configbutler.ai/v1alpha1` CommitRequests, and `commitRequestGVR()` at line 430 sends
them to the same version. The pinned controller's
`config/crd/bases/configbutler.ai_commitrequests.yaml` serves **`v1alpha3` only**.

There is a target mismatch too: the app defaults `CONFIGBUTLER_GIT_TARGET_NAME` to
`voter-demo`, while the external test overlay creates a GitTarget named
`demo-coffeeconfig` and does not override the app setting. That overlay's commit window
is `0s`, which needs reconciling with a design that patches first and then asks to
finalize an open window.

The failure mode is quiet: the CoffeeConfig PATCH can succeed while CommitRequest
creation fails. The handler logs the failure and returns success, and the recent-history
entry records the patch independently. That partial-success behaviour is defensible for
an already-applied write — but it means a green save cannot establish that Git contains
the requested message or author.

The existing fake-HTTP tests explicitly assert the **old** API version
([auth-service/kube_client_test.go:272](auth-service/kube_client_test.go#L272)), so they
will protect the wrong contract indefinitely.

Align the served API, target name, namespace and commit-window behaviour with the pinned
controller release. If the UI promises both, surface "configuration saved" and "Git
commit confirmed" as separate states. Then add an integration test that checks the
resulting Git object, author and message.

---

## 6. The frontend

Small, understandable, and in better shape than the rest of the repo. Clean `npm ci`,
lint and build, first try. Runtime namespace discovery via `/public/session`,
session-aware navigation with a real router guard (`beforeEach → getPublicSession`,
redirect to `/login` on 401), SSE streams, field-level conflict indicators. This code
merits incremental improvement, not a rewrite.

The one structural concern is `AdminScreen.vue` at ~1,594 lines, where editing,
merge/conflict logic, subscriptions and rendering all share a file. Extract its state
transitions and write behaviour into testable modules and divide the form into sections —
preserving the existing behaviour while adding the server-enforced concurrency from §3.5.

**There is not a single test.** No vitest, no Playwright, no component test. For a UI you
drive live in front of an audience, what is worth having is not unit tests — it is a
browser script that walks the exact path you walk on stage, so a broken demo fails in CI
instead of in the room.

Three gaps matter more than any cosmetic cleanup:

1. **The mock cannot run the coffee journey.** `dev/kube-mock-plugin.ts` only intercepts
   paths under `/apis/examples.configbutler.ai/v1alpha1/` — it handles no `/public/login`,
   `/public/session`, storefront or order APIs, so a request to `/public/session` returns
   the SPA's HTML with status 200. The quiz mock also looks under
   `dev/fixtures/quizsessions` while the committed fixture sits in
   `dev/fixtures/questionnairesessions`, so that request 404s. **Repair these contracts
   before writing a Playwright test "against the existing mock."**
2. **Old questionnaire links need migration** — see §4.2.
3. **Live-session rules are UI-only.** The quiz store keeps an already-loaded session and
   the answer screen checks that cached state. The forward-auth path does not check the
   referenced quiz state at all. If closing a quiz must prevent submissions, enforce it
   server-side or in admission and test direct API requests — hiding a submit button is
   not a rule.

Priority browser coverage: quiz login/submission, coffee login/order, simultaneous editor
saves, expiry/re-login, stream disconnect/recovery. Mock tests establish frontend
behaviour; they cannot establish Kubernetes authorization or Git attribution.

---

## 7. Pipelines and releases

There are none. No `.github/`, no workflow files, no pre-commit hooks. The entire release
process is:

- `make build-push` from your laptop, straight to `zot.z65.nl`
- tagged `:coffee`, `imagePullPolicy: Always`
- no test gate of any kind

The cluster runs whatever you last pushed, from whatever working tree you had, clean or
dirty. You clearly already knew — the `GIT_DIRTY` build arg and the startup log line that
prints `commit=… (dirty)` exist precisely because you needed a way to find out what was
running. That is an escape hatch built in place of a fix, and it is a genuinely useful
starting point for a real release record.

The 2,081 lines of Go tests (`main_test.go`, `kube_client_test.go`, `identity_test.go`,
`coffee_logic_test.go`, `coffee_change_runtime_test.go`) are real work that nothing
executes automatically.

`external/gitops-reverser` contains a 51 KB `ci.yml` with twelve jobs, a `release.yml`
driven by release-please with cosign attestations and multi-arch retagging, OpenSSF
Scorecard, PR-title linting, and a `docs/ci-overview.md` explaining the trust-zone split.
Use it as a source of **conventions** — particularly the separation of untrusted
validation from publishing — rather than copying it wholesale. Its controller-specific
jobs, images and permissions would be maintenance work unrelated to this app.

---

## 8. Operational behaviour worth documenting now

Coffee orders, voucher usage and the change history are all **process memory**.
Restarting resets orders, IDs and voucher consumption. `replicas: 1` on both Deployments
is therefore load-bearing, not a capacity choice: multiple replicas would each keep
independent voucher counters and, with generated codes, independent access-code stores.
Even a rolling update of a single-replica Deployment briefly overlaps old and new
processes.

Treat this as an explicitly ephemeral, single-instance demo until that state is
externalized. Decide whether a rollout is *supposed* to reset it, and write that down. Do
not add replicas as an availability improvement without addressing the semantics first.

---

## 9. Where GitOps Reverser fits

The external repository supplies the environment and the actual Git workflow.
`test/e2e/Taskfile-e2e.yml:681` defines `test-e2e-demo`, which prepares the demo and
intentionally leaves resources behind — a setup runner, not a measurable test suite.

The coffee test overlay watches CoffeeConfig changes into the demo Git repository's
`demo-test` branch under `voter-coffee`; Flux consumes that branch for test. The
production overlay consumes `main` and maps the recorded test-namespace config into
production. **Promotion through Git is part of the demo**, not an incidental deployment
detail.

The decisive acceptance test therefore follows the whole path: a browser edits config →
Kubernetes records the intended identity → ConfigButler creates the expected Git change →
the intended Flux environment reconciles it. An application build and a controller test
suite can both pass while that combined story is broken.

Keep the environment orchestration there. Own application behaviour and its base
deployment here. Pin the interface between the repositories: application image digests,
deployment-base revision, controller version and API, environment settings.

---

## 10. What to do, in order

### Step 1 — Establish a known demo baseline

The edits are small; proving them across Traefik, Kubernetes, ConfigButler and Flux is
the work.

1. **Pick one hostname per environment and make all three routes agree.** Frontend,
   `/public`/`/auth` and `/apis` must share an origin or the SPA cannot talk to anything.
2. **Fix the deployment identity mismatch.** One versioned base in this repo owning
   service accounts, RBAC, routes and images; the external project supplies environment
   values only. Delete the fork's copy of what it no longer owns.
3. **Fix the CommitRequest API version and GitTarget name** (§5), and update the tests
   that currently assert the old version.
4. **Port the hardening that exists only in the fork** (§4.4), retargeted at the routes
   and identities this app actually uses — and rate-limit `/public/login`, not just the
   API route.
5. **Resolve the admin naming.** Keep the everyone-can-edit policy; delete
   `AdminPassword`, `AdminCookieName`, `AdminSessionMaxAgeSecs` and the `app.yaml` env
   var; rename the middleware and routes to say what they do. Document the policy as
   intentional in the README.
6. **Add the conditional write** (§3.5) — this is the one that fails live.
7. **Stop tagging mutable images.** Tag with the short SHA you already compute, set the
   image by digest, and keep a known rollback pair.
8. **Write a short runbook**: start, verify, display code, reset, stop, rollback.

**Complete when:** a clean environment, using recorded image digests and manifest
revisions, completes both demo journeys and produces the expected attributed Git change —
and someone else can reproduce that state without reading a chat transcript.

### Step 2 — A small required CI gate around that baseline

| Gate | What it establishes |
|---|---|
| `gofmt -l`, `go vet ./...`, `go test ./... -race` | Cookies, caches, identity, concurrent runtime behaviour |
| `npm ci`, lint, build | Reproducible install, type-check, compilation |
| Container builds | Dockerfiles package the intended sources for supported architectures |
| `kustomize build` + `kubeconform` | Deployment structure is valid |
| **Explicit rendered-manifest assertions** | Hostnames, namespace, account names, identity forwarding and API versions agree |
| Browser smoke tests | Both journeys work through their supported entry URLs |
| Disposable-cluster integration | Real RBAC, Traefik headers, audit identity and ConfigButler Git output work together |

Start with the fast checks today — they pass now, so CI starts green and stays honest.
The rendered-manifest assertions are the row that catches §2; schema validation alone
never will. Repair the mock (§6) before promising a browser test.

**Complete when:** the required checks reject a broken quiz route, an incorrect
impersonation configuration, a stale concurrent save, and an incompatible CommitRequest
version.

### Step 3 — Make releases and deployment state inspectable

Build a matched frontend/auth image pair from a clean, tested commit and publish
immutable identities. Keep `zot.z65.nl` — changing registry provider is not required to
fix traceability, though a public mirror helps the open-source story later. Update
digests together, retain a known rollback pair, build once and promote the same
artifacts.

Extend the existing SHA/dirty metadata into a small release record relating frontend
digest, auth digest, source SHA, deployment revision and tested controller version. A
status/preflight command should print those and verify routing, login, resource access
and Git completion without changing anything.

**Complete when:** "what is running, what was tested, and how do I restore the previous
demo?" have short, reproducible answers.

### Step 4 — Extract the reusable component (after the demo is boring to run)

The publishable thing is not "an auth service for a quiz app". It is:

> *A browser-session-to-Kubernetes-identity gateway: a Traefik ForwardAuth service that
> turns a browser session into an impersonated Kubernetes identity, so the API server's
> own RBAC and audit log become your access control and your attribution.*

| Reusable component | Demo application |
|---|---|
| Session issuance, validation, expiry, key handling | Quiz join-code discovery and lifecycle |
| Trusted identity input → Kubernetes subject mapping | Coffee orders, prices, vouchers, history |
| Token acquisition/cache, forward-auth decision | CoffeeConfig editing and frontend screens |
| Header trust contract and deployment policy examples | ConfigButler CommitRequest orchestration |
| Generic logging, config validation, health behaviour | Demo claim names and GitTarget config |

Sequence:

1. **Create the boundary in place first.** `coffee_*.go` is 1,208 of 2,812 non-test lines
   — 43% of the service, none of it authentication. Move it behind an interface inside
   this repo. If the auth path still compiles and its tests pass with the coffee files
   deleted, the seam is real. The generic core must not need CoffeeConfig types or quiz
   resource names to compile.
2. **Make the coordinates configuration** (§3.8): group, version, resource, namespace,
   session-route pattern. Narrow the auth-service's broad namespace-secret permissions and
   cluster-wide reads as part of shedding demo responsibilities.
3. **Define the "who gets in" seam.** Anonymous join-code is one adapter; accepting
   identity from an OIDC provider is the next. Becoming an OIDC *issuer* is a separate
   commitment — see §4.1.
4. **Rate-limit in-process**, not only in Traefik, so the component is safe standing in
   someone else's ingress.
5. **Distroless, non-root, read-only rootfs**, with `securityContext` in the shipped
   manifests and chart (§3.6).
6. **Document the impersonation blast radius honestly** (§3.1) and distinguish verified
   from anonymous identities in the API (§3.2).
7. **Write the integration test that is also the demo:** kind or envtest, make an
   impersonated write, then assert the audit event names `demo:<id>` and carries the
   display-name extra. Include negative authorization cases — denied operations through
   the real proxy. That test is the proof of the entire thesis and is worth more than any
   README paragraph.
8. **Ship the release scaffolding:** runnable example, supported version matrix,
   `CONTRIBUTING.md`, `SECURITY.md`, `CODEOWNERS`, license and module-path cleanup,
   session/key lifecycle guidance.
9. **Name it properly.** `auth-service` will not do on a company account. Something like
   `kube-impersonating-forward-auth` or `configbutler/forward-auth`, with the italic line
   above as the README's first sentence.

**Complete when:** an unrelated example CRD can use the component without changing its
source, verified and anonymous identities are clearly distinguished, and the public
integration test demonstrates both allowed and denied operations through the real proxy.

---

## 11. The opinion

The engineering is better than its packaging. The impersonation chain is genuinely good
design — the impersonate-only ServiceAccount, the pinned group, the header-stripping
middleware with its order-matters comment, the fail-closed identity derivation, the
deliberate refusal to give the SA `patch` on `coffeeconfigs`. The Go tests are real
tests. The frontend builds and lints clean on a fresh install, which is rarer than it
should be.

What is missing splits cleanly in two, and the split matters for how you plan.

**Structural work needs no new ideas.** One hostname instead of three. One copy of the
manifests instead of two. One workflow file instead of none. Immutable tags. Every item
there is deletion, copying or renaming.

**Contract work needs real proving.** The CommitRequest version mismatch, the external
manifests' stale identity configuration, the missing conditional write, and the mock's
missing routes are each small edits with a large verification tail. Neither Kustomize
rendering nor a build-only CI would have caught any of them, and they are the reason a
demo that "should work" doesn't. Budget days for this, not an afternoon — the edits are
small, but proving two journeys across Traefik, Kubernetes, ConfigButler and Flux is the
work that determines readiness.

Two things to hold onto. First: the everyone-is-an-editor policy is a feature, and the
demo is better for it — just make the code say so, and add the conditional write so
simultaneous editing degrades into a visible conflict instead of a silent overwrite.
Second: bring the `kubectl` moment back, per-identity this time. It was the most quotable
thing in the old demo and impersonation makes it a strictly better story than it was
before.

On the open-source ambition: the instinct is right and the pattern deserves to exist as a
component. But do not start Step 4 before the demo is boring to run. The extraction is a
large, satisfying, easily-rationalised distraction, and it will be far easier once you
have run the thing in front of a room and know which parts of the story actually landed.
