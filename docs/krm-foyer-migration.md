# Plan: move Voter's login, sessions and streams to krm-foyer

Status: **in progress (2026-10-06).** Steps 0 and 2 are done, and step 1 apart from
the Vite loop. Steps 2 to 6 are built on one branch, `krm-foyer-frontend-contract`
(draft PR #27), which must not merge before step 6: from step 2 on, `/public/*` needs
Voter's old session and the browser suite is red. Nothing is deployed yet; the cluster
runs Voter 1.3.0 with its own login, and krm-foyer only as the hello example on
`foyer.k8s.koudijs.dev`. Work found along the way is under
[Found along the way](#found-along-the-way).
It replaces the "not yet" in [k8s-front-adoption.md](k8s-front-adoption.md). The asks in
[krm-foyer-feedback.md](krm-foyer-feedback.md) are all answered on krm-foyer's `main`.

## Is krm-foyer complete enough?

**Yes, from 0.3.0.** Everything Voter asked for is in
[0.3.0](https://github.com/ConfigButler/krm-foyer/releases/tag/v0.3.0) (2026-10-06),
which contains `fae70b6`. 0.2.0, which the cluster runs for the hello example, does not
have `/auth/check`.

| Voter needed | Since | Evidence |
| --- | --- | --- |
| OIDC login, sealed cookie sessions that survive a restart | 0.2.0 | e2e against Dex |
| `connector_id` chosen by the login link, `connector` in the session | 0.2.0 | e2e |
| `/k8s` as the user, `/stream/v1` with shared watches and SubjectAccessReview rechecks | 0.2.0 | e2e, 200-identity rehearsal |
| Identity for a domain backend (`/auth/check?identity=true`, `Krm-Foyer-Identity`) | 0.3.0 | e2e through real Traefik, forged header refused |
| The Room Pass QR login, end to end | 0.3.0 | Chromium spec against Room Pass 2.0.0 and its Dex ([room-pass.md](https://github.com/ConfigButler/krm-foyer/blob/main/docs/room-pass.md)) |
| A Traefik `IngressRoute` recipe for one shared host | 0.3.0 | Run by the fixture |
| A narrow browser identity for the cluster-admin operator | 0.3.0 (docs) | e2e spec, `foyer_scope_test.go`; not used for now ([decision 2](#decision-2-cluster-admin-in-the-browser-accepted-for-now)) |
| A Vite dev-server proxy | 0.3.0 (docs) | Written, **not run** by their suite |

Known limits we accept, because downtime is acceptable:

- One replica.
- A rollout briefly refuses connections. krm-foyer parked this as
  [rollout-connections.md](https://github.com/ConfigButler/krm-foyer/blob/main/docs/investigations/rollout-connections.md).
- No refresh. Room Pass's `authproxy` issues no refresh tokens anyway.
- Logout does not revoke a copied cookie.
- Safari is untested for the QR flow; only Chromium is.

The cutover logs everyone out once. That is fine: there is no event planned in the
window.

## What Voter becomes

Voter keeps one binary and one Deployment. It does less:

| Stays in Voter | Goes to krm-foyer | Deleted |
| --- | --- | --- |
| The SPA's files (`static.go`) | `/auth/*`, `/k8s/*`, `/stream/v1` | `oidc.go:1-400`, `oidc_handlers.go`, `participant_session.go`, `session_cookie.go` |
| `/join-room`, the QR join endpoint (the cookie part of `oidc.go:401-484`) | | `participant_kube.go`, `http_handlers.go`'s credential seam |
| The quiz reconciler and tally | | `stream_runtime.go`, `participant_stream.go` |
| Storefront, orders, vouchers, order SSE (`/public/…`), behind the identity check | | `participant_authz.go` (`/auth/rules`) |
| Votes: see [decision 1](#decision-1-how-votes-reach-kubernetes) | | `participant_coffee.go`, `participant_databases.go`, `participant_save.go`, `operator_audience_grant.go`, most of `participant_quiz.go` |

That deletes about 2,000 of 4,950 lines of Go. In the frontend, `api/http.ts`,
`api/session.ts` and the per-feature REST wrappers shrink to calls on `/k8s`. The
krm-stream client moves to `/stream/v1`.

## Decisions (taken 2026-10-06)

| | Decision | Consequence |
| --- | --- | --- |
| 1 | **Votes: option A.** The participant creates the submission through `/k8s`; admission and the reconciler enforce the rules | Ballots keep their Git attribution; the vote handler is deleted (step 4) |
| 2 | **The operator keeps `cluster-admin` in the browser, for now** | No Talos change and no operator Role in this migration |
| 3 | **krm-foyer reuses the `voter` Dex client** | No new client, audience or return URL |

### Decision 1: votes are created by the participant (option A)

QuizSubmissions are committed to Git (`git-sink-demo1.yaml`, `git-sink-demo2.yaml`),
attributed to the participant through `user.extra`. That attribution needs the write to
be made **with the participant's token**, and `/auth/check` deliberately gives a
backend no token. So the participant creates the ballot through `/k8s`:

- A ValidatingAdmissionPolicy on `quizsubmissions` create:
  - The name is `<round>-<lowerAscii(display-name extra)>`. That is exactly today's
    `submissionName`, and Room Pass display names are unique per room and already
    folded to `[A-Za-z0-9-]`. A second vote stays a 409 AlreadyExists.
  - The `submitter` and `round` labels match the display-name extra and `spec`.
  - Only `demo:` usernames, which means only the `room-pass` connector, may create
    a ballot like this. The operator keeps a path for the "Casting your own answers"
    interlude, but must declare it (see [quiz-admission.md](quiz-admission.md)).
  - The round is `live`, and the ballot's `roundUID` and `questionsDigest` match it.
    The policy reads the round as a parameter, so this is checked when the ballot is
    created and the refusal is synchronous.
- The reconciler counts a ballot only if all of these hold:
  - its `spec.roundUID`, when present, equals the round's `uid`, so a ballot for a
    deleted round never counts for a new one under the same name;
  - its `spec.questionsDigest`, when present, equals the digest of the round's
    current questions. Not
    `generation`: `state` is in the round's `spec`, so opening and closing each move
    the generation, and a ballot checked at count time would stop matching the moment
    the round closes. Today's handler gets away with it because it checks at vote
    time. The reconciler computes the digest from `spec.questions` and the browser
    copies it into the ballot, so nothing has to canonicalise JSON in two languages;
  - it passes `validateQuizAnswers`, which it already applies.
- `status.openedAt` and `status.closedAt` are for reading, not a counting rule. Voter
  stamps them when it observes the change, so a round that changes state while Voter
  is down, as on any image bump, is stamped late. A rule of "created after
  `openedAt`" would then drop every vote cast before the restart. The live window is
  enforced when the ballot is created: by the handler today, by the policy next.
- The browser keeps `validateQuizAnswers`' checks for immediate feedback.

The rejected option B kept the vote handler behind `/auth/check?identity=true`, writing
as Voter's ServiceAccount. Every ballot commit would then be authored by
`system:serviceaccount:voter:voter`, which loses the demo's point.

### Decision 2: cluster-admin in the browser, accepted for now

The operator signs in through GitHub as `github:simonkoudijs@gmail.com`, which is
`cluster-admin`. Through krm-foyer, **any script on `demo.koudijs.dev` holds that while
the operator is signed in**. Today Voter's backend holds the same token, but uses it
only in specific handlers. This is accepted for now.

What keeps the exposure small:

- The operator signs in on their own device only, and signs out after the demo.
- No third-party scripts are on the origin: the SPA is built from this repository.
- The audience never gets this identity. Room Pass logins are `demo:` users with the
  narrow participant Role.

**Revisit before** the operator account is used on a shared or untrusted machine, or
before anything other than Voter's own build is served from `demo.koudijs.dev`. The fix
is krm-foyer's
[browser identity](https://github.com/ConfigButler/krm-foyer/blob/main/docs/application-scope.md#a-browser-identity-in-kubernetes):
in `1-talos/files/authentication-config.yaml`, tokens from the `voter` client get their
own username prefix with a narrow `voter-operator` Role, and `github:` keeps
cluster-admin. That is a Talos change: roll it out one control-plane node at a time and
inspect a fresh `talm template` render first ([PLAN.md 6](../PLAN.md#6-retained-platform-work)).
The expression has to key on both the client and the connector, so that the `demo:`
and `linkedin:` mappings stay unchanged.

### Decision 3: krm-foyer reuses the `voter` Dex client

The client's redirect URI, `https://demo.koudijs.dev/auth/callback`, is already the URL
krm-foyer serves. The API server already accepts `aud: voter`, and Room Pass's allowed
return URLs need no change. This avoids the cross-client-scope arrangement that
`foyer.k8s.koudijs.dev` needed. krm-foyer reads the client secret from the existing
`voter-oidc-client` Secret (step 7.3), so nothing is re-encrypted; Voter stops reading
it.

## Room Pass 2.1.0

Room Pass released **2.1.0** on 2026-10-06 (`df2070d`). Flux's image automation has
already moved the cluster to it (`9b54a57` in `external/k8s`). For this migration:

- **The contracts the cutover relies on are unchanged.** The join-code cookie
  ([qr-join.md](https://github.com/sunib/room-pass/blob/main/docs/qr-join.md)), the
  handoff ([handoff.md](https://github.com/sunib/room-pass/blob/main/docs/handoff.md))
  and the `/join`, `/bind`, `/logout` paths on the shared host are as in 2.0.0.
  krm-foyer's end-to-end QR evidence was run against 2.0.0, so our step 6 is the first
  run against 2.1.0.
- **The cluster's Room CRD was stale; fixed 2026-10-06** (`b1b1ef0` in `external/k8s`).
  `voter-demo/crds/room-pass.koudijs.dev_rooms.yaml` was still 2.0.0's, without
  `spec.appearance`, so the API server would have silently dropped the field. It is now
  v2.1.0's. The Participant CRD did not change.
- **`spec.appearance`** dresses the join page: tagline, picture, accent, background.
  - Pictures are paths on the join host, which is `demo.koudijs.dev`, because the join
    page's CSP is `img-src 'self'`.
  - They must load without a login, since participants have not signed in yet. In the
    step 7.4 edge they are Voter's files on the catch-all route, which has no login
    gate. Keep it that way, and keep `/talks/` (or whatever path is chosen) off any
    `foyer-gate` route.
  - Editing `appearance`, like any Room spec change, starts a fresh join-code epoch.
    Set it before the event, not during it.
- **2.1.0's `config/crd` and `deploy/base` work as remote kustomize bases.** Voter's
  e2e (`test/e2e/room-pass/kustomization.yaml`) can use them in place of the raw file
  URLs (step 1).
- **Not ours:** 2.1.0's Dex storage changes (state in Kubernetes, `deploy/dex-crds`)
  apply to Room Pass's reference `deploy/dex`. This cluster runs its own Dex from
  `auth/dex`, so nothing changes here.

## Steps

Each step ends green on its own checks. Steps 1 to 6 happen in this repository and its
e2e. Step 7 is the only cluster change.

### 0. The krm-foyer release

- [x] krm-foyer **0.3.0** (2026-10-06) contains `fae70b6` (`/auth/check`). Pin image and
      chart by digest, as `2-gitops/krm-foyer/release.yaml` does:
      - chart `oci://ghcr.io/configbutler/charts/krm-foyer:0.3.0@sha256:40f68f1ba1477e09a9813d304991518f600310b1f31e3a600634ba3a0c1ad0d2`
      - image `ghcr.io/configbutler/krm-foyer:0.3.0@sha256:6b620d658fdc3ff2fad394d2df27142ff706e8a4d20496ef810e6d45136ff6cf`
      It also moves krm-stream to 0.10.0, past the 0.7.0 that step 2 names; check the
      browser client's version against the gateway it pins.

### 1. Local loop and e2e on krm-foyer

- [ ] Vite (unblocked now that step 2 gives a krm-foyer session to develop against):
      follow krm-foyer's dev-server proxy recipe (`/auth/`, `/k8s/`, `/stream/`,
      `/_foyer/` to a `task demo` krm-foyer, on `https://foyer.localhost:8443`). Proxy
      `/public/` to a local Voter with a fixed `Krm-Foyer-Identity` header. Their
      suite does not run this recipe, so report anything that breaks back to them.
- [ ] Delete `frontend/dev/kube-mock-plugin.ts`, or shrink it to a mock of `/public/`
      only.
- [x] `test/e2e`: install krm-foyer's chart beside Voter, with client `voter`, and take
      the routes from krm-foyer's `test/e2e/cluster/room-pass/routes.yaml` (Room Pass,
      krm-foyer and the app on one host). `authentication-config.yaml` needs no change.
      Done 2026-10-06: 0.3.0 by digest (`test/e2e/krm-foyer-values.yaml`), client
      `voter-fixture`, plain HTTP with a NetworkPolicy admitting only Traefik. It has
      `/k8s/`, `/stream/` and `/_foyer/`; **`/auth/` stays Voter's until step 2**, so
      nobody has a krm-foyer session yet. `up.sh` waits for its 401 on `/k8s/api`.
- [x] `test/e2e/room-pass/kustomization.yaml`: Room Pass `v2.0.0` → `v2.1.0`, image
      pinned by digest, `config/crd?ref=v2.1.0` as a remote base in place of the two
      raw CRD URLs (2.1.0 moved them to `config/crd/bases/`). Done 2026-10-06; the
      browser suite passed 10/10 against it.
- [x] `JOIN_ORIGIN` was `join.voter.test`; it is now the shared `app.voter.test`, with
      Room Pass on exactly `/join`, `/bind` and `/logout` through prioritised
      IngressRoutes, as in production (`test/e2e/edge.yaml`). Done 2026-10-06.

### 2. Frontend on krm-foyer's contract

Done 2026-10-06 on branch `krm-foyer-frontend-contract`, checked in Chromium against
the fixture: QR link from another site → Room Pass's join page with the code filled in
→ signed in through krm-foyer; `/auth/session` and `/auth/whoami` from krm-foyer; the
rounds and the CoffeeConfig live over `/stream/v1`; the rules table from a review
through `/k8s`; sign-out ends on Room Pass's "already enrolled" page with krm-foyer's
cookie gone. What still fails is exactly `/public/*`, which needs Voter's old session
until steps 3 to 5. **The browser suite is red from here until step 6.**

- [x] krm-stream browser client 0.4.0 → **0.10.0** (what krm-foyer 0.3.0's gateway
      runs; 0.7.0 was the plan's guess), stream URL `/public/stream` → `/stream/v1`.
      `connectManagedResourceStream` became `connectResourceStream` with a callback
      (krm-stream `docs/migrating.md`).
- [x] `api/session.ts` reads krm-foyer's `/auth/session`. `expiresAt` is a timestamp
      string now, and there is no Kubernetes username in it: `/auth/whoami` has that.
      `canVote(session)` compares the connector with `participantConnector` from
      `/config.json`, for display only, so the browser holds no second copy of the rule.
- [x] Application settings come from `/config.json`, which Voter serves from its
      environment without a session (`namespace`, `coffeeConfigName`, `roomName`,
      `commitURLTemplate`, `participantConnector`).
- [x] Login links go through `loginURL(returnTo, connector)`: `return_to=` and
      `oidc.connector_id=`. krm-foyer defaults the connector to `room-pass`. Logout is
      `POST /auth/logout`; a Room Pass participant then goes to `/join` for its own
      sign-out form, the operator to the signed-out screen.
- [x] One fetch wrapper (`api/http.ts`), with the CSRF header the session names.
- [x] `/auth/whoami` (JSON) for the Kubernetes name; a SelfSubjectRulesReview posted to
      `/k8s/apis/authorization.k8s.io/v1/selfsubjectrulesreviews` in place of
      `/auth/rules`, flattened in the browser. The "as YAML" links became the raw
      review, expandable on the page.
- [x] **Brought forward from step 5:** `/join-room?code=` in Voter (`join_room.go`),
      because the QR code pointed at `/auth/login?code=`, which is krm-foyer's now. The
      QR in `RoomScreen.vue` points there.
- [x] **Brought forward from step 5:** Voter's own krm-stream gateway is gone
      (`stream_runtime.go`, `participant_stream.go`, `/public/stream`), and with it
      the `/metrics` listener, which served only its counters; krm-foyer has its own.
      The tally reconciler keeps a plain service-account client
      (`service_account.go`). `krm-stream/gateway` stays in `go.mod` only for
      `Project` and `ValidateMergePatch` in the CoffeeConfig and Databases handlers,
      which step 3 deletes.
- [x] Fixture: krm-foyer has `/auth/` too, defaults to the `room-pass` connector, and
      shares watches on coffeeconfigs, quizsessions, databases and rooms.

### 3. Resources through `/k8s`

Done 2026-10-06 on the same branch. Every write below is the person's own, through
`/k8s`, with `fieldManager=voter` so `managedFields` does not credit "Mozilla"
(`frontend/src/api/kube.ts`). Checked in Chromium against the fixture: a participant
saves the menu at `/admin` (a `200` merge patch, the cluster holds the new value), and
a label patch sent from the same page by hand is refused by the policy with its own
message. **krm-stream is gone from `voter/go.mod`.**

- [x] CoffeeConfig: get and conditional merge patch on
      `/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/coffeeconfigs/<name>`,
      with `metadata.uid` and `metadata.resourceVersion` in the patch as the API
      server's preconditions. Then the browser `POST`s the CommitRequest, reporting
      partial success when only the first write landed. Reads are projected like the
      stream (`krm-full/v1`: no `managedFields`, no last-applied annotation), so the
      editor's reconciliation sees the same shape.
- [x] A ValidatingAdmissionPolicy keeping a person's write to `spec`
      (`voter/config/admission/editable-spec-policy.yaml`): on CoffeeConfigs and
      Databases, no label, annotation, finalizer or owner-reference changes, except the
      intent annotation on a Database. It matches people only (`!startsWith('system:')`),
      so Flux, gitops-reverser and other controllers are untouched. Tested by
      impersonation on the fixture: spec changes pass; labels, annotations and
      finalizers are refused for `demo:` and `github:` alike; `system:` users pass.
      **A person's `kubectl apply` on these objects is refused too**, since `apply`
      writes its last-applied annotation; they change through Git.
- [x] Databases: list, create and patch through `/k8s`. The name check, the intent
      annotation and the CommitRequest (only when `databaseGitTargetName` is set) are
      in the browser (`api/databases.ts`).
- [x] Round state: a merge patch of `quizsessions/<name>` `spec.state` through `/k8s`.
- [x] The audience coffee-admin grant: the operator page reads, creates and deletes
      the RoleBinding through `/k8s`, with the group from `Room.spec.audienceGroup` and
      the same labels, annotation and missing-Role refusal as before.
- [x] `/config.json` carries what these need: `gitTargetName`, `databaseGitTargetName`,
      `commitRequestNamespace`, `commitCloseDelaySeconds`, `audienceCoffeeAdminRole`.
- [x] Deleted: `participant_coffee.go`, `participant_save.go`, `participant_databases.go`,
      `operator_audience_grant.go`, the round-state handler, and their tests.
- [ ] Not run end to end yet: the operator's round switch and audience grant. The
      fixture's Dex has no GitHub connector, so there is no operator to sign in as
      there; step 6 has to give the fixture one (a second static user through the same
      authproxy, mapped to `github:`), or test them against the cluster at 7.6.
- [ ] The fixture's audience Role has no `databases` grants, unlike the cluster's
      `participant-rbac.yaml`; add them so step 6 can drive the Databases pages.
- [ ] Cluster copy of the policy: 7.2.

### 4. Votes (decision 1)

The admission half of this step can ship before krm-foyer, on Voter 1.x:
see [quiz-admission.md](quiz-admission.md).

- [x] CRD fields, digest and pins: [quiz-admission.md](quiz-admission.md) phase 1
      (#21, released as 1.3.0 and on the cluster).
- [x] The ValidatingAdmissionPolicy described under decision 1:
      [quiz-admission.md](quiz-admission.md) phase 2, checked by impersonation on the
      fixture. Its envtest suite is still open there.
- [x] The browser creates the ballot at `/k8s/.../quizsubmissions` (`api/ballot.ts`,
      `api/quiz.ts`): checked answers (`validateAnswers`, a port of
      `validateQuizAnswers` with the same cases), the name and labels admission
      expects, both pins, from the round it read. "Already voted" is a `GET` of its own
      name, and a second create's 409 reads as AlreadyVoted.
- [x] Rounds are read through `/k8s` too, and results come from the round's status
      alone: the REST results endpoint is gone. Right after a vote the count lags by
      about a second (measured: 0 at 77 ms, 1 at 1.1 s), so the results page says
      "Counting your vote…" rather than "0 votes recorded".
- [x] Deleted the round list, vote and results handlers. What the reconciler needs
      (types, `validateQuizAnswers`, `decodeQuizSpec`) is `quiz_rules.go`.
- [x] Checked in Chromium on the fixture: an empty submit says "answer required"; a
      vote lands with labels and pins; a second visit knows it voted. A late voter
      never reaches admission: the page disables Submit as soon as the stream says
      the round closed.
- [ ] `voting.spec.js` asserts "1 votes recorded" straight after a vote; with the
      count from status it has to wait for it (step 6).

### 5. The domain backend behind the identity check

- [ ] One middleware reads `Krm-Foyer-Identity` (base64url JSON: `userInfo`,
      `displayName`, `connector`) in place of `requireParticipant`. It refuses requests
      without it. The header can only be trusted behind the route that asks
      `?identity=true`, and the NetworkPolicy below enforces that.
- [ ] Storefront and orders read CoffeeConfig with Voter's ServiceAccount: add `get` on
      `coffeeconfigs` to the `voter` Role. Order and voucher logic is unchanged.
- [x] `/join-room?code=`: the cookie part of today's `setJoinCodeHandoff`, then a `302`
      to `/auth/login?return_to=%2F&oidc.connector_id=room-pass`. The QR code in
      `RoomScreen.vue` points here. Done in step 2 (`join_room.go`); the access log for
      this route is a cluster matter, see 7.4.
- [x] Delete Voter's stream gateway (`stream_runtime.go`, `participant_stream.go`,
      `/public/stream`, `/metrics`). Done in step 2.
- [ ] Delete the rest of what krm-foyer replaced (see
      [What Voter becomes](#what-voter-becomes)) and its config (`OIDC_*`, cookie
      keys, `APP_ORIGIN`). Delete the dead `normalizeJoinCodeHeader` and `clientIP`
      while there. **Keep `STREAM_KUBECONFIG`:** it is now Voter's own credential
      outside a cluster, which the reconciler needs; in a pod it is unset anyway.
- [ ] The `voter` Role (fixture `test/e2e/voter.yaml`, cluster `app.yaml`) still
      carries the stream gateway's grants: `list`/`watch` on the CoffeeConfig and the
      Room, and the `voter-stream-access-review` ClusterRole for SubjectAccessReviews.
      Drop them; keep the reconciler's (`quizsessions` get/list/watch,
      `quizsubmissions` list/watch, `quizsessions/status` get/patch) and add the
      storefront's `get coffeeconfigs` above. Rewrite the Role's comment, which still
      explains the shared watch.

### 6. Prove it before the cluster

- [ ] Voter's browser e2e, through krm-foyer:
      - QR → join → vote;
      - order with a voucher;
      - the operator grants coffee-admin and an audience member saves the menu, which
        produces a commit;
      - logout of both programs.
- [ ] One check that a participant cannot list or create what they could not before:
      a raw submission with someone else's name, a CoffeeConfig label patch.
- [ ] A load rehearsal at 200 sessions through krm-foyer's shared watches, as
      PLAN.md 3 asks for Voter's own gateway today.
- [ ] Rewrite the browser specs for the new contract. `live-stream.spec.js` asserts
      Voter's `voter_stream_*` metrics through the pod proxy and that `/metrics` is a
      404; both are gone, and the shared-watch evidence is krm-foyer's metrics now.
      `voting.spec.js` and `operator.spec.js` sign in through Voter's login.
- [ ] Rewrite `voter/test/loadtest` (`voteload`), which signs in through Voter's
      `/auth/login`, reads `/auth/session` and votes through `/public/rounds`.
- [ ] Check `test/browser/rehearse-production.mjs` against the new flow before it is
      next run against the demo.

### 7. Cutover in `external/k8s` (pull first, see AGENTS.md)

Downtime is acceptable, so this is one change set, in this order. There is no Talos
change (decision 2), so everything goes through Flux.

1. ~~**Room Pass CRDs**~~ **Done 2026-10-06** (`b1b1ef0`): the Room CRD is v2.1.0's.
   On a later Room Pass upgrade, refresh it again in the same change.
2. **RBAC** (`participant-rbac.yaml`, `app.yaml`):
   - Participants: `quizsubmissions` loses `list` and `watch` if the results screen no
     longer needs them; keep `get` and `create` (decision 1).
   - Voter's ServiceAccount loses the stream gateway's grants (step 5's Role item).
   - Add `get coffeeconfigs` for Voter's ServiceAccount.
   - Add the two ValidatingAdmissionPolicies and their bindings: the ballot policy
     (step 4) and `voter-editable-spec` from `voter/config/admission/`.
   - The `voter-audience-coffee-admin` RoleBinding stays out of Git, as today.
3. **krm-foyer for Voter:** a HelmRelease in the `voter` namespace (no cross-namespace
   Traefik service), with these values:
   - client `voter` (decision 3), reading the existing `voter-oidc-client` Secret
     (`oidc.clientSecret: {secretName: voter-oidc-client, key: VOTER_OIDC_CLIENT_SECRET}`),
     so no secret is re-encrypted. Dex's `voter` client and its redirect URI stay as
     they are;
   - `login.authorizationParameters.connector_id` allowing `[room-pass, github]`;
   - `sessionClaims.connector: /federated_claims/connector_id`;
   - `sharedWatches.resources`: coffeeconfigs, quizsessions, databases, commitrequests
     and rooms;
   - its own session keys (SOPS);
   - the NetworkPolicy for Traefik and monitoring.

   Leave `foyer.k8s.koudijs.dev` and the hello example alone, or retire them in a
   separate change.
4. **Edge** (`ingress.yaml`), on `demo.koudijs.dev`:
   - `/auth/`, `/k8s/`, `/stream/`, `/_foyer/` → krm-foyer;
   - `/join`, `/bind`, `/logout` → Room Pass, as today;
   - `/join-room` → Voter, access log off. The room code is in its query. Per-route
     `observability: {accessLogs: false}` needs Traefik 3.1 or later; check the
     cluster's version, or confirm its access log is off. The fixture's k3s Traefik is
     2.11 with no access log;
   - `/public/` → Voter with the `foyer-identity` ForwardAuth and `no-cookie`
     middlewares;
   - everything else → Voter with `no-cookie`, ungated, which also serves any Room
     `appearance` pictures.

   Add a NetworkPolicy that admits only Traefik to Voter's port, so nothing else can
   send it a `Krm-Foyer-Identity` header.
5. **Voter 2.0.0.** The cutover commit in this repository is `feat!:`, so its major
   release does not reach the cluster on its own (AGENTS.md). Bump `app.yaml` by hand
   in the same change set, and remove the env vars Voter no longer reads (`OIDC_*`,
   cookie keys, `APP_ORIGIN`, `METRICS_ADDRESS`), and the pod's `9090` port if
   `app.yaml` declares one. Check nothing scrapes Voter's `:9090` (a PodMonitor,
   ServiceMonitor or scrape annotation); scrape krm-foyer's metrics Service instead.
6. **Check:**
   - a QR login from a phone, which is the first time Safari is tried, against
     Room Pass 2.1.0;
   - a vote that appears in Git under the participant's name;
   - an operator save that produces a CommitRequest;
   - `/auth/whoami` for a participant (`demo:…`) and for the operator (`github:…`,
     cluster-admin, as decided);
   - Flux image automation still bumps `app.yaml`.

Rollback: revert the `external/k8s` change set and pin `app.yaml` to the last 1.x. Dex
and the API server are untouched, so the old Voter works again as soon as it is back.

## Found along the way

Things the work so far turned up that are not one of the steps above.

- [ ] **The stream-expiry fix is not released.** `TestSharedStreamExpiryAndDisconnectIsolation`
      was flaky because krm-stream 0.4.0 clamps each write's deadline to the context's;
      the fix (cancel at expiry instead) is on `main` since #24, but #24 was
      squash-merged under its `test:` title, so no release carries it. The next `fix:`
      or `feat:` release on `main` does. It matters only until the cutover, which
      deletes that code.
- [ ] **Room Pass's `room-qr` tool** points QR codes at `LOGIN_PATH`, `/auth/login` by
      default, which becomes krm-foyer's and ignores `code=`. Set
      `LOGIN_PATH=/join-room` wherever the runbook or `task room-pass:present` runs it.
- [ ] **Docs to rewrite at the cutover.** They describe Voter 1.x as deployed, which
      stays true until step 7, so they change with it rather than before:
      - `ARCHITECTURE.md`: the ownership table (Voter no longer owns the session or a
        stream engine), the routes table (`/auth/*`, `/auth/rules`, `/public/stream`,
        `/metrics`), the two stream locks, the QR flow
        (`/auth/login?code=` → `/join-room`);
      - `docs/authorization.md`: `/auth/rules` becomes a review through `/k8s`;
      - `docs/shared-streams.md`: Voter's gateway is gone, and shared watches are
        krm-foyer's (`sharedWatches`); keep it as history;
      - `PLAN.md` 2a and 3: the 200-session rehearsal runs through krm-foyer;
      - `test/browser/README.md`: metrics through the pod proxy;
      - `docs/talk-checklist.md`: "this cluster runs no policy or webhook", and the
        ballot policy described as not built; it is built, and so is
        `voter-editable-spec`;
      - `docs/deliberate-simplifications.md` entry 4: an operator's vote is refused
        by admission now, not by the application, unless it is declared;
      - `docs/voting-demo.md`: voting through Voter's endpoints;
      - `ARCHITECTURE.md`'s ownership table says the cluster runs no admission
        policy.
- [x] **The fixture had never exercised the QR handoff.** Room Pass's join page was
      on `join.voter.test`, so Voter's host-only join cookie never reached it there.
      It shares `app.voter.test` now (step 1), and the step 2 browser check followed
      the QR link from another site to a join page with the code filled in.

## Open points to take back to krm-foyer

- **The shared-watch identity's grants are cluster-wide.** The chart grants
  `list`/`watch` through a ClusterRole, while Voter's own ServiceAccount holds a Role in
  `voter`. A namespaced option would match Voter's scope. It is small and not blocking.
- **The Vite proxy recipe has not been run** by their suite. Our step 1 will be the
  first real run.
- **The QR flow has not been tested in Safari.** Our step 7.6 phone check is the first.
