# Plan: thin Voter, reusable libraries, independent Room Pass

This is the implementation plan, not a claim that the target design is deployed.
[ARCHITECTURE.md](ARCHITECTURE.md) defines the boundaries and target data flow.
Updated **2026-09-11**, reviewing source at `2fecdd5`. This pass changes documentation
only. Historical investigations remain in Git; this file keeps actionable work and
operational facts that still matter.

## Outcome

Voter should demonstrate a small application built on public components: a participant
joins, orders coffee, encounters the depleted voucher, and an operator safely changes
its configuration while other browsers update live. Kubernetes sees the person's
identity for writes; the UI distinguishes a Kubernetes save from an observed Git commit.
The demo targets **200 concurrent attendees**: shared streaming is a required part
of this refactor, delivered as a separate change from the editor replacement.

Generic resource streaming, reconciliation and editor state belong in krm-stream.
Room Pass becomes an independently released room-enrollment service integrated with
Dex. Voter keeps coffee behavior, its application session, narrow authorized endpoints
and presentation. Reducing code means deleting duplicate responsibilities, not moving
an entire demo into a general-purpose package.

## First implementation increment

Implemented on 2026-09-11 while the upstream krm-stream improvements are in progress:

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

The library-store migration, receipt-only saves, conditional writes and shared streaming
remain outstanding. This increment does not claim concurrency safety or production deployment.

## Verified starting point (before this increment)

| Area | Evidence and remaining gap |
| --- | --- |
| Source | `10f7e98` added krm-stream; `2fecdd5` added the real Voter browser fixture, CSP fix, error visibility and a merge guard. Working tree was clean at review start. |
| Browser tests | Independently reran `task test-browser`: **7 passed in 4.5s**. Covers four room-auth cases, two live delivery cases and preservation of an independent scalar edit. |
| Editor | Still uses `reconcileValue`, local dirty/conflict maps and whole-spec PATCH. The browser tests do **not** exercise krm-stream as the owner of user edits. |
| Remaining review defects | Incorrect positional/structural array reconciliation; no conditional writes; recursive `refreshVoucherUsage`; no gap recovery or visible stream/session failure handling. |
| Resource contract | REST CoffeeConfig metadata drops UID, frontend coffee metadata omits UID/resourceVersion, and the composable uses unchecked casts. The reported blank-form root cause is not proven by the guard. |
| CI | Run `34571376541` for `2fecdd5`: browser, lint and devcontainer checks passed; unit tests and the overall run were still in progress at the final check. |
| Deployment | Live Voter is `sha-ee001d6`; Room Pass is `sha-2a90ef2`. Flux `voter-demo` is Ready at platform revision `2cb475d85ee2c44a1741a24458d9e7da719c52e2`. Neither streaming nor the newest CSP fix is deployed. |
| Git payoff | Rechecked: `commitrequests.configbutler.ai` CRD is absent. A Kubernetes save cannot currently produce the promised ConfigButler commit. |

The previous plan's claims that only login exists, the coffee routes return 404 and
the configured voucher list is empty are obsolete. The platform manifest now seeds
TESTNET with maximum usage 3. No authenticated production journey was rerun here.

## 1. Establish the public resource contract and library changes

Agree the resource contract before replacing the editor, but do not block every
Voter deletion on completion of every upstream feature. Begin the store integration
against existing APIs while developing missing generic behavior in krm-stream.
The contributor implementing each upstream dependency also owns its release handoff:
record the upstream PR, exact revision, required API and consuming Voter change.
Publish a tagged prerelease when that increment passes its tests; stabilize it before
production rollout. No fixed release date or maintainer approval is assumed.

During development, an exact Go pseudo-version or reproducibly packed npm artifact
from a recorded commit is acceptable. Record its owner and replacement milestone in
the consuming PR; replace local paths/overrides with registry artifacts before merging
Voter to main. Never depend on a moving branch. The external checkout is a development
workspace, not a production dependency.

- [ ] Make stream, initial read and conflict reread use the same projected KRM shape:
      GVK, namespace/name, **UID**, resourceVersion and preserved JSON value types.
      Keep redaction information with the resource where relevant. Use
      `gateway.Project`; avoid round-tripping editable objects through lossy coffee
      DTOs. Defaults for display must not silently become user edits.
- [ ] Render withheld-field indicators from `store.redactions()`, never from mask
      strings inserted into draft values. Preserve disclosure metadata during recovery
      and reject patches to protected paths. CoffeeConfig's `apiKeySecretRef` fields
      contain references (name/key), not Secret values; do not resolve or mask them
      merely because their names mention secrets. Current built-in Secret redaction
      does not automatically redact arbitrary fields in a CoffeeConfig.
- [ ] Reuse `LiveResourceStore`, segment-array `Path`, `regionPolicy`,
      `readOnlyPolicy`, `withOpenAPIKeyedLists`, stream URL/transport helpers and
      `gateway.ValidateMergePatch`. Configure Voter's editable region as `spec`.
- [ ] Add generic connection lifecycle/recovery upstream: explicit state, bounded
      retry with jitter, cancellation, fresh snapshot after a sequence gap, terminal
      error delivery and a transport-closure signal. In 0.2.1 a gap calls
      `EventSource.close()`; native automatic retry cannot repair that connection.
      Keep identity selection and login navigation in the host.
- [ ] Provide a small optional Vue adapter in the public krm-stream project if needed
      to avoid copying store subscriptions/ref synchronization into each application.
      Keep the core framework-independent; Vue is an optional peer dependency.
- [ ] Exercise save/watch ordering upstream: duplicate echoes, a newer watch event
      preceding an older HTTP result, local edits made during a save, convergence,
      deletion and recreation under a new UID. Add only the missing store behavior;
      do not implement a second reconciliation or version-ordering algorithm in Voter.
- [ ] Put reusable conditional-edit mechanics in an optional krm-stream client module:
      capture patch/base version/UID together, track the in-flight save, and reconcile
      a conflict reread through the store. Accept host-supplied save/read callbacks;
      return conflict/auth/error outcomes without navigating or retrying a stale write.
      This is a proposed upstream API, not an existing 0.2.1 capability. The host owns
      HTTP status mapping, credentials/CSRF, mandatory precondition enforcement,
      Kubernetes PATCH, resource policy and commit orchestration. Keep those host
      handlers short; do not build a generic mutation server to remove a few lines.
- [ ] Pin and consume published npm/Go releases and lockfiles. No lasting `replace`,
      copied library source or application-local fork. Add a small non-coffee example
      for each new generic API so its public contract is independently useful.

Acceptance: library tests cover generic invariants; Voter can consume a released
resource/connection adapter without knowing how reconciliation or recovery works.

## 2. Replace the editor and make writes conditional

- [ ] Make the library store the sole owner of server truth, draft, dirtiness and
      conflicts. Wire fields through `setValue`, `isDirty`, `changes`, `conflicts`
      and `takeTheirs`; treat values returned by the store as snapshots, not mutable
      Vue models. Route add/remove operations through store APIs too.
- [ ] Delete `reconcileValue`, `preserveOrConflict`, dirty/conflict bookkeeping,
      duplicated deep equality/path mutation helpers and the unusable-merge guard.
      Retain only presentation state such as text being entered into a comma-separated
      field, reason text and highlight timing where the adapter does not own it.
- [ ] Start with atomic products/vouchers arrays. Never merge by numeric index after
      structure changes. Enable keyed merging only from an authoritative CRD schema
      with validated unique stable keys (`sku`/`code`); define key edits as identity
      changes. Keep existing SKU/code fields read-only in the first refactor; adding
      and deleting rows remains explicit. A later rename action must explain that it
      replaces identity and can affect references; it is not an ordinary text edit.
      Generate/consume that schema instead of maintaining a second handwritten list policy. Keyed merging still requires conditional writes: JSON merge patch
      replaces the resulting array as a whole.
- [ ] Use one initialization path: the stream snapshot seeds the store. A manual
      refresh or recovery GET feeds the same store, never resets an active draft.
      Coordinate delayed reads so they cannot roll back a newer snapshot.
- [ ] Send `store.patch()` plus the **resourceVersion of the base used to produce
      that patch**. Capture patch, base version and object identity together. Require
      the precondition in the backend; never substitute a newer version onto an old
      patch. Preserve Kubernetes 409 responses and enforce the fixed object scope.
- [ ] On 409, read current state with the caller's token and reconcile through the
      library. Preserve the draft and show conflicts for explicit resolution before
      another save. Do not retry the stale payload automatically. Reject saving a
      draft against a replacement UID or while the resource is missing/unsynchronized.
- [ ] Validate allowed patch paths and projection with the library on the backend.
      Only `spec` is user-editable; metadata.resourceVersion is a precondition, not
      an editable field. Kubernetes remains the authorization/admission authority.
- [ ] Return a **receipt only**, never the CoffeeConfig object, from the save endpoint.
      Keep HTTP 200 JSON for Kubernetes-save and commit-request outcomes; a bare 204
      cannot carry partial-success information. The normal projected watch echo owns
      resource updates. Report saved immediately and synchronization pending until
      observed; recover a missing echo via the same projected read path. Remove the
      unused save-object/adoptSaved adapter from Voter. Do not clear edits made after
      dispatch or regress newer state. Any future object-return exception needs a
      documented use case and projection/ordering tests; it is outside this refactor.
- [ ] Disable submission of unresolved conflicts until the user explicitly chooses
      local or server values. Show conflict state without hiding the form. The new
      `v-else-if="loadError"` branch currently hides an existing draft on save errors;
      keep initial-load failure separate from in-editor errors.
- [x] Replace recursive voucher refresh with `getVoucherUsage()`. Keep redemption
      state separate from CoffeeConfig resource state and describe its process scope.
      Test that refreshing calls the endpoint and updates the displayed count; a swallowed
      exception can evade page-error listeners. A failed read keeps the old count with
      a stale/error indication instead of silently claiming freshness.
- [ ] Render connecting/live/reconnecting/expired/forbidden/missing states in both
      screens. Expired sessions need an application-owned re-login path: native
      EventSource cannot read the wrapper's JSON 401 body. Preserve drafts across
      re-login only for the same identity, scope and UID; never carry them to a
      different user. Any temporary persistence excludes credentials and sensitive data.
- [ ] Keep storefront presentation on a read-only library store; server order pricing
      remains authoritative. Stop streams and subscriptions on unmount/logout.
- [ ] Restrict the stream to the configured namespace and CoffeeConfig name server-side.
      The current policy checks the resource type, not those exact values. Bound stream
      subscription lifetime by session expiry. Shared-stream authorization and
      revocation handling follow the dedicated integration step below.

Acceptance: no custom merge remains in Voter, no unconditional CoffeeConfig save is
accepted, and failures preserve the user's draft while clearly reporting the state.

## 2a. Share streams for the 200-attendee demo

Adopt the existing documented [SharedBackend pattern](https://github.com/ConfigButler/krm-stream/blob/main/docs/auth.md#two-things-that-are-easy-to-confuse).
This is required for the demo, not deferred optimization. Keep the integration in a
separate reviewable change; reuse library fan-out, cache, queues and cleanup.

- [ ] Construct one long-lived `gateway.SharedBackend` per configured cluster/backend
      in the Voter process, wrapping a service-account `kube.Backend`. Return that same
      instance from `Clients`; constructing it per request would defeat sharing.
      Scope is fixed to the demo CoffeeConfig. Same-scope subscribers share one watch;
      different scopes and separate replicas do not share it.
- [ ] Use `kube.SSARAuthorizer` before any cached snapshot is disclosed. Despite its
      name, it creates SubjectAccessReviews for both list and watch. Map the session
      to Kubernetes user/groups/UID/extras from trusted SelfSubjectReview identity,
      never browser headers or guessed OIDC username prefixes. Denial or review failure
      refuses the subscription. Replace the current token-presence-only authorizer.
- [ ] Add only the service-account reads needed for the configured resource and scope,
      plus permission to create SubjectAccessReviews, through platform GitOps and the
      explicit local fixture. No impersonation or CoffeeConfig write grants. Keep
      direct REST reads, PATCH and CommitRequest creation on the participant's token.
      Audit documentation must distinguish service-account watches from personal writes.
- [ ] Enforce each subscriber's session deadline server-side and release its subscription
      on expiry/disconnect. One attendee leaving must not cancel other attendees' watch;
      the last subscriber leaving must cancel it. Keep browser drafts independent.
- [ ] Recheck subscriber authorization at each snapshot cycle and at a bounded interval
      (target maximum 60 seconds), even during continuous live traffic. Session expiry
      remains its own earlier deadline. Use a library lifecycle hook or upstream generic
      extension for periodic checks; do not implement a second watch loop. Document and
      test the revocation bound; never claim instantaneous withdrawal.
- [ ] Expose subscriber count, upstream watch count, resync/overflow and access-review
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
- [ ] Browser: two users edit the same field; independent edits; edit product A while
      the server reorders products; lose a stream while typing; reconnect and resnapshot;
      session expiry/re-login; resource deletion; save rejection without losing the form.
      Delay a watch event deliberately to prove the save race is closed.
- [ ] Keep the seven existing browser cases, and fail relevant tests on unexpected
      page errors. Include observable voucher refresh and protected-field patch tests,
      since caught failures need not surface as browser exceptions. Confirm current CI.
- [ ] Run a 200-session SSE load rehearsal through the real gateway/ingress and
      Kubernetes API with independently authenticated identities. Count actual upstream
      watches: one at steady state for the configured scope on one replica. Verify all
      viewers converge after updates; exercise reconnect bursts, slow clients and final
      disconnect cleanup. Record latency, CPU/memory, access-review load and errors
      under deployment resource limits. Use real-browser smoke tests alongside the load
      harness; 200 HTTP clients alone do not prove browser rendering.
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
      separate states. `committed: true` must no longer mean merely request creation.
      Show the resulting commit reference and verify actor attribution end to end.
- [ ] Use ConfigButler's public APIs for durable change history and commit observation;
      a krm-stream watch is current state, not an audit/history database. General
      commit-controller behavior belongs in that project, not Voter.
- [ ] Keep the demo explicitly single-replica/process-scoped for orders and voucher
      counts for now. Before promising durable orders or enabling replicas > 1, choose
      shared persistence with atomic redemption enforcement. Do not build the admin
      order history on transient counters or put coffee persistence in krm-stream.
- [ ] Proposed scope reduction: remove the broken quiz/ForwardAuth UI and unsupported
      admin-order/history affordances from the demo's active navigation. Port only if
      they become an explicit requirement; do not retain dead routes as obligations.
- [ ] Publish tested images, then change
      `external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/{app,room-pass}.yaml` in Git.
      No direct live workload mutation. Verify Flux revision, ready pods, image digests
      and the authenticated browser journey; record a Git revert as the rollback.
      Do not deploy the known-unsafe editor merely because its image exists.

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
