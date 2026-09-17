# Plan: thin Voter, reusable libraries, independent Room Pass

This is the implementation plan, not a claim that the target design is deployed.
[ARCHITECTURE.md](ARCHITECTURE.md) defines the boundaries and target data flow.

Voter is on krm-stream **0.4.0**; the released primitives are integrated and
deployed. Cluster facts were last verified **2026-09-15**, with three GitTargets
(`demo1` 4/4 streams, `demo2` 2/2, `gitops-reverser-config` 5/5), voter on
`sha-d9f3d93` and room-pass on `sha-359c09b`.

**This file keeps only actionable work.** A section is deleted when its last item
closes, and completed increments live in Git history rather than here -- which is
why the numbering starts at 2. What was removed: the shared-streams, first
implementation and earlier voting increments, the verified-release notes, and
item 1, adopting the released integration primitives.

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
      0.4.0 still resnapshots after upstream watch closure and every browser reconnect;
      sharing does not eliminate per-browser transfer or rendering. Upstream continuation
      is a measured follow-up, not a claimed 0.4.0 feature. Use real-browser smoke tests
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
      `@koudijs.dev.test` identity formatting are current assumptions. Define an
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

- [x] Install ConfigButler through `external/k8s` and provision its Git targets.
      One GitProvider (`k8s-audit-trail`) and three GitTargets, all Succeeded.
      Actor attribution is verified end to end in both directions: a participant's
      vote and an operator's RoleBinding both commit with the human as **Author**
      and the bot as **Committer**.
- [ ] Observe the commit, rather than only requesting it. The receipt still names
      request acceptance `commitRequested`; there is no state that means the commit
      landed, and no commit reference is shown back to the user. Kubernetes save,
      CommitRequest acceptance and observed Git commit are three states and the UI
      currently distinguishes two.
- [ ] Use ConfigButler's public APIs for durable change history and commit observation;
      a krm-stream watch is current state, not an audit/history database. General
      commit-controller behavior belongs in that project, not Voter.
- [ ] Keep the demo explicitly single-replica/process-scoped for orders and voucher
      counts for now. Before promising durable orders or enabling replicas > 1, choose
      shared persistence with atomic redemption enforcement. Do not put coffee
      persistence in krm-stream, and do not build a durable-looking order HISTORY on
      transient state. The live feed on `/admin/orders` is the allowed shape of this:
      it shows what this process has seen, says on the page that it is one replica
      since its last restart, and keeps only the last 100.
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
- [x] Reinstate `/admin/orders`, this time with a backend behind it: a bounded
      in-memory order log, `GET /public/orders` and a hand-written SSE feed, recording
      refusals as well as placements. It sits beside the Config tab to make the
      classification argument concrete — the CoffeeConfig is a Kubernetes object that
      reaches Git, and an order is an event that deliberately reaches neither. The
      feed exposes display names only. The ConfigHistory screen stays deleted; it
      would need durable history that nothing here provides.
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

## 5a. The Databases page

The third tab is built, tested and in the image. What is left is everything that
lives outside this repository -- see [docs/databases.md](docs/databases.md) for the
commands and for why each step is separate.

- [ ] `kubectl apply -f voter/config/crd/databases.yaml`. Cluster-scoped, one apply,
      no controller behind it. Until this exists every Database page renders the API
      server's "could not find the requested resource", which is at least honest.
- [ ] Add the `platform.configbutler.ai/databases` rule to the `voter-audience` Role in
      `external/k8s/.../voter-demo/participant-rbac.yaml`: `get, list, watch, create,
      patch, update`, and deliberately not `delete`. Without it the pages explain the
      gap and every button still produces the real 403.
- [ ] Deploy. Not done yet, on purpose: the image carries the page but the demo has
      not rehearsed with it. Same GitOps path as everything else -- image tag in
      `voter-demo/app.yaml`, never a live workload mutation.
- [ ] Seed two or three requests from different teams, so the list reads as
      "outstanding intent from all teams" rather than as an empty state.

Deferred rather than missing:

- [ ] The gitops-reverser half. No GitTarget watches Databases, so a save creates no
      CommitRequest and the intent is recorded as an annotation on the object instead.
      When a target exists, `CONFIGBUTLER_DATABASE_GIT_TARGET_NAME` on the Deployment
      is the entire change. It is a SEPARATE setting from the coffee one on purpose:
      reusing that target would close demo 1's open commit window from a page that
      never touched the menu.

## 6. Retained platform work

These are unresolved items from the previous plan, not newly verified cluster facts.
They belong in the platform repository and should move to its tracked backlog when
implementation starts. Paths below are relative to `external/k8s/k8s.koudijs.dev/`.

- [ ] Test rendered authenticator connector/claim containment, including the transitional
      `room` connector. Exercise attendee, unrelated external identity and named operator
      against real RBAC; audit additive `system:authenticated` grants and object scope.
- [ ] Review the named LinkedIn cluster-admin binding, placeholder GitHub team grants,
      oauth2-proxy email-domain policy, Grafana rules and all shared-Dex clients.
      The bare-email operator bindings are already gone (2026-09-11, `6a6f881`);
      every ClusterRoleBinding subject is connector-prefixed, verified 2026-09-15.
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
