# State of the repository — Codex review

Reviewed 9 September 2026: `main` at `0eb378d`, locally available `origin/coffee` at `9aa3093`, and `external/gitops-reverser` at `61ec733f` (release 0.44.0). I read Claude’s [review](state-of-the-repo.md), then checked the code and deployment files independently.

**My assessment: the demos have useful implementation work behind them, but there is no tested contract tying application source, images, manifests and the ConfigButler controller together. Establish that contract before extracting the auth-service.** Several concrete incompatibilities already cross those boundaries; adding build CI alone will not catch them.

This is a source and local-build review, not a live deployment audit. I did not inspect the registry, change branches, push images, deploy resources or modify application code. Existing `.gitignore` edits and Claude’s report were left alone. References prefixed `coffee:` refer to files at `9aa3093`; they are not necessarily present in the current main checkout. External paths below are relative to `external/gitops-reverser/`.

## What you have

| Area | Main | Coffee | Assessment |
| --- | --- | --- | --- |
| Application | Questionnaire SPA | Coffee storefront/editor plus questionnaire screens | Two demo experiences sharing an evolving codebase |
| Browser authentication | Join code resolves a quiz session; cookie stores session reference | Global demo code; signed cookie stores participant identity | A changed authentication contract, not just added screens |
| Kubernetes access | Shared `quiz-access` token; bearer passthrough and downloadable kubeconfig | Shared impersonator token plus participant headers; server-side impersonated coffee writes | Coffee is the better foundation for the reusable component |
| Frontend | Vue, TypeScript, Pinia, PrimeVue, Tailwind | Same stack, additional screens and SSE streams | Both compile; neither has automated browser tests |
| Deployment | Loose manifests, mutable `latest` images | Kustomize entry point, mutable `coffee` images | Both need integration fixes |
| CI in this repository | No tracked GitHub workflows | No tracked GitHub workflows | Local Make targets are the release process |
| ConfigButler integration | Questionnaire resources can enter the external demo flow | CoffeeConfig writes plus a CommitRequest side effect | Source and controller API disagree today |

Coffee is 21 commits ahead and zero behind main. That makes a fast-forward mechanically possible. It does **not** prove functional compatibility: coffee changes the root route, questionnaire URLs, login semantics and cookie format, and removes audience kubeconfig access.

I would ultimately maintain both demos from one trunk, with explicit entry routes or demo profiles. First preserve the old release by a named reference and prove both intended journeys on coffee. Do not equate Git ancestry with a safe demo migration.

## Findings that should drive the next work

### 1. The external demo manifests do not match the coffee auth-service

**Priority: demo blocker for the reviewed source/manifests combination.**

The external `test/e2e/setup/demo-only/voter-gitops/base/app.yaml` selects `:coffee` images, but its configuration and RBAC still use `FORWARD_SA=quiz-access`. The surrounding manifests:

- Give that account direct quiz permissions, rather than the coffee impersonation permissions.
- Give the auth-service the old permissions, without the impersonation rules its coffee PATCH implementation needs.
- Copy only `Authorization` from ForwardAuth, omitting `Impersonate-User`, group and identity extras.
- Do not install the coffee branch’s audience group RoleBinding and impersonator account.

With the reviewed coffee binary, server-side CoffeeConfig patches therefore lack the required impersonation authorization. Direct quiz requests through Traefik lose participant attribution and use the old shared account. Correct hostnames do not resolve this mismatch.

Both external environment overlays render successfully. That is evidence that Kustomize can assemble them, not that the application contract is satisfied. Mutable tags also prevent determining which source revision the existing cluster actually runs from these files alone.

**Next step:** use a single versioned application deployment base containing the application’s service accounts, RBAC, routes and image identities. Let the external project supply environment values and demo-specific Flux/ConfigButler resources. Test the combined rendered output against the exact image pair.

Evidence: external `test/e2e/setup/demo-only/voter-gitops/base/{app.yaml,auth-service-rbac.yaml,quiz-rbac.yaml}` and `{production,test}/ingress-auth.yaml`; `coffee:auth-service/kube_client.go`, `coffee:k8s/audience-impersonator-rbac.yaml`.

### 2. Coffee’s save-message integration targets the wrong ConfigButler API

**Priority: demo blocker for the “save with my message” story.**

`coffee:auth-service/kube_client.go:413` constructs `configbutler.ai/v1alpha1` CommitRequests, and `commitRequestGVR` at line 430 sends them to the same version. The external controller’s `config/crd/bases/configbutler.ai_commitrequests.yaml` serves **v1alpha3 only**.

There is also a target mismatch: the app defaults `CONFIGBUTLER_GIT_TARGET_NAME` to `voter-demo`; the external test overlay creates a GitTarget named `demo-coffeeconfig`, without overriding that app setting. Its commit window is `0s`, which also needs reconciliation with a design that patches first and then asks to finalize an open window.

The CoffeeConfig PATCH can succeed while CommitRequest creation fails. The handler logs the failure and returns success; its recent-history entry records the patch independently. This partial-success behavior is sensible for an already-applied write, but it cannot establish that Git contains the requested message or author.

The existing fake HTTP tests explicitly expect the old API version. They can protect the wrong integration contract indefinitely.

**Next step:** align the served API, target name, namespace and commit-window behavior with the pinned controller release. Expose “configuration saved” and “Git commit confirmed” as separate states if the UI promises both. Add a real controller integration test that checks the resulting Git object, author and message.

Evidence: `coffee:auth-service/{kube_client.go:413,kube_client_test.go:272,config.go:31,coffee_handlers.go:163}`; external `test/e2e/setup/demo-only/voter-gitops/test/coffeeconfig-reverse-gitops.yaml`.

### 3. The coffee editor detects conflicts visually but does not prevent lost updates

**Priority: high for an audience editing simultaneously.**

`coffee:frontend/src/screens/AdminScreen.vue:93` saves the entire draft `spec`. It sends no `metadata.resourceVersion` precondition. The backend reads the current object for history, then forwards the merge patch separately; that read does not make the write conditional.

Two browsers can load the same config, make different edits, and save before the corresponding SSE updates arrive. The second full-spec save can overwrite the first. Products and vouchers are arrays, increasing the amount replaced by a stale save. Field markers are useful UX, but they are not server-enforced optimistic concurrency.

**Next step:** carry the version associated with the edited baseline into a conditional write, return Kubernetes conflicts as HTTP 409, and let the editor rebase or request resolution. `writeKubeError` currently preserves only NotFound and otherwise returns 500, so conflict handling needs work on both sides. Test simultaneous edits from two browser contexts, including array edits.

Evidence: `coffee:frontend/src/screens/AdminScreen.vue:93`; `coffee:auth-service/{coffee_handlers.go:150,kube_client.go}`.

### 4. The identity is self-asserted, and impersonation privileges are broader than the comments imply

**Priority: release blocker for a reusable security component; an explicit constraint for the demo.**

The browser generates a six-digit ID with `Math.random()` and sends it, display name and email to `/public/login`. The server validates their shape and signs them into a cookie. Anyone with the demo code can submit another participant’s ID or an arbitrary valid email address. Signing a claim protects it after login; it does not establish who originally supplied it.

This is acceptable for a deliberately anonymous attribution demonstration. It is not verified identity, collision-resistant identity issuance, or a safe foundation for per-person privileges. The six-digit space also allows accidental collisions.

Separately, both coffee impersonation ClusterRoles grant unrestricted `impersonate` on `users`. The `demo:` prefix is enforced by application code, not Kubernetes RBAC. Pinning the group does not prevent a compromised credential from choosing a different username with a direct RoleBinding. “Impersonate-only” should not be described as limiting a leaked token to audience identities.

Kubernetes authorizes impersonated attributes separately and supports exact `resourceNames` restrictions; the shipped rules do not restrict usernames. See the official [impersonation documentation](https://kubernetes.io/docs/reference/access-authn-authz/user-impersonation/).

**Next step:** document the trusted components and credential-compromise boundary. For anonymous mode, issue a sufficiently random server-owned subject and label display name/email as unverified. For authenticated mode, derive the subject from a verified identity provider. Separate the demo credential policy from the reusable component’s identity interface. Keep unrestricted impersonation confined to a deliberately isolated environment unless a narrower policy is implemented and tested.

Evidence: `coffee:frontend/src/lib/demoIdentity.ts`; `coffee:auth-service/{http_handlers.go:43,identity.go}`; `coffee:k8s/{audience-impersonator-rbac.yaml,auth-service-rbac.yaml}`.

### 5. “Admin” currently means any logged-in participant

**Priority: policy decision before the next public demo.**

`requireAdminMiddleware` simply delegates to `requireSessionMiddleware`. The audience Role also permits CoffeeConfig patch/update. `ADMIN_PASSWORD` and the admin cookie settings are declared but do not implement a separate privilege boundary.

Given the demo’s purpose, collaborative editing may be intentional. If so, call it an editor and remove misleading unused settings. If some actions need an operator role, enforce it in both handlers and Kubernetes authorization. Adding a password to the admin handler alone would leave the direct Kubernetes API route available to audience identities with write permissions.

Evidence: `coffee:auth-service/coffee_handlers.go:254`, `coffee:auth-service/config.go`, `coffee:k8s/auth-service-rbac.yaml`.

### 6. Source deployment defaults disagree with the frontends

**Priority: reproducibility blocker.**

Main serves frontend/auth at `voter.z65.nl` and routes Kubernetes API requests at `vote.reversegitops.dev`. Its frontend defaults to namespace `vote`, while its application manifests use `voter`. Coffee still serves frontend/public routes at `voter.z65.nl` while its Kubernetes route matches `coffee.z65.nl`; it does improve namespace discovery through `/public/session`.

Same-origin SPA requests will not select the intended API router with these defaults. Frontend fallback routing may even return HTML where the client expects JSON.

**Next step:** make hostname and namespace environment inputs and assert agreement across the rendered routes and runtime configuration. A schema validator will not detect semantically inconsistent but valid hostname strings.

Evidence: both branches’ `k8s/ingress-*.yaml`, main `k8s/test-deploy.yaml` and `frontend/src/api/kube.ts`, coffee `frontend/src/api/kube.ts`.

### 7. Login protection and demo resource bounds are incomplete

**Priority: high before internet-facing use.**

The default generated access codes are short, and neither branch implements login throttling in the app or its checked-in ingress. Coffee also supports an explicitly configured static code. The external API rate limiter is attached to the Kubernetes route, not the `/public/login` route. Its `average: 10000` is not evidence of a suitable login policy.

Coffee’s PATCH handler reads the entire body with `io.ReadAll`, and login/order decoding has no explicit body cap. Orders accumulate in memory, including rejected orders. These are concrete resource paths to bound before a room of users or hostile traffic reaches the app.

**Next step:** rate-limit the actual login route, bound request bodies, validate duration/size settings at startup, and cap retained order records. Keep short manually typed codes only with deliberate expiry and abuse controls. Avoid permanent account-style lockouts that let one audience member deny entry to the rest.

The external `maxResponseBodySize` is a limit on the **auth server’s response**, not on incoming application request bodies. See [Traefik ForwardAuth documentation](https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/forwardauth/).

Evidence: both `auth-service/join_codes.go`; coffee `auth-service/{config.go,http_handlers.go,coffee_handlers.go,coffee_runtime.go}`; external `voter-gitops/test/ingress-kubeapi.yaml` under the demo setup directory.

## The frontends

Main is small and understandable. Coffee adds useful runtime namespace discovery, session-aware navigation, streaming updates and field conflict indicators. Both build successfully. The code merits incremental improvement, not a framework rewrite.

The largest maintenance concern is the 1,594-line `AdminScreen.vue`, where editing, merge/conflict logic, subscriptions and rendering share a file. Extract its state transitions and write behavior into testable modules, and divide the form into coherent sections. Preserve its existing useful behavior while adding server-enforced concurrency.

Three practical gaps matter more than cosmetic cleanup:

1. **The mock cannot run the coffee journey.** It handles Kubernetes paths but not `/public/login`, `/public/session`, storefront or order APIs. A local request to `/public/session` returned HTML with status 200. The quiz mock also looks under `dev/fixtures/quizsessions`, while the committed fixture sits under `questionnairesessions`; requesting it returned 404. Repair these contracts before adding a Playwright test “against the existing mock.”
2. **Old questionnaire links need migration.** Main uses `/join/:session?` and `/s/:session/answer`; coffee uses `/login` and `/answer/:session`, with `/` becoming the coffee order screen. Old links can hit the catch-all and land in coffee. Define supported QR formats and test them explicitly.
3. **Live-session rules are mostly UI behavior.** The quiz store keeps an already loaded session, and the answer screen checks that cached state. The coffee forward-auth path does not check the referenced quiz state. If closing a quiz must prevent submissions, enforce that in a server/admission policy and test direct API requests; hiding a submit action is insufficient.

Prioritize browser coverage for quiz login/submission, coffee login/order, simultaneous editor saves, expiry/re-login, and stream disconnect/recovery. Mock tests establish frontend behavior; they cannot establish Kubernetes authorization or Git attribution.

## Operational behavior worth documenting now

Coffee orders and voucher usage are process memory, and its change history is also an in-memory view. Restarting resets orders, IDs and voucher consumption. Multiple replicas would maintain independent usage counters and, with generated codes, independent access-code stores. Even a rolling update of a one-replica Deployment can temporarily overlap old and new processes.

Treat this as an explicitly ephemeral, single-instance demo until that state is externalized. Decide whether rollout should intentionally reset it. Do not add replicas as an availability improvement without addressing these semantics.

The auth container runs as root and hardcodes an amd64 build. It lacks the deployment security settings appropriate for publishing a small trusted service. Set a non-root user, remove unnecessary privileges, configure a read-only filesystem where compatible, and make architecture support intentional. These are worthwhile release tasks; they rank below the confirmed integration and authorization-contract issues.

Readiness currently proves that the process serves `/healthz`, not that impersonated writes or Git commits work. Keep liveness simple and add a separate demo preflight for dependencies and permissions. The external APF policy is for `auth-service` and `quiz-access` in namespace `vote`; it does not establish protection for the `voter-test`/`voter-production` coffee identities.

## Where GitOps Reverser fits

The external repository supplies the environment and the actual Git workflow. Its `test/e2e/Taskfile-e2e.yml:681` defines `test-e2e-demo`, prepares the demo and intentionally leaves resources behind. The file describes this as a setup runner, not a measurable application test suite. I did not execute it.

The coffee test overlay watches CoffeeConfig changes into the demo Git repository’s `demo-test` branch under `voter-coffee`. Flux consumes that branch for test. The production overlay consumes `main` and maps the recorded test-namespace config into production. Promotion through Git is therefore a central part of the demo, not an incidental deployment detail.

The decisive acceptance test should follow that whole path: a browser edits config, Kubernetes records the intended identity, ConfigButler creates the expected Git change, and the intended Flux environment reconciles it. An application build and a controller test suite can both pass while this combined story is broken.

Keep the external environment orchestration there. Own application behavior and its base deployment here. Pin the interface between the repositories: application image digests, deployment-base revision, controller version/API and environment settings.

## Recommended sequence and completion criteria

### Step 1 — Establish a known demo baseline

- Record the two source revisions and intended demo URLs before consolidating branches.
- Resolve the deployment identity mismatch and CommitRequest API/target mismatch.
- Make host/namespace configuration coherent and support the intended quiz QR routes.
- Decide whether all participants can edit, and whether demo state resets on rollout.
- Write a short runbook covering start, verify, display code, reset, stop and rollback.

**Complete when:** a clean environment, using recorded image digests and manifest revisions, completes both demo journeys and the expected attributed Git change. Another person should be able to identify and reproduce that state without reading chat transcripts.

### Step 2 — Put a small required CI gate around that baseline

| Gate | What it establishes |
| --- | --- |
| Go formatting, vet, unit tests and race tests | Basic correctness of cookies, caches, identity and concurrent runtime behavior |
| `npm ci`, lint and build | Reproducible frontend dependency install, type-check and compilation |
| Container builds | Dockerfiles package the intended sources for supported architectures |
| Kustomize render and schema validation | Deployment structure is valid |
| Explicit rendered-manifest assertions | Hostnames, namespace, account names, identity forwarding and API versions agree |
| Browser smoke tests | Both demo journeys work through their supported entry URLs |
| Disposable-cluster integration | Real RBAC/admission, Traefik headers, audit identity and ConfigButler Git output work together |

Start with the fast checks immediately. Add the repaired mock tests and the real integration check before describing the demo as reproducible. While both branches remain active, validate both.

Use GitOps Reverser’s CI as a source of conventions, particularly separation of untrusted validation from publishing. Do not copy its large workflow wholesale: controller-specific jobs, images, tools and permissions would introduce maintenance work unrelated to this app.

**Complete when:** required checks reject a broken quiz route, an incorrect impersonation configuration, a stale concurrent save and an incompatible CommitRequest version.

### Step 3 — Make releases and deployment state inspectable

Build a matched frontend/auth image pair from a clean, tested commit. Publish immutable identities and retain the existing registry as appropriate; changing registry providers is not required to fix traceability. Update deployment digests together and retain a known rollback pair. Build once and promote those same artifacts.

The existing build SHA/dirty metadata is a useful start. Add a small release record relating frontend digest, auth digest, source SHA, deployment revision and tested controller version. A status/preflight command should print those values and verify application routing, login, resource access and Git completion without altering the environment unexpectedly.

**Complete when:** “what is running, what was tested, and how do I restore the previous demo?” have short, reproducible answers.

### Step 4 — Extract the reusable auth component

The product boundary I would choose is **a browser-session-to-Kubernetes-identity gateway, with a documented Traefik integration**.

| Keep in the reusable component | Keep in the demo/application |
| --- | --- |
| Session issuance, validation, expiry and key handling | Quiz join-code discovery and quiz lifecycle |
| Trusted identity input and mapping to Kubernetes subjects | Coffee orders, prices, vouchers and retained history |
| Token acquisition/cache and forward-auth decision | CoffeeConfig editing and frontend screens |
| Header trust contract and deployment policy examples | ConfigButler CommitRequest orchestration |
| Generic logging, configuration validation and health behavior | Demo-specific claim names and GitTarget configuration |

First create this boundary within the existing repository. A separately testable generic core should not need the CoffeeConfig types or quiz resource names to compile. The coffee application can use a small shared identity helper for its own impersonated Kubernetes writes.

Start with one supported ingress integration and an explicit identity-provider interface. Anonymous demo access can be one adapter. Becoming an OIDC **issuer** is a separate product commitment; it is not necessary merely to accept identity from an OIDC provider.

Before a ConfigButler company release, provide a runnable example, supported version matrix, documented impersonation blast radius, secure deployment defaults, real negative authorization tests, session/key lifecycle guidance, license/module-path cleanup, a security reporting policy and a release process. Narrow the auth-service’s broad namespace-secret permissions and cluster-wide demo reads as part of removing demo responsibilities.

**Complete when:** an unrelated example CRD can use the component without changing its source, verified and anonymous identities are clearly distinguished, and the public integration test demonstrates both allowed and denied operations through the real proxy.

## Where I agree and disagree with Claude

I agree with the central diagnosis: deployment drift and unrecorded release state explain much of the recurring preparation cost. Coffee’s identity work is the right starting point, and extracting it before stabilizing the demos would make the task harder.

I would revise several conclusions:

- Coffee’s commit history is a superset; its behavior is not a drop-in superset. Preserve and test both demo experiences before retiring main’s behavior.
- The external manifests are not a verified working deployment for the latest coffee source. Their old RBAC/header contract conflicts with it.
- The editor’s conflict markers do not constitute conditional writes.
- An impersonate-only account with unrestricted usernames is not restricted to audience identities after credential compromise.
- Kustomize plus schema validation will not catch hostname disagreement; add semantic assertions and HTTP checks.
- The existing mock needs repairs before it can support the suggested browser smoke test.
- The external APF policy targets a different namespace/account set, and its auth-response body limit is not an application request limit.
- Building a router inside `parseSessionRef` is cleanup/performance work, not a high-severity finding without evidence of material impact. Integration correctness comes first.

I would also avoid half-day promises for the initial phases. The edits may be small, but proving the two demos across Traefik, Kubernetes, ConfigButler and Flux is the work that determines readiness.

## Verification performed

| Check | Result |
| --- | --- |
| Branch ancestry | Main is 0 commits ahead / 21 behind coffee |
| Main frontend lint/build | Passed with existing installed dependencies; JS 434.85 kB, gzip 105.28 kB |
| Coffee clean `npm ci`, lint/build | Passed in a temporary archive checkout; JS 480.15 kB, gzip 117.39 kB |
| Coffee `kubectl kustomize k8s` | Passed |
| External combined `voter-gitops` render | Passed for test and production |
| Coffee mock `/public/session` | Returned 200 HTML, not the required session JSON |
| Mock quiz fixture request | Returned 404 due to fixture directory mismatch |
| Go tests/vet/race | Not run: no Go executable found in PATH or the checked conventional installation paths |
| Docker image build, browser automation, cluster/audit/Git end-to-end | Not run |
| Registry and deployed image inspection | Deliberately not performed, per the brief |

These results support the source findings and frontend build assessment. They do not certify the running demos or the auth-service for production use.
