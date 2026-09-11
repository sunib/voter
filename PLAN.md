# Plan: thin Voter, reusable libraries, independent Room Pass

This is the implementation plan, not a claim that the target design is deployed.
[ARCHITECTURE.md](ARCHITECTURE.md) defines the boundaries and target data flow.
Reviewed against krm-stream **0.3.0** and upstream main `2154f9d` on **2026-09-11**;
the released primitives are integrated and deployed as described below.
Updated **2026-09-11** after verifying deployed Voter revision `85de0c0` and GitOps
revision `2d770a1`; Room Pass remains on `d7d38ba`. Historical investigations remain in Git; this file keeps actionable
work and operational facts that still matter.

## Outcome

Voter should demonstrate a small application built on public components: a participant
joins, orders coffee, encounters the depleted voucher, and an operator safely changes
its configuration while other browsers update live. Kubernetes sees the person's
identity for writes; the UI distinguishes a Kubernetes save from an observed Git commit.
The demo targets **200 concurrent attendees**: shared streaming is a required part
of this refactor, delivered as a separate change from the editor replacement.
The [k8s-front adoption recommendation](docs/k8s-front-adoption.md) keeps this capacity
work ahead of a possible CoffeeConfig pilot and separates product prototyping from
Voter deployment decisions.

Generic resource streaming, reconciliation and editor state belong in krm-stream.
Room Pass becomes an independently released room-enrollment service integrated with
Dex. Voter keeps coffee and voting behavior, its application session, narrow authorized endpoints
and presentation. Reducing code means deleting duplicate responsibilities, not moving
an entire demo into a general-purpose package.

## Deployed increment: shared streams

Deployed on **2026-09-11** as Voter `85de0c0`, promoted through GitOps `2d770a1`.

- One process-wide library `SharedBackend`; participant SelfSubjectReview identity
  and list/watch SARs before cache disclosure, with 30-second rechecks.
- Independent session deadlines, five-second authorization/write deadlines and
  aggregate stream metrics. Direct REST reads/writes retain participant tokens.
- Narrow grants applied in the disposable fixture and promoted to the platform:
  named `demo-coffee` list/watch plus SubjectAccessReview creation, nothing else.
  The vestigial `get` verb and quizsessions/quizsubmissions grants were removed.
- Local 200-identity rehearsal proved **one actual upstream watch**, update convergence,
  50-client reconnect reuse, grant withdrawal in 30.00s and last-disconnect cleanup
  under 1 CPU / 256 MiB limits. Uses real Kubernetes service-account identities in
  fixture-signed sessions; attendee OIDC/browser load remains unproven.

Validation: Go tests with race detection, vet/lint, all **21 frontend tests**,
frontend type-check/build/lint, container build and platform Kustomize rendering
passed. All **13 browser tests passed in 41.9s**, including real RBAC withdrawal
while another viewer continues. The simultaneous 200-identity rehearsal passed in
45.0s. [CI run 34605972419](https://github.com/sunib/voter/actions/runs/34605972419)
passed every job for `85de0c0`.

Production verification: Flux applied `2d770a1`; the ready pod runs
`ghcr.io/sunib/voter:sha-85de0c0@sha256:cc0f384099ca002a697aeadf5f5617d35658b0b9c33416373cb72af21bf057e3`
and `/public/build-info` reports `gitCommit: 85de0c0`, `gitDirty: 0`. On Kubernetes
**1.36.1** the service account is allowed named `coffeeconfigs/demo-coffee` list and
watch, and denied unnamed list, `get`, `patch`, quiz resources, Secrets and
impersonation -- the named-authorization promotion gate is closed. Public `/metrics`
returns 404 while the pod-proxy listener on 9090 serves counters; an unauthenticated
stream returns 401 and the Dex login redirect is intact.

See [shared-stream verification and release handoff](docs/shared-streams.md) for
measurements, commands and limitations. **Next: an authenticated production smoke
test and production-equivalent capacity evidence** -- no attendee has opened a stream
against the deployed build yet, so all stream counters are still zero. Then
ConfigButler commit observation and actor attribution. k8s-front remains a separate
proposal.

### Review fixes and upstream boundary

Metrics now use a separate listener excluded from the application Service/ingress.
Explicit `STREAM_KUBECONFIG` supports local development while keeping shared credentials
out of participant clients. Subject initialization is synchronized, wiring failures
are detected at registration, and flush errors cancel their subscription immediately.
RBAC rechecks and IdP/token-claim revocation have separate documented bounds.

Keep these host fixes in the shared-stream change. Keep navigation redirects,
orphaned screen/API removal and stale README cleanup in a separate housekeeping
commit when preparing the release. No new krm-stream APIs have been adopted; transport/lifecycle wrapper replacement
is deferred until the upcoming release and its public APIs are verified.

Production gates still include named list/watch authorization on the production API
version and a complete single-replica Recreate rollout/reconnect rehearsal. Both current
fixture and platform manifests have 1 CPU / 256 MiB limits.

## First implementation increment

Implemented and deployed as `d7d38ba` on 2026-09-11 while the upstream krm-stream
improvements are in progress:

- [x] REST editor reads use `gateway.Project(ProjectionFull)` directly, preserving UID,
      resourceVersion, unknown fields and explicit false/zero/empty/null values. The
      transitional save response uses the same projection; business DTOs remain for
      pricing only. Added a handler regression test and frontend metadata fields.
- [x] Voucher refresh calls its endpoint, reports stale counts on failure and recovers
      on live updates. Usage failure no longer prevents loading the editor.
- [x] Save errors keep the form and unsaved input visible. The API client now displays
      the backend's `error` message instead of only a generic HTTP status.
- [x] Added browser coverage for rejected-save draft preservation and usage failure/
      recovery, plus unexpected page-error detection.

Validation: Go tests/vet/lint and frontend unit tests/type-check/build/lint passed.
All **9 browser tests passed in 6.0s** against the rebuilt image in the disposable
fixture after clearing local disk pressure and restoring its issuer DNS aliases.

That increment predates the deployed library migration below. Shared streaming
and the 200-attendee acceptance rehearsal remain outstanding.

## Earlier voting increment

Deployed Voter `14dffd4` through GitOps `ef45717` on 2026-09-11. `/vote` lists rounds,
`/answer/demo-round-1` is open, and `/answer/demo-round-1/results` shows results on
explicit refresh. QuizSubmissions persist in Kubernetes; text answers serialize correctly;
required/type/choice/range validation, closed/stale-round rejection and duplicate
prevention use the participant-token backend. Removed the obsolete ForwardAuth
client and cached quiz-session store. See [voting demo](docs/voting-demo.md).

All 10 browser tests pass locally and the fresh-cluster browser CI job passed.
The full [CI run 34584101156](https://github.com/sunib/voter/actions/runs/34584101156)
succeeded, including lint, unit/integration/network tests and both image builds.
Production checks verified Flux revision, ready pod digest, public build metadata,
a live sample round and the `/vote` login path. The authenticated two-voter journey
was exercised in the disposable fixture, not by casting votes in the production round.

## Verified release and current gaps

| Area | Evidence and remaining gap |
| --- | --- |
| CI | [Run 34593086461](https://github.com/sunib/voter/actions/runs/34593086461) for `55e287d` passed all jobs, including browser tests and both image builds. |
| Deployment | GitOps commit `59fc82848caf63a40cf2f8e95aebf00d6d0ecc10` is pushed to `ConfigButler/k8s` main. Flux `voter-demo` is Ready at that revision; Voter completed rollout and the sample round is live. |
| Running versions | Voter runs `sha-55e287d`, pinned to `sha256:ccffb1ca260f78e46af1316e639faabea73b93a160b056a0938d86dd0ce4d186`; the ready pod image ID matches. Room Pass remains on `sha-d7d38ba`. |
| Production checks | Public `/public/build-info` reports `gitCommit: 55e287d`, `gitDirty: 0`. Chromium reached the room-code form through the quiz, results and editor routes without page errors. The sample QuizSession is live. |
| Editor | Deployed editor uses the library store, atomic arrays and conditional intent saves; no custom reconciliation remains. |
| Stream | Deployed stream adds managed recovery, exact namespace/name/version restrictions and per-session deadlines. Shared watches and bounded RBAC rechecks remain open. |
| Resource contract | Deployed editor initializes from stream snapshots, reconciles guarded projected GETs and returns receipt-only saves. |
| Git payoff | At the latest CRD check, `commitrequests.configbutler.ai` was absent. Neither an observed Git commit nor end-to-end actor attribution has been verified. |

First-increment artifacts retained as a rollback reference:

- Voter: `sha256:c6dd9cfe0903fb169c599907ac5b385fd53956952bdd55f70de6e3f1af233659`.
- Room Pass: `sha256:7e1690adb4e304c600afbed2e1f529b98dc727bd5c1bb5f14b2356d5287b8cee`.

Rollback the current library migration by reverting GitOps commit `59fc828` in
`ConfigButler/k8s`, pushing and letting Flux reconcile. That restores Voter `14dffd4`
while retaining the sample round. All workload changes came from Git;
Flux reconciliation was triggered to apply the committed revision.

## 1. Adopt the released integration primitives

The external checkout was fast-forwarded from `4a12571` to main `2154f9d`.
The npm and both Go 0.3.0 tags point to `209537c`; main only adds release tooling/docs.
Voter now pins npm and both Go modules to **0.3.0**. The Go module and image builder
use **1.27.1**. Upstream runtime behavior was reviewed at the release tag; no local
upstream override or core library copy is used.

### Implemented and deployed as `55e287d`

- AdminScreen now renders the store's draft and derived changes/conflicts. Removed
  custom reconciliation, mutation helpers and dirty registries. Arrays remain atomic;
  existing SKU/code inputs are read-only and row changes use store APIs.
- Managed fetch streams retain same-origin cookie authentication, expose connection
  state, recover sequence gaps, and close on disposal. Storefront uses a read-only policy.
- Saves send captured UID/RV/patch; backend accepts only spec edits, validates projection,
  and adds the captured preconditions to Kubernetes PATCH. No unconditional save is
  accepted. Responses contain receipts with `saved` and optional `commitRequested`,
  `commitRequest` / `commitError`; they never return the object.
- A 409 triggers a guarded uncached GET. Actual conflicts require Take Theirs or Keep
  Mine; a refreshed version without conflicts asks for a new deliberate save. Later
  typing survives the response. A bounded delayed read recovers missing watch echoes.
- Editor UID stays fixed. Deletion offers an in-memory copy of unsaved input; disposal
  clears it. Reopening a replacement starts a new editor. Cross-login draft restoration
  remains separate work; a page navigation currently discards the in-memory draft.
- The per-participant stream now enforces configured namespace/name/version and closes
  at session/token expiry. It does not yet share watches or periodically query RBAC.

Validation: **21 frontend tests**, frontend build/lint, Go tests with race detection,
vet/lint and the Go 1.27.1 container build passed. **All 12 browser tests passed in
10.7s** against the final rebuilt disposable fixture, including a real Kubernetes 409 followed
by a fresh reviewed save, receipt-only response and explicit conflict choice. Backend
tests also prove exact scope refusal and expiry isolation; a client integration test
proves transport reconnection preserves edits through a fresh snapshot. Shared-watch capacity and the full race/reconnect matrix remain open.
The final fixture run required extending the expired local Room deadline. The subsequent
production rollout was verified through Flux, the ready pod digest, public build metadata
and anonymous browser smoke checks; authenticated tests ran in the fixture, and no
production QuizSubmissions were created.

The existing live quiz is [demo-round-1](https://voter.koudijs.dev/answer/demo-round-1),
“How do you change Kubernetes configuration today?” Its
[results](https://voter.koudijs.dev/answer/demo-round-1/results) are available after room
login. Room `demo` is open through **2026-09-30 18:00 UTC** at this verification.

| Earlier upstream request | 0.3.0 finding | Voter decision |
| --- | --- | --- |
| Managed recovery | `connectManagedResourceStream` is exported: fetch transport, bounded jittered retries, health reset, cancellation, terminal 401/403 | Adopt it with the existing same-origin session cookie; delete native EventSource gap assumptions |
| Periodic subscriber authorization | `ReauthorizationInterval` and `ReauthorizationTimeout` check authorization and projection independently per subscriber | Configure the existing gateway; no upstream extension or Voter watch loop |
| Conditional-save integration | Exported `captureSave` and `captureReconciliation`, tested host/client examples and a real-cluster 409 test source | Use the primitives; adapt the small host controller for CoffeeConfig and receipts |
| Vue binding | Tested copyable composable, not an npm export or separate adapter package | Keep a thin Voter binding following the example; no adapter release dependency |
| Authorization naming | `SubjectAccessReviewAuthorizer` exported; `SSARAuthorizer` retained as deprecated alias | Use the clearer name |

[Proposal 0005](https://github.com/ConfigButler/krm-stream/blob/2154f9d/docs/proposals/0005-kubernetes-stream-and-save-semantics.md)
is design discussion, not an approved runtime contract.
[Plan 0006](https://github.com/ConfigButler/krm-stream/blob/2154f9d/docs/proposals/0006-stream-and-save-implementation-plan.md)
supersedes its phase order. Corrected save outcomes and focused regressions shipped;
formal convergence clarification, real-API status/save and UID-race hardening, and upstream
watch continuation remain follow-ups. Do not wait for replay, SSA, version-only events
or a general save framework to remove Voter's duplicate editor state.

- [x] Pin npm and both Go modules to published **0.3.0**, update lockfiles and run
      consumer checks outside upstream `go.work`. Both Go modules now require Go
      **1.27.1**; verify Voter module/toolchain, CI and image build compatibility.
      Verified Go tests/vet/lint and the image build against registry modules.
      No lasting `replace`, local overrides or vendored core source. The external
      checkout is review/development evidence, not a production dependency.
- [x] Make stream, initial read and conflict reread use the same projected KRM shape:
      GVK, namespace/name, **UID**, resourceVersion and preserved JSON value types.
      Keep redaction information with the resource where relevant. Use
      `gateway.Project`; avoid round-tripping editable objects through lossy coffee
      DTOs. Defaults for display must not silently become user edits.
- [x] Render withheld-field indicators from `store.redactions()`, never from mask
      strings inserted into draft values. Preserve disclosure metadata during recovery
      and reject patches to protected paths. CoffeeConfig's `apiKeySecretRef` fields
      contain references (name/key), not Secret values; do not resolve or mask them
      merely because their names mention secrets. Current built-in Secret redaction
      does not automatically redact arbitrary fields in a CoffeeConfig.
- [x] Reuse `LiveResourceStore`, segment-array `Path`, `regionPolicy`,
      `readOnlyPolicy`, stream URL/transport helpers and
      `gateway.ValidateMergePatch`. Voter's editable region is `spec`.
      Schema-driven `withOpenAPIKeyedLists` adoption remains a separate enhancement below.
- [x] Replace `connectWithEventSource` with `connectManagedResourceStream`. Use its
      connecting/syncing/live/retrying/closed/terminal/exhausted states and `onError`
      classification. Keep login navigation in Voter; same-origin fetch carries the
      HttpOnly cookie without exposing the token. Close the owned connection on teardown.
- [x] Follow the upstream Vue example for reactive draft/changes/conflicts/redactions
      and subscription cleanup. It binds one fixed UID; mount the editor only after
      snapshot discovery and remount for a replacement UID. A parent owns any shared
      browser connection. Thin host glue is allowed; copied reconciliation is not.
- [x] Use `captureSave(uid)` and `captureReconciliation(uid)` directly. Adapt the
      conditional-editor example for Voter's HTTP errors, CSRF and receipt body;
      serialize saves and gate them on live/same-UID readiness and no unresolved conflicts.
      Do not propose another generic conditional-edit module upstream.
- [x] Preserve the existing upstream save/watch ordering tests and add focused Voter
      integration coverage for the actual component and host endpoints. Track upstream
      0006 follow-ups separately rather than treating every planned test as shipped.

Acceptance: Voter consumes released store/connection primitives with thin host glue,
without implementing reconciliation, retry scheduling or shared-watch machinery.

## 2. Replace the editor and make writes conditional

- [x] Make the library store the sole owner of server truth, draft, dirtiness and
      conflicts. Wire fields through `setValue`, `isDirty`, `changes`, `conflicts`
      and `takeTheirs`; treat values returned by the store as snapshots, not mutable
      Vue models. Route add/remove operations through store APIs too.
- [x] Delete `reconcileValue`, `preserveOrConflict`, dirty/conflict bookkeeping,
      duplicated deep equality/path mutation helpers and the unusable-merge guard.
      Retain only presentation state such as text being entered into a comma-separated
      field, reason text and highlight timing where the adapter does not own it.
- [x] Start with atomic products/vouchers arrays. Never merge by numeric index after
      structure changes. Existing SKU/code fields are read-only; adding/deleting rows is explicit.
- [ ] Enable keyed merging only from an authoritative CRD schema
      with validated unique stable keys (`sku`/`code`); define key edits as identity
      changes. Keep existing SKU/code fields read-only in the first refactor; adding
      and deleting rows remains explicit. A later rename action must explain that it
      replaces identity and can affect references; it is not an ordinary text edit.
      Generate/consume that schema instead of maintaining a second handwritten list policy. Keyed merging still requires conditional writes: JSON merge patch
      replaces the resulting array as a whole.
- [x] Use one initialization path: the stream snapshot seeds the store. A manual
      refresh or recovery GET feeds the same store, never resets an active draft.
      Coordinate delayed reads so they cannot roll back a newer snapshot.
- [x] Send `store.captureSave(uid)` with its detached patch, base resourceVersion and UID.
      Capture the intent before any await. Require
      the precondition in the backend; never substitute a newer version onto an old
      patch. Preserve Kubernetes 409 responses and enforce the fixed object scope.
- [x] On 409, capture `store.captureReconciliation(uid)` before a most-recent,
      uncached projected GET with the caller's token. An accepted GET advances the base.
      Show `draft-conflict` only for actual conflicting paths; otherwise report
      `version-stale` (“Configuration refreshed; review and save again”). A refused
      guard returns `recovering`, not permission to force the response; follow the
      example's conservative guarded-read recovery before another write. Do not retry the stale payload automatically. Reject saving a
      draft against a replacement UID or while the resource is missing/unsynchronized.
- [x] Validate allowed patch paths and projection with the library on the backend.
      Only `spec` is user-editable; metadata.resourceVersion is a precondition, not
      an editable field. Kubernetes remains the authorization/admission authority.
- [x] Return a **receipt only**, never the CoffeeConfig object, from the save endpoint.
      Keep HTTP 200 JSON for Kubernetes-save and commit-request outcomes; a bare 204
      cannot carry partial-success information. The normal projected watch echo owns
      resource updates. Report saved immediately and synchronization pending until
      observed; recover a missing echo via the same projected read path. Remove the
      unused save-object/adoptSaved adapter from Voter. Do not clear edits made after
      dispatch or regress newer state. Any future object-return exception needs a
      documented use case and projection/ordering tests; it is outside this refactor.
- [x] Disable submission of unresolved conflicts until the user explicitly chooses
      local or server values. `takeTheirs` is public; there is no dedicated keep-local
      method. For a reviewed local choice, retain the chosen value, resolve with
      `takeTheirs`, then reapply it through the store as a new edit without an await;
      verify scalar and atomic-array behavior. Keep save errors separate from initial
      loading failures; draft-preserving error rendering was already fixed in d7d38ba.
- [x] Replace recursive voucher refresh with `getVoucherUsage()`. Keep redemption
      state separate from CoffeeConfig resource state and describe its process scope.
      Test that refreshing calls the endpoint and updates the displayed count; a swallowed
      exception can evade page-error listeners. A failed read keeps the old count with
      a stale/error indication instead of silently claiming freshness.
- [ ] Render connecting/live/reconnecting/expired/forbidden/missing states in both
      screens. Expired sessions need an application-owned re-login path; managed fetch reports
      HTTP 401/403 terminally, unlike the old native EventSource wrapper. Preserve drafts across
      re-login only for the same identity, scope and UID; never carry them to a
      different user. Any temporary persistence excludes credentials and sensitive data.
- [x] Handle deletion explicitly: the store prunes deleted-object drafts. Keep an
      in-memory recovery copy of unsaved input before pruning if offering copy-out;
      never automatically attach it to a replacement UID or another identity. Clear
      it on logout/identity change and keep it out of credentials/persistent storage.
- [x] Keep storefront presentation on a read-only library store; server order pricing
      remains authoritative. Stop streams and subscriptions on unmount/logout.
- [x] Restrict the stream to the configured namespace and CoffeeConfig name server-side.
      Namespace/name/version are checked before opening the backend. Subscription
      lifetime is bounded by the earlier session/token expiry. Shared-stream authorization and
      revocation handling follow the dedicated integration step below.

Acceptance: no custom merge remains in Voter, no unconditional CoffeeConfig save is
accepted, and failures preserve the user's draft while clearly reporting the state.

## 2a. Share streams for the 200-attendee demo

Adopt the existing documented [SharedBackend pattern](https://github.com/ConfigButler/krm-stream/blob/main/docs/auth.md#two-things-that-are-easy-to-confuse).
This is required for the demo, not deferred optimization. Keep the integration in a
separate reviewable change; reuse library fan-out, cache, queues and cleanup.

- [x] Construct one long-lived `gateway.SharedBackend` per configured cluster/backend
      in the Voter process, wrapping a service-account `kube.Backend`. Return that same
      instance from `Clients`; constructing it per request would defeat sharing.
      Scope is fixed to the demo CoffeeConfig. Same-scope subscribers share one watch;
      different scopes and separate replicas do not share it.
- [x] Use `kube.SubjectAccessReviewAuthorizer` before any cached snapshot is disclosed.
      It creates SubjectAccessReviews for both list and watch. Map the session
      to Kubernetes user/groups/UID/extras from trusted SelfSubjectReview identity,
      never browser headers or guessed OIDC username prefixes. Denial or review failure
      refuses the subscription. Replace the current token-presence-only authorizer.
- [ ] Promote the prepared narrow service-account grants through platform GitOps.
      The explicit local fixture already has named CoffeeConfig reads and permission
      to create SubjectAccessReviews; the platform manifest is prepared locally. No impersonation or CoffeeConfig write grants. Keep
      direct REST reads, PATCH and CommitRequest creation on the participant's token.
      Audit documentation must distinguish service-account watches from personal writes.
- [x] Enforce each subscriber's session deadline server-side and release its subscription
      on expiry/disconnect. One attendee leaving must not cancel other attendees' watch;
      the last subscriber leaving must cancel it. Keep browser drafts independent.
- [x] Set `ReauthorizationInterval = 30s` and `ReauthorizationTimeout = 5s`.
      Checks pause only that subscriber's object delivery and fail closed on denial,
      timeout or projection-policy change. Session expiry remains an earlier host
      deadline; the captured principal does not refresh itself. Target termination
      within 60s of withdrawal, verifying interval + check/sink scheduling under load.
      Host callbacks must honor context cancellation and sinks must have bounded writes.
      Budget roughly 13.3 SAR requests/second for 200 subscribers, plus opening/cycle
      bursts. No custom timer or second watch loop in Voter.
- [x] Expose subscriber count, upstream watch count, resync/overflow and access-review
      failures with bounded metric labels. Use library queue bounds and slow-consumer
      recovery; do not create another Voter cache or per-user event queue.

Acceptance: 200 authorized same-scope subscriptions on one Voter replica have **one
steady-state upstream watch**. Subscriber authorization, expiry and recovery remain
independent. Access reviews still create API traffic; shared streaming reduces watches,
not browser connections or the need to authorize each person.

## 3. Prove the integration before shipping

Keep generic test matrices upstream and a focused set of Voter boundary tests here.
Extend the existing disposable browser fixture rather than introducing another stack.

- [ ] Library: scalar convergence/conflict; clean remote array append/delete; local
      array edits with unchanged server; reorder; deletion/type change; missing/duplicate
      list keys; snapshot gap; deletion/recreation; save/echo ordering.
- [ ] Backend with a real disposable API server: missing/stale resourceVersion,
      conflicting concurrent PATCH, scope denial, participant credentials, projection
      and allowed fields. Assert a real 409; fake clients alone cannot prove it.
      Open a stream with a short valid session, keep upstream events flowing through
      expiry, and assert that the server closes that subscriber at the deadline and
      denies reconnection. Other authorized subscribers must continue; the upstream
      watch stops only after the last subscriber leaves. Also prove bounded grant
      withdrawal and fail-closed access-review errors against an already-warm cache.
- [ ] Browser: a 409 without conflicting fields shows refreshed/review state; a
      refused GET overlapping a snapshot blocks writes until recovery; keep-local
      resolution preserves the reviewed choice; deletion offers the recovery copy
      without editing a replacement UID. Also cover two users editing the same field; independent edits; edit product A while
      the server reorders products; lose a stream while typing; reconnect and resnapshot;
      session expiry/re-login; resource deletion; save rejection without losing the form.
      Delay a watch event deliberately to prove the save race is closed.
- [ ] Retain the existing browser cases (12 before sharing; 13 with RBAC withdrawal), and fail relevant tests on unexpected
      page errors. Include observable voucher refresh and protected-field patch tests,
      since caught failures need not surface as browser exceptions. Confirm current CI.
- [ ] Run a 200-session SSE load rehearsal through the real gateway/ingress and
      Kubernetes API with independently authenticated identities. Count actual upstream
      watches: one at steady state for the configured scope on one replica. Verify all
      viewers converge after updates; exercise reconnect bursts, slow clients and final
      disconnect cleanup. Record latency, CPU/memory, access-review load and errors
      under deployment resource limits. Count upstream recycling and browser reconnect
      resets separately, bytes per snapshot, and save 409s with/without field conflicts.
      0.3.0 still resnapshots after upstream watch closure and every browser reconnect;
      sharing does not eliminate per-browser transfer or rendering. Upstream continuation
      is a measured follow-up, not a claimed 0.3.0 feature. Use real-browser smoke tests
      alongside the load harness; 200 HTTP clients alone do not prove browser rendering.
- [ ] Verify multiple tabs keep independent drafts while sharing the upstream watch.
      Test denied users, altered identity headers, denied list/watch separately, scope
      separation and expiry without disrupting other subscribers. Retain upstream
      coalescing tests, and add the Voter wiring test that proves reuse of one instance.
- [ ] Review the deletion diff and test that rendered fields/conflicts/patches follow
      library state through the actual component. Do not add a test banning the name
      `reconcileValue`: a renamed duplicate passes it, while harmless names fail it.
- [ ] Build each image once in CI and load those exact artifacts into k3d; publish or
      promote those same artifacts. Keep slow browser execution in a separate job, but
      require its success for a release candidate. Avoid the current duplicate image
      builds and add runner disk preparation/bounded bringup.

## 4. Extract Room Pass as a public project soon

This is a separate deliverable, not dependent on completing the coffee demo. Start by
removing Voter test/build coupling; retain the existing protocol during extraction.

**Recommended name: Room Pass**, described as “room-code sign-in for applications,
powered by Dex.” Room Pass provides enrollment and a trusted authentication assertion;
Dex provides OAuth 2.0/OIDC protocol endpoints and signed tokens. `room-oauth` would
suggest a standalone authorization server that the project does not implement.
A future native OIDC provider is a separate product decision, not a prerequisite.

- [ ] Extract `room-pass/` with its useful history into a public repository under the
      chosen owner; update the Go module/import path, independent CI, image publishing,
      versioned CRDs/manifests, license/attribution, contribution and security docs.
      Audit the exported history for private fixture data before publication.
- [ ] Move Voter/CoffeeConfig/browser-live tests out of the Room Pass component suite
      into Voter's integration suite. Keep Room Pass's minimal independent OIDC example
      and room-auth/browser, envtest, handoff, race and load tests in its own project.
      Neither build may require the other's checkout or parent Taskfile/devcontainer.
- [ ] Make Voter consume a released Room Pass image/manifests in its fixture. Its
      runtime integration is through Dex/OIDC, not Go imports from Room Pass. Remove
      the in-tree source after the external release and consumer smoke tests pass.
- [ ] Separate demo policy from the component: `demo:` group validation and
      `@demo.invalid` identity formatting are current assumptions. Define an
      operator-controlled allowed group namespace/allowlist with a restrictive default,
      immutable identity settings and synthetic attribution semantics. Do not simply
      remove validation or let room authors choose privileged groups.
- [ ] Preserve existing `roompass.configbutler.ai/v1alpha1` objects, UIDs, cookie keys,
      connector ID `room-pass`, subject construction and audience-group behavior in
      the first extraction release. Repository/image naming must not trigger identity
      migration. Version any later protocol or CRD change explicitly.
- [ ] Publish a non-Voter example that completes room-code login to an ordinary OIDC
      app without that app knowing Kubernetes. Kubernetes remains Room Pass's storage
      prerequisite for this release; database independence and multiple active rooms
      are separate future features.
- [ ] Document one Room/one replica, persistent cookie keys, enrollment/issued-token
      lifetime differences, unverified display names and trusted-proxy deployment.
      Pin/test Dex compatibility and every callback alias. Keep state/nonce/PKCE and
      token verification in established OIDC implementations.
- [ ] Prove upgrade with existing enrollment and rollback via pinned image/manifests.
      Extract first; add HA, new identity backends or native token issuance separately.

Acceptance: a new user can clone/build/test/deploy Room Pass without Voter, authenticate
an unrelated OIDC application through Dex, and upgrade without changing identities.

## 5. Complete the demo and deploy through GitOps

- [ ] Install/configure ConfigButler through `external/k8s` and provision its Git target.
      Represent Kubernetes save, CommitRequest acceptance and observed Git commit as
      separate states. The deployed receipt names request acceptance `commitRequested`;
      end-to-end commit observation is still missing.
      Show the resulting commit reference and verify actor attribution end to end.
- [ ] Use ConfigButler's public APIs for durable change history and commit observation;
      a krm-stream watch is current state, not an audit/history database. General
      commit-controller behavior belongs in that project, not Voter.
- [ ] Keep the demo explicitly single-replica/process-scoped for orders and voucher
      counts for now. Before promising durable orders or enabling replicas > 1, choose
      shared persistence with atomic redemption enforcement. Do not build the admin
      order history on transient counters or put coffee persistence in krm-stream.
- [x] Restore voting as an explicit demo requirement: participant-token round reads,
      validated, create-only QuizSubmission, one per identity/round UID and aggregated results.
      Submitted answers cannot be edited through Voter; retries never overwrite them.
      Remove the obsolete ForwardAuth client and cached quiz-session store. `/vote`
      lists rounds; existing `/answer/:session` links work; vote confirmation has its
      own results screen. Results refresh on demand, without attendee watches/polling.
      Go tests/vet/lint, 14 frontend tests/build/lint and all 10 browser tests pass
      locally (9.1s); CI and deployment are tracked separately below.
- [x] Published `14dffd4` after validation and provisioned the sample round through
      GitOps `ef45717`. Full CI passed; verified Flux, ready pod digest, public build
      revision, live round and login path. Room Pass image remains unchanged.
- [x] Remove unsupported admin-order/history affordances from active navigation;
      restoring voting does not imply implementing unrelated legacy screens.
- [x] Deploy the first tested increment (`d7d38ba`) through GitOps (`5d3d176`);
      verify Flux revision, ready pods, pinned digests and the public login path.
- [x] Add QR-code joining: the presenter projects `task room-pass:present`, a scan
      lands on `/auth/login?code=<current>&return=<path>`, and the participant types
      only a display name before arriving at the chosen page. The destination rides
      in Voter's login transaction; the room code rides in a plain, single-use,
      same-host `__Host-room-pass-joincode` cookie that Room Pass treats as an untrusted
      prefill and re-checks through the ordinary POST. Go tests/vet/lint on both
      modules and all 6 room-auth browser tests pass locally, including the two new
      ones that prove the cookie survives the Dex redirect chain in a real browser.
      See [qr-join.md](room-pass/docs/qr-join.md). Not yet published or deployed.
- [x] Give scanners time to choose a name: the Room now pins `joinCode` to
      `rotateEvery: 30s` / `validFor: 120s`, overriding the CRD's 15s/30s defaults.
      120s is the most the four-rotation ceiling allows (enforced by CEL and again in
      `Advance`), so the requested 10s rotation was not possible — and buys nothing,
      since `validFor` alone sets how long a photographed code works. Applied by
      recreating the Room through `kustomize.toolkit.fluxcd.io/force: enabled`, which
      retired the eight test Participants.
- [ ] Remove that force annotation from `room.yaml` once the code settings are
      settled. It stays armed, so the next edit to an immutable field silently wipes
      the participant list instead of failing loudly.
- [ ] For the completed refactor, publish tested images, then change
      `external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/{app,room-pass}.yaml` in Git.
      No direct live workload mutation. Verify Flux revision, ready pods, image digests
      and the authenticated browser journey; record a Git revert as the rollback.
      Require the concurrency and 200-attendee acceptance tests before presenting the
      completed refactor as ready for the event; the current rollout is an intermediate build.

## 6. Retained platform work

These are unresolved items from the previous plan, not newly verified cluster facts.
They belong in the platform repository and should move to its tracked backlog when
implementation starts. Paths below are relative to `external/k8s/k8s.koudijs.dev/`.

- [ ] Test rendered authenticator connector/claim containment, including the transitional
      `room` connector. Exercise attendee, unrelated external identity and named operator
      against real RBAC; audit additive `system:authenticated` grants and object scope.
- [ ] Review the named LinkedIn cluster-admin binding, placeholder GitHub team grants,
      oauth2-proxy email-domain policy, Grafana rules and all shared-Dex clients.
      Remove bare-email operator bindings only after verifying prefixed OIDC identity.
- [ ] Recheck the previously stalled FluxInstance. Prior diagnosis: operator 0.48.0
      against floating Flux 2.9.5 CRD shape. In the newer external operator checkout,
      `internal/builder/templates.go` gates `/spec/versions/1/...` Receiver/Alert patches
      on `VersionInfo.Minor <= 8`; the prior diagnosis was that 0.48.0 predates that
      gate while distribution `2.x` floated to 2.9.5. Review a compatible pinned pair
      in `2-gitops/clusters/course-cluster/operator.yaml`.
      A Ready application Kustomization does not prove the FluxInstance is healthy.
- [ ] Finish legacy `room` acceptance removal in `1-talos/values.yaml` and the rendered
      authenticator; generate or remove the stale authentication reference YAML. Batch
      authenticator rollout safely, one control-plane node at a time.
- [ ] Verify routes, Dex network isolation and metrics Service ports. Recheck leftover
      Participants, failed preview workloads and pod tombstones before cleanup; old
      counts are not current inventories. Use the appropriate platform workflow.
- [ ] Fix Room's future-date print column and clarify join-code display formatting in
      the extracted component. Revisit Dockerfile frontend pinning in CI hardening.

Operational reminders: request Dex `federated:id` wherever connector containment is
required; test browser Origin/CSP across the full redirect chain; preserve the
[cross-origin regression](room-pass/docs/csp-form-action.md). Dex credentials loaded
through `envFrom` need a pod rollout after changes. Prior `talm template --offline`
rendering dropped networking: inspect a fresh render before any Talos application.
