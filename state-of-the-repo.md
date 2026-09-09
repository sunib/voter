# Where the Voter Repo Actually Stands

Analysis run 2026-09-09 against the working tree at `0eb378d` (main, clean), `origin/coffee`
at `9aa3093`, and `external/gitops-reverser`.

Frontend build and lint were executed. Go tests were **not** executed — there is no Go
toolchain installed on this machine, so `make test-auth-service` cannot run here.

| | |
|---|---|
| Branches | `coffee` ⊇ `main` |
| CI workflows | **0** |
| Deployable manifest sets in-repo | **0 of 2** |
| Frontend build & lint | clean |
| Frontend tests | none |
| auth-service tests | 2,081 lines (coffee) |

---

## 1. The thing that changes everything else

**`coffee` is not a branch beside `main`. It is `main` plus 21 commits.**

```
git rev-list --count origin/coffee..main   →  0
git rev-list --count main..origin/coffee   →  21
git merge-base main origin/coffee          →  0eb378d  (== main HEAD)
```

```
  ●────────●────────◎────────●────────●────────●────────●──▶
                    │                                   │
                  main                                coffee
                 0eb378d                             9aa3093
                    └───────── 21 commits ───────────┘
              impersonation · identity · coffee domain
```

There is not a single commit on `main` that `coffee` lacks. You do not have two projects to
maintain — you have one project whose `main` is a stale snapshot from before the identity
work landed. Everything on `main`, including the quiz demo, also exists on `coffee` in a
better form. Merging is a fast-forward with zero conflicts.

This matters because most of the time you lose is spent reconstructing which branch has
which fix. There is no reconstruction to do.

**The one real loss** going from `main` to `coffee` is the audience `kubectl` path —
`kubeconfig.tmpl`, the `TokenReview` passthrough, and the `/auth/kubeconfig` one-liner were
deliberately removed (see `plans/remove-audience-kubectl-access.md`). That was the most
quotable moment in the old demo. Decide consciously whether you want it back, rather than
letting it survive only because an old branch happens to still hold it.

---

## 2. Three copies of the deployment, none of them agreeing

| Copy | Frontend + `/auth` host | Kube API host | Rate limit | APF | Impersonation hardening | Serves the app? |
|---|---|---|---|---|---|---|
| `voter@main` `k8s/` | `voter.z65.nl` | `vote.reversegitops.dev` | – | – | n/a (shared SA) | **no** |
| `voter@coffee` `k8s/` | `voter.z65.nl` | `coffee.z65.nl` | – | – | yes | **no** |
| `gitops-reverser` `demo-only/` | `demo.configbutler.ai` | `demo.configbutler.ai` | yes | yes | – | **yes** |

The frontend is a static SPA that calls `/apis/…` and `/auth/…` **same-origin**. On both
in-repo manifest sets the frontend is served from one host and the Kubernetes API route is
bound to a different one, so every API call from the loaded page hits a Traefik router with
no matching rule. **Neither `k8s/` directory can serve a working app as written.**

Meanwhile the working copy at
`external/gitops-reverser/test/e2e/setup/demo-only/voter-gitops/` has hardening the source
repo has never seen — a `rateLimit` middleware, `maxResponseBodySize`, and a full
`FlowSchema` / `PriorityLevelConfiguration` pair pinning the quiz service accounts to their
own API Priority & Fairness queue — and simultaneously *lacks* the `strip-client-auth`
middleware the coffee branch depends on for its impersonation model to be safe. The two
copies have drifted in opposite directions.

> **Root cause of your recurring time sink:** the repo you edit is not the repo that
> deploys. Every demo starts with an archaeology session because there is no single artifact
> that is *both* authoritative and known-good. Fixing this one thing recovers more of your
> time than anything else in this document.

---

## 3. The auth-service

Two generations of the same service.

**The `main` generation** (1,311 lines of non-test Go) mints a shared `quiz-access`
ServiceAccount token per browser session, so every audience write lands in the audit log as
one service account. It also carries the `kubectl` passthrough path: a real `TokenReview` on
any inbound bearer token, plus `/public/kubeconfig` handing the audience a working 10-minute
kubeconfig.

**The `coffee` generation** replaces that with **impersonation**, and this is the part
genuinely worth publishing. The ServiceAccount Traefik attaches has *no* CRUD rights at all —
its ClusterRole grants only `impersonate`. The real identity is derived server-side from the
session cookie into `demo:<stableID>` in group `voter-audience`, with display name and email
carried as impersonation extras under `configbutler.ai/claims/…` — which gitops-reverser then
reads off the audit event to author the Git commit. Traefik strips every client-supplied
`Impersonate-*` and `Authorization` header before forward-auth runs; the service rejects
inbound `Authorization` unconditionally as its first action.

That chain — browser cookie → impersonated Kubernetes user → API-server RBAC and audit →
attributed Git commit — is the whole thesis of your talk expressed in about 200 lines of Go
and two RBAC files. It is also, as far as I can tell, not written up anywhere as a reusable
component. **That is the open-source story.**

### What will bite you

#### CRITICAL — There is no admin authentication (coffee)

`requireAdminMiddleware` is defined as `return requireSessionMiddleware(deps, next)` —
verbatim, nothing else. Every `/public/admin/*` route, including
`PATCH /public/admin/coffeeconfig` which rewrites prices, menu and vouchers, is gated only by
"has a valid audience session".

`ADMIN_PASSWORD` is declared in `config.go` with the committed default `testnetcoffee` and
wired into `app.yaml`, but **no code reads it**. Same for `ADMIN_COOKIE_NAME` and
`ADMIN_SESSION_MAX_AGE_SECONDS`. It looks like a control; it is not one.

This may well be what you want for the demo — everyone edits, and the commits show who did
what. But right now it is an accident either way. Make it a decision.

`auth-service/coffee_handlers.go:254` · `auth-service/config.go:19` · `k8s/app.yaml:85`

#### CRITICAL — No rate limiting on the join code (both branches)

Codes are 4 characters from a 36-character alphabet — about 1.7M possibilities — and there is
no throttle on the login path in either branch. On `coffee` the default
`JOIN_CODE_TTL_SECONDS` was raised to `7200s`, so a single code stays valid for two hours.

The `api-ratelimit` Traefik middleware that would blunt this exists *only* in the
gitops-reverser demo copy, and it is attached to the Kubernetes API route, not to `/auth`.
Nothing in the voter repo rate-limits anything.

`auth-service/config.go` · `auth-service/join_codes.go` · `k8s/ingress-auth.yaml`

#### HIGH — Container runs as root on a Debian base (both branches)

The binary is built `CGO_ENABLED=0` and then dropped into `debian:bookworm-slim` purely for
CA certificates. No `USER`, and `app.yaml` sets no `securityContext`, so the pod runs as root
with a writable root filesystem. `gcr.io/distroless/static` or `scratch` with the CA bundle
copied in removes an entire OS worth of CVE surface from an internet-facing component.

`auth-service/Dockerfile:18` · `k8s/app.yaml`

#### HIGH — A chi router is constructed per forward-auth request (main)

`parseSessionRef` builds a fresh `chi.NewRouter()`, registers a route on it, and matches once
— on every single call, on the hot path that fronts the Kubernetes API. It is also the *only*
use of chi in the module. A package-level compiled `regexp` does the same job with no
dependency.

`auth-service/session_ref.go:16-33`

#### HIGH — Coffee-shop domain logic is 43% of the service (coffee)

Of 2,812 non-test Go lines, 1,208 are `coffee_*.go` — orders, vouchers, storefront pricing, a
change-tracking runtime. None of it is authentication. As long as it sits in the same package
as the identity code, the service cannot be extracted, and every change to the demo risks the
auth path.

`auth-service/coffee_handlers.go`, `coffee_logic.go`, `coffee_runtime.go`,
`coffee_change_runtime.go`, `coffee_types.go`

#### MEDIUM — Group, resource and namespace are compiled in (both branches)

`examples.configbutler.ai`, `quizsessions`, `coffeeconfigs` and the URL shape of the session
route are string constants across `kube_client.go` and `session_ref.go`. Fine for a demo; a
hard blocker for anyone else adopting the component. These want to be configuration.

`auth-service/kube_client.go:199, 271, 287` · `auth-service/session_ref.go:25`

#### MEDIUM — Kubernetes libraries are two years behind the Go toolchain (both branches)

`go.mod` declares `go 1.25.0` while pinning `k8s.io/api`, `apimachinery` and `client-go` at
`v0.31.0` (August 2024). Nothing is broken today, but publishing a security-adjacent
component on an old client-go invites the first issue someone files.

`auth-service/go.mod`

---

## 4. The frontends — which are one frontend

There is no second frontend. It is the same `frontend/` directory, with the coffee branch
adding screens on top. Vue 3, Vite 7, Pinia, PrimeVue 4 and Tailwind 4 in both.

I installed and built the `main` frontend: `vue-tsc` and `vite build` pass with no errors,
`eslint .` is silent, output is 435 KB JS / 105 KB gzipped. It is in better shape than the
rest of the repo. Leftovers are minor — `HelloWorld.vue` and `vite.svg` are unreferenced Vite
scaffolding, and `kube.ts` hardcodes namespace `'vote'` with a comment listing four candidate
hostnames.

The coffee branch is where it gets uneven. It gains real structure:

- a router guard that actually checks the session (`beforeEach → getPublicSession`,
  redirecting to `/login` on 401)
- namespace resolved at runtime from `/public/session` instead of hardcoded
- a dev proxy so you can run the SPA locally against `demo.configbutler.ai`

It also gains `AdminScreen.vue` at **1,594 lines** — a single file larger than the entire
`main` frontend's `src/` (1,089 lines), holding config editing, optimistic concurrency,
field-level state markers and change streaming.

**Neither branch has a single test.** No vitest, no Playwright, no component test. For a UI
you drive live in front of an audience, the thing worth having is not unit tests — it is one
Playwright script that walks join → order → submit against the mock plugin you already wrote
(`dev/kube-mock-plugin.ts`), so a broken demo path fails in CI instead of on stage.

---

## 5. The pipelines, bluntly

There are none. No `.github/` directory, no workflow files, no pre-commit hooks, nothing. The
entire release process is:

- `make build-push` from your laptop, straight to `zot.z65.nl`
- tagged `:latest` on `main` and `:coffee` on the coffee branch
- `imagePullPolicy: Always` in the deployment

Which means the cluster runs whatever you last pushed, from whatever working tree you had,
clean or dirty. You clearly already knew — the `GIT_DIRTY` build arg and the startup log line
that prints `commit=… (dirty)` exist precisely because you needed a way to find out what was
running. That is an escape hatch built in place of a fix.

`go test ./...` and `npm run build` run only when you personally run them. The 2,081 lines of
Go tests on the coffee branch (`main_test.go`, `kube_client_test.go`, `identity_test.go`,
`coffee_logic_test.go`) are genuinely good work that nothing executes automatically.

> **You already own the template.** Sitting in `external/gitops-reverser` is a 51 KB `ci.yml`
> with twelve jobs, a `release.yml` driven by release-please with cosign attestations and
> multi-arch retagging, OpenSSF Scorecard, PR-title linting, and `docs/ci-overview.md`
> explaining the trust-zone split. You do not need to learn how to do this. You need to copy it.

---

## 6. What to do, in order

Three phases. The ordering is deliberate — Phase 0 is what stops you losing an afternoon
before each demo, and nothing in Phase 2 is worth starting until Phase 0 holds.

### Phase 0 — Collapse the ambiguity (half a day, do first)

*No new capability. Purely removing the questions you currently have to re-answer every time.*

1. **Fast-forward `main` to `coffee`.** It is a strict superset — zero conflicts. Keep the
   quiz demo as a route inside the one app, not as a branch. If you want the branch name to
   survive for the talk narrative, make `coffee` the default branch instead; what matters is
   that there stops being two heads.
2. **Pick one hostname per environment and make all three routes agree.** Frontend, `/auth`
   and `/apis` must share an origin or the SPA cannot talk to anything. `demo.configbutler.ai`
   already works — adopt it in the repo.
3. **Choose which copy of `k8s/` is real.** Given the whole thesis is that the cluster feeds
   Git, the voter repo should own its manifests and gitops-reverser's
   `demo-only/voter-gitops/` should reference them rather than fork them. Whichever way you
   go, delete the loser.
4. **Port the hardening that only exists in the fork.** The rate-limit middleware,
   `maxResponseBodySize`, and the APF `FlowSchema` belong in the source repo. They are the
   difference between a demo that survives a room of 300 people and one that takes your API
   server down.
5. **Stop tagging `:latest`.** Tag with the short SHA you already compute in the Makefile, and
   set the image by digest in the deployment. Then "what is running" is answerable by reading
   one line.
6. **Resolve the admin question.** Either implement the `ADMIN_PASSWORD` check with a
   constant-time compare, or delete `AdminPassword`, `AdminCookieName`,
   `AdminSessionMaxAgeSecs` and the `app.yaml` env var, and rename `requireAdminMiddleware` to
   say what it actually does.

### Phase 1 — One workflow file (half a day, then leave it alone)

*Deliberately small. A single `ci.yml` that runs on PR and on push to main — not a port of
gitops-reverser's twelve jobs.*

1. **Go job:** `gofmt -l` (fail on output), `go vet ./...`, `go test ./...`. Your tests are
   good; just run them.
2. **Frontend job:** `npm ci`, `npm run lint`, `npm run build`. Both pass today, so this
   starts green and stays honest.
3. **Manifest job:** `kustomize build k8s/` piped through `kubeconform` with the CRD schemas.
   This is the job that would have caught the hostname split.
4. **Publish on main only:** build both images, tag by SHA, push to `ghcr.io` so the
   open-source story has a public home. Keep `zot.z65.nl` as the cluster's pull-through mirror
   — your local registry stays the fast path for test clusters, it just stops being the only
   copy.
5. **One Playwright smoke test** against `dev:mock`: login → order → submit. Guards the exact
   path you walk on stage.

### Phase 2 — Extract the auth-service (after the demo has proven the pattern)

The publishable thing is not "an auth service for a quiz app". It is:

> *A Traefik ForwardAuth service that turns a browser session into a Kubernetes impersonated
> identity, so the API server's own RBAC and audit log become your access control and your
> attribution.*

That is a real, reusable, under-documented pattern.

1. **Split the package first, in place.** Move `coffee_*.go` behind an interface inside the
   current repo before any extraction. If the auth path still compiles and tests pass with the
   coffee files deleted, the seam is real.
2. **Make the CRD coordinates configuration.** Group, version, resource, namespace and the
   session-route pattern become env or a small config file. Nobody can adopt a component
   hardcoded to `examples.configbutler.ai`.
3. **Define the "who gets in" seam.** Join code is one implementation. OIDC is the obvious
   next one, and you already have `plans/auth-service-as-oidc-issuer.md`. An interface here is
   what makes it a component instead of a demo.
4. **Rate limiting in-process,** not only in Traefik, so the component is safe standing alone
   in someone else's ingress.
5. **Distroless, non-root, read-only rootfs,** with a `securityContext` in the shipped
   manifests and chart. A security component that ships as root-on-Debian will be the first
   thing reviewers notice.
6. **Write the integration test that is also the demo:** kind or envtest, make an impersonated
   write, then assert the audit event names `demo:<id>` and carries the display-name extra.
   That single test is the proof of the entire thesis, and it is worth more than any README
   paragraph.
7. **Copy gitops-reverser's CI wholesale** — `ci.yml`, `release.yml`, `scorecard.yml`,
   `pr-title.yml`, plus `CONTRIBUTING.md`, `SECURITY.md`, `CODEOWNERS`. Same shape, same
   conventions, no new thinking required.
8. **Name it properly.** `auth-service` will not do on a company account. Something that says
   what it is: `kube-impersonating-forward-auth`, or `configbutler/forward-auth`. The README's
   first sentence should be the italic line at the top of this phase.

---

## 7. The opinion you asked for

The engineering here is better than its packaging. The impersonation chain on the coffee
branch is genuinely good design — the impersonate-only ServiceAccount, the pinned group, the
header-stripping middleware with its order-matters comment, the fail-closed identity
derivation — and the comments in those files show someone who thought carefully about the
failure modes. The Go tests are real tests. The frontend builds and lints clean, first try,
which is rarer than it should be.

What is missing is entirely structural, and that is good news: none of it requires new ideas.
One branch instead of two. One hostname instead of three. One copy of the manifests instead of
three. One workflow file instead of none. Every item in Phase 0 and Phase 1 is deletion or
copying, not design.

On the open-source ambition — the instinct is right, and the pattern deserves to exist as a
component. But do not start Phase 2 before the demo is boring to run. The extraction is a
large, satisfying, easily-rationalised distraction, and it will be far easier once you have run
the thing in front of a room and know which parts of the story actually landed.
