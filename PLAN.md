# What is left

The single list of remaining work. [ARCHITECTURE.md](ARCHITECTURE.md) describes
the system as it is; this describes the gap to calling it done.

Last verified against the running `k8s.koudijs.dev` cluster and this source tree
on **2026-09-10**. Anything not marked *verified* below was read from
configuration, not observed live.

## Done means

An attendee scans a QR code, joins with a room code, orders a coffee, hits the
depleted-voucher bug, an operator fixes the CoffeeConfig from their phone, and
the change lands in Git — with the attendee's own name in the audit log and the
operator's own name on the commit. An unrelated LinkedIn account can complete
the same login and gets an understandable 403.

Login is the only part of that sentence currently true.

## Verified live on 2026-09-10

Recorded so the next session does not re-derive it.

- All three control planes Ready; `/var/authentication-config.yaml` md5 identical
  across `192.168.20.41/.42/.43` — the containment rules really are uniform.
- Room-code login completes: `oidc: login ok … connector room-pass
  groups=[demo:voter-audience]`.
- The cluster matches Git (`voter:sha-0cde614`, `room-pass:sha-2a90ef2`), but
  both are **behind `main`** — the metrics and browser-test work in `dd9cac4` is
  not deployed.
- Backend route probe: `/public/coffeeconfig` → 401 (exists, needs a session);
  `/public/storefront`, `/public/orders`, `/public/admin/coffeeconfig` → 404.
- `FluxInstance/flux` has been **Stalled** for ~20h. Every Kustomization and
  HelmRelease is otherwise Ready, so nothing is silently un-reconciled *except*
  Flux's own components.
- 7 leftover `Participant` objects and ~71 `ContainerStatusUnknown` pod
  tombstones from the reboots.
- **Publishing an image requires CI.** A local `task image-voter PUSH=true`
  builds fine but the push is denied: a `gh auth token` carries
  `gist, read:org, repo, workflow` and no `write:packages`. `docker login
  ghcr.io` still succeeds, so the failure appears only at push time.
- **ConfigButler is not installed.** There is no `commitrequests.configbutler.ai`
  CRD and no controller in any namespace, while Voter runs with
  `CONFIGBUTLER_GIT_TARGET_NAME=voter-demo`. Every save therefore creates the
  CoffeeConfig change and then fails to create the CommitRequest. See section 2.
- The participant Role `voter-audience` matches
  [docs/authorization.md](docs/authorization.md) exactly: `get,list,watch,patch,
  update` on coffeeconfigs, `create` on quizsubmissions and commitrequests.
  Confirmed with `kubectl auth can-i --as-group=demo:voter-audience`.
- The live CoffeeConfig is `demo-coffee` with `vouchers: []` — the demo's
  depletable TestNet voucher is not configured yet.

---

## 1. Restore one complete application journey

**This is the biggest gap and everything else is smaller.** The demo is a login
with almost no app behind it.

The frontend screens are kept deliberately — they are the restoration target,
not dead weight. What is missing is the backend behind them.

- [x] **Wire the editor to the endpoint that already worked.**
      `/public/coffeeconfig` was ported but nothing called it; the SPA called
      `/public/admin/coffeeconfig`, which 404s. It now calls the real path, and
      every mutation carries the CSRF token the backend requires — it did not
      before, so a save would have been a 403 even at the right path.
- [x] **Port the storefront.** `GET /public/storefront?voucher=CODE` reads the
      CoffeeConfig with the participant's own token and returns the shape the
      order screen already expected.
- [x] **Port orders.** `POST /public/orders` prices the basket and enforces the
      voucher's `maximumUsage`, which is the bug the demo is built around: the
      storefront keeps showing the discount, and the failure appears at submit.
      The limit is read from the CoffeeConfig on every order, so raising it in
      Git unblocks the next order with no restart. That behaviour is pinned by
      a test.
- [x] Keep credentials scoped per request; no fallback server writes. Tested
      against a controlled upstream: the participant's own token is what goes on
      the wire, `Impersonate-*`/`X-Remote-*` from the browser never do, and an
      unusable token fails rather than falling back.
- [ ] **Port the editor's live watch and change history**
      (`/public/admin/coffeeconfig/{watch,changes,changes/stream}`). Both were
      SSE on the deleted session. The screens now re-read on demand instead of
      pretending to be live, and `applyIncomingConfig` — the conflict machinery
      the watch fed — is deliberately kept for when it returns.
- [x] **Show real voucher usage in the editor.** `GET /public/vouchers` reports
      this process's redemption counts, and the admin screen shows them beside
      each `maximumUsage`. Without it the screen read "Used 0 / 1" while orders
      were failing — the wrong diagnosis at the worst moment. The response
      carries `scope: "process"` because the number is one replica's, since boot.
- [ ] **Port the admin orders view** (`/public/admin/orders{,/debug,/stream}`).
      Blocked on the persistence decision below: there is nothing to list while
      orders exist only in memory.
- [ ] **Port quiz forwarding**, then retire the ForwardAuth remnant in
      `frontend/src/api/kube.ts` — the `X-Join-Code` header and the
      direct-to-apiserver path belong to the deleted model, and `AnswerScreen`
      still depends on them.
- [ ] Preserve conditional writes and surface 409 conflicts in the editor. The
      backend already passes a 409 through unchanged (there is a test), but the
      editor sends `{spec}` with no `resourceVersion`, so nothing is actually
      conditional yet — a concurrent edit silently wins.
- [ ] **Decide order and voucher persistence.** Redemptions are currently
      counted in process memory (`voter/coffee_vouchers.go`), which is honest
      for one replica and wrong for two: each would keep its own tally and the
      effective limit would double. A restart also forgives every redemption.
      This must be settled before `replicas > 1`.
- [ ] Decide whether the demo needs the quiz journey at all. Cutting it removes
      most of what is left in this section.

Route status after this pass:

| Frontend calls | Backend |
| --- | --- |
| `/public/coffeeconfig` (GET, PATCH) | **wired** |
| `/public/storefront` | **ported** |
| `/public/orders` | **ported** |
| `/public/vouchers` | **new** — redemption counts for the editor |
| `/public/storefront/watch` | removed from the screen; not ported |
| `/public/admin/coffeeconfig{/watch,/changes,/changes/stream}` | not ported |
| `/public/admin/orders{,/debug,/stream}` | not ported |

**Acceptance:** an attendee logs in, orders, and edits; an ungranted external
account gets an understandable denial; the expected username appears in the
audit log and in the Git output.

Where that stands: login, order and edit now exist and are covered by tests
against a fake API server. The denial path is tested at the handler level
(Kubernetes' 403 reaches the browser unchanged). Neither the audit-log name nor
the Git output has been observed end to end in the cluster — the first needs a
real login through the deployed build, the second needs section 2.

## 2. Make "saved" mean something honest

- [ ] **Install ConfigButler, or stop asking for commits.** Verified live: the
      cluster has no `commitrequests.configbutler.ai` CRD and no controller, so
      today *every* save fails its second step. Nothing is silently wrong — the
      backend already reports partial success — but the demo's payoff does not
      exist in this cluster.
- [x] Surface the partial failure in the editor. The save result carries
      `saved` and `committed` separately, and the screen now shows "Saved, but
      not committed" with the reason instead of an unqualified success. Covered
      by a frontend test.
- [ ] `committed: true` still means a `CommitRequest` was *created*. Either
      observe the resulting Git commit or change the wording to "submitted".
      Three distinct states belong in the response: Kubernetes save,
      CommitRequest accepted, Git commit observed.

This matters more than its size suggests — the whole talk is "your change
becomes a commit". Claiming it without proving it is the one thing a reviewer in
the room will catch.

## 3. Prove the access policy rather than describing it

The containment argument lives in a *templated* CEL expression. It is a
privilege boundary and it is currently only argued, not tested.

- [ ] Render the actual platform authenticator and evaluate it per connector:
      valid identity, missing/unknown connector, missing claims, a forged
      operator email arriving from Room Pass, non-`demo:` groups and system
      groups. Read the **rendered** output, not the template. Include the
      transitional `room` id while it is still accepted.
- [ ] Use a disposable API server to test real tokens and RBAC: room attendee,
      unrelated GitHub/LinkedIn user, named owner, unauthenticated caller.
      Assert an allowed CoffeeConfig patch and denied Secret reads, RBAC
      changes, impersonation, deletion, and writes outside `voter`.
- [ ] **Audit what `system:authenticated` grants**, now that a stranger with a
      LinkedIn account can obtain a valid token from this issuer. "No named
      grant" is not the same as no access.
- [ ] Decide whether attendees may edit *every* CoffeeConfig in `voter` (the
      current Role permits it), read other submissions, and submit to a closed
      quiz. Enforce object and lifecycle limits with scoped RBAC or admission,
      and test direct API calls as well as application requests.
- [ ] Review the `linkedin:simon@configbutler.ai` **cluster-admin** binding,
      which trusts LinkedIn's email verification for cluster-admin.
- [ ] Before opening GitHub login to everyone, review every Dex client
      independently. Replace oauth2-proxy's broad `emailDomains: ["gmail.com"]`
      gate and Grafana's single-address JMESPath with the intended
      allowed-groups expression once the operator team slug is settled (it is
      the placeholder `koudijs-dev:the-specific-group` throughout the platform
      repo). Three clients enforce one intent through three mechanisms; that
      drifts.

**Acceptance:** a connector × identity × operation matrix runs against rendered
policy and real authorization. Login eligibility is never an implicit grant.

## 4. Exercise the browser, not just Go HTTP clients

A Go client cannot reproduce how a browser sends `Origin` — which is exactly why
the `Referrer-Policy: no-referrer` outage reached production invisibly.

- [x] Chromium tests for the Room Pass + Dex fixture: enrollment, stable return
      identity, invalid code, CSRF, cookie flags, closed enrollment. Found and
      fixed the cross-origin form-redirect CSP bug. They pass locally in ~2s.
- [ ] **Get that browser check green in CI — it has never once completed there.**
      It was added in `dd9cac4`, whose run was cancelled by the next push, and
      its first real execution (2026-09-10, on `a938699`) hit the test job's
      30-minute timeout. The job was cancelled and the image jobs were
      **skipped**, so a fully green commit produced no deployable image. It now
      has its own job and budget, but the cold-bringup cost is still unmeasured
      and unoptimised: `task room-pass:e2e-up` creates a k3d cluster, builds the
      room-pass image and deploys Dex and Traefik on every run.
- [x] Operational metrics with bounded labels and scrape-time handoff gauges.
- [ ] **Browser-driven login through the real Voter app**, not only the fixture:
      browser Origin behaviour, returning enrollment, logout, expiry.
- [ ] State/nonce mismatch, wrong issuer/audience/signature, and successful
      callback replay against a controlled issuer through the real callback.
- [ ] Demonstrate that stopping a Room blocks new handoff while an issued token
      stays usable until expiry unless its authorization is removed.
- [ ] Preserve unsaved input across re-login; stop and reconnect streams cleanly.
- [ ] Get a disposable-cluster browser smoke test into CI. `task test-e2e` is
      not a CI gate today and drives the flow with a Go client.
- [x] Give the frontend a unit-test runner. `task test` now runs vitest before
      the type-check. The first suite covers the API client, where the bugs are
      not type errors: a request to a path the backend no longer serves, or a
      mutation missing its CSRF header, compiles and builds perfectly. Both of
      those were real, and both are now pinned.
- [ ] Extend frontend tests past the API client to the screens themselves
      (component tests need a DOM environment; only `vitest` and the node
      environment are installed today).

Worth a look while here: `voter/oidc.go:372` still sets `Referrer-Policy:
no-referrer`, and Voter's CSRF check *rejects* `null` Origin — the same pairing
that broke Room Pass. It is currently harmless (the SPA document is served by
the static handler, which does not set the header, so its fetches carry a real
Origin), but a browser test is what would keep it that way.

## 5. Platform and operational follow-up

Items in the private `ConfigButler/k8s` checkout, not this repository.

- [ ] **Unstall the FluxInstance.** Root cause found: flux-operator emits a
      JSON-patch at `/spec/versions/1/…` of the Receiver and Alert CRDs, gated
      on `VersionInfo.Minor <= 8`. The cluster runs flux-operator **0.48.0**,
      which predates that gate, while `distribution.version: "2.x"` floated to
      **v2.9.5**, where those CRDs no longer have a second version. Fix: bump
      `inputs.version` in `2-gitops/clusters/course-cluster/operator.yaml` from
      `0.48.0` toward `0.59.0` (the version in `external/flux-operator`, which
      has the gate), or pin `distribution.version` to `2.8.x` as a stopgap. A
      floating distribution against a pinned operator is the underlying defect.
- [ ] **Deploy current `main`.** Both images are behind HEAD, so the metrics
      listener and the CSP fix are not running. Bump the tags in
      `2-gitops/voter-demo/{app,room-pass}.yaml`.
- [ ] **Remove the bare-email operator subjects** at
      `2-gitops/auth/rbac/humans-rbac.yaml:59` and `:81`. They were kept so the
      reboot could not lock anyone out. Confirm an OIDC `kubectl auth whoami`
      reports `github:simonkoudijs@gmail.com` first — a `kubectl` run with the
      admin client cert reports `admin` and proves nothing here.
- [ ] **Finish the `room` → `room-pass` rename.** Every *functional* place is
      already `room-pass` (verified live: Dex connector `id: room-pass` with no
      `room` connector remaining, Traefik matching
      `Path(/callback/room-pass) || PathPrefix(/room-pass)`, and both
      `OIDC_CONNECTOR_ID` and `CONNECTOR_ID` set to `room-pass`). What is left:
      - `legacyConnectorIds: ["room"]` at `1-talos/values.yaml:112`. The
        rendered authenticator on the nodes still accepts `'room'` in both the
        validation and the containment rule, for an id nothing can issue any
        more. Removing it needs a rolling reboot, so batch it with the next
        authenticator change; roll one node at a time and re-check the md5
        across all three.
      - `2-gitops/auth/authentication-config.reference.yaml:39,44,51` still
        reads `['github', 'room']` and has no `linkedin` branch at all. It is a
        hand-maintained approximation of a privilege boundary that no longer
        matches the rendered one — delete it, or generate it from the template.
      - Comments naming the old path: `2-gitops/auth/dex/networkpolicy.yaml:5,8,22`,
        `2-gitops/voter-demo/README.md:19,25`, and
        `2-gitops/voter-demo/room-pass.yaml:94,97`. One actively misdirects —
        `2-gitops/auth/dex/ingressroute.yaml:14` warns "do not add a rule
        matching `/callback/room`", which is no longer the path that needs
        protecting.
- [ ] Verify the platform-owned manifests against this code: rendered routes,
      identities, permissions, and the new metrics Service port.
- [ ] Record source revision, image digest, deployment revision and rollback
      steps, so a demo-day failure has a known way back.

## 6. Tidying, none of it urgent

- [ ] Delete the 7 leftover `Participant` objects from testing (via Git; the
      namespace is Flux-owned).
- [ ] Clear ~71 `ContainerStatusUnknown` pod tombstones left by the reboots.
- [ ] `web-preview-pr-13` is in `ImagePullBackOff` and its Kustomization is
      failing. Unrelated to this demo, but it is noise in every status check.
- [ ] `Room`'s `Ends` print column is `type=date` at
      `room-pass/api/v1alpha1/types.go:93`, which kubectl renders as an *age* —
      so a future `endsAt` always shows `<invalid>`. Should be `type=string`.
- [ ] The join page placeholder is `BCD-FGH`, implying a dashed 7-character
      code, but real codes are 6 undashed characters. Dashes are stripped
      server-side, so it is cosmetic — but it has already misled one person.
- [ ] Consider whether the demo needs the `/answer` quiz flow at all, or whether
      the coffee journey alone carries the talk. Cutting it would remove most of
      section 1's remaining work.

## Traps worth not rediscovering

Three things that each cost a working session.

**Dex only emits `federated_claims` for the `federated:id` scope.** The entire
connector-prefix design rests on that claim. A client that omits the scope gets
a token with no connector and is now rejected outright. Dex v2.44.0 does not
advertise the scope in its discovery document; that omission is cosmetic.

**`Referrer-Policy: no-referrer` makes browsers send `Origin: null` on form
POSTs.** curl implements no referrer policy and neither does a Go HTTP client,
so the entire test suite passed while every real browser was rejected. Suspect
this whenever a form works under curl and fails in a browser.

**`talm template --offline` silently drops networking.** The render loses
`LinkConfig` and `Layer2VIPConfig` and randomises the hostname; applying it to
three control planes would have taken the cluster off the network with no API
left to recover through. The actual fix was dropping `-i`, which belongs to
maintenance-mode bringup. Always diff a fresh render against the config on disk.

Also: Dex reads connector credentials from `envFrom` at pod start, so
`podAnnotations.configbutler.ai/secrets-revision` must be bumped whenever
`dex-secrets.yaml` changes, or the new value is silently ignored.
