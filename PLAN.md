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

---

## 1. Restore one complete application journey

**This is the biggest gap and everything else is smaller.** The demo is a login
with almost no app behind it.

The frontend screens are kept deliberately — they are the restoration target,
not dead weight. What is missing is the backend behind them.

- [ ] **Wire the one endpoint that already works.** `/public/coffeeconfig` is
      ported to participant tokens but *no frontend code calls it*.
      `frontend/src/api/coffee.ts:103,120` still calls
      `/public/admin/coffeeconfig`. This is the cheapest possible win and it
      makes the editor screen live.
- [ ] **Port the storefront**, since that is the screen a participant lands on
      after login. Today `/` calls `/public/storefront` and gets a 404.
- [ ] **Port orders**, then editor watches/history, then quiz forwarding.
      Inventory each route's identity, resource/verb, CSRF requirement and
      401/403 behaviour *before* implementing it. `participant_coffee.go` is the
      pattern.
- [ ] **Retire the ForwardAuth remnant in `frontend/src/api/kube.ts`** as quiz
      forwarding is restored — the `X-Join-Code` header and the
      direct-to-apiserver path belong to the deleted model.
- [ ] Keep credentials scoped per request; no fallback server writes.
- [ ] Preserve conditional writes and surface 409 conflicts in the editor.
- [ ] Decide order/voucher persistence and rollout semantics before adding
      replicas.

The dead routes, for the inventory:

| Frontend calls | Backend |
| --- | --- |
| `/public/storefront`, `/public/storefront/watch` | gone |
| `/public/orders` | gone |
| `/public/admin/coffeeconfig{,/watch,/changes,/changes/stream}` | gone |
| `/public/admin/orders{,/debug,/stream}` | gone |
| `/public/coffeeconfig` | **ported, not wired** |

**Acceptance:** an attendee logs in, orders, and edits; an ungranted external
account gets an understandable denial; the expected username appears in the
audit log and in the Git output.

## 2. Make "saved" mean something honest

- [ ] `committed: true` currently means a `CommitRequest` was *created*. Either
      observe the resulting Git commit or change the UI contract to say
      "submitted". Distinguish three states in the response: Kubernetes save,
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
      identity, invalid code, CSRF, cookie flags, closed enrollment. In CI with
      retained recordings. Found and fixed the cross-origin form-redirect CSP bug.
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
