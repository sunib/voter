# Plan: move Voter's login, sessions and streams to krm-foyer

Status: **done (2026-10-06).** Live on `demo.koudijs.dev`: Voter **2.0.0** (`b446793`)
behind krm-foyer **0.3.0**, deployed from ConfigButler/k8s `2ebda2e`. Steps 0 to 7 are
done; steps 2 to 6 were built on one branch (PR #27) and released together as 2.0.0.
How the system works now is [ARCHITECTURE.md](../ARCHITECTURE.md); this page is the
record of how it got there. What is still open is collected under
[Left over](#left-over).
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
- [x] The operator's round switch and audience grant, end to end: step 6 gave the
      fixture an operator, and `operator.spec.js` drives both.
- [x] The fixture's audience Role has the cluster's `databases` grants (step 5).
- [x] Cluster copy of the policy: 7.2 (`2ebda2e`).

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
- [x] `voting.spec.js` waits for the count from status (step 6).

### 5. The domain backend behind the identity check

Done 2026-10-06 on the same branch. Checked on the fixture: an order with the
`TESTNET` voucher is placed through the identity check (total 0) and the feed shows
the participant's name; signed out, a forged `Krm-Foyer-Identity` gets krm-foyer's
401; signed in, a forged one is replaced by krm-foyer's, so the feed still shows the
real name; a write without CSRF proof is krm-foyer's 403; a pod other than Traefik
cannot reach Voter. Voter logs an order under the API server's `demo:` name.

- [x] One middleware, `requireIdentity` (`foyer_identity.go`), reads
      `Krm-Foyer-Identity` in place of `requireParticipant` and refuses requests
      without it, logging that the route is wrong. Every refusal it passes on is still
      logged (the refusal recorder from the 2026-09-17 post-mortem moved with it).
- [x] Storefront and orders read the CoffeeConfig with Voter's ServiceAccount; the
      `voter` Role has `get` on `demo-coffee`. **Behaviour change:** anyone signed in
      sees the storefront, where before a person outside the audience group got the
      API server's 403. Who may *edit* the menu is unchanged, decided on the `/k8s`
      write. A 401 or 403 from the API server is now Voter's own credential failing,
      reported as a 502 and logged.
- [x] `/join-room?code=`: done in step 2.
- [x] Deleted what krm-foyer replaced: Voter's OIDC client, session cookie, CSRF check,
      participant clients and `/auth/*` (`oidc.go`, `oidc_handlers.go`,
      `participant_session.go`, `session_cookie.go`, `participant_kube.go`,
      `participant_authz.go`), their config (`OIDC_*`, cookie keys, `APP_ORIGIN`,
      `PARTICIPANT_COOKIE_NAME`, `SESSION_COOKIE_MAX_AGE_SECONDS`), and the dead
      `normalizeJoinCodeHeader` and `clientIP`. `go mod tidy` dropped the OIDC, oauth2
      and secure-cookie modules. `STREAM_KUBECONFIG` stays (Voter's own credential).
- [x] The fixture's `voter` Role is the reconciler's grants plus `get` on the
      CoffeeConfig; the stream gateway's grants and its SubjectAccessReview
      ClusterRole are gone. The audience Role has the cluster's `databases` grants.
- [x] Fixture edge: `/public/` → Voter through `foyer-identity` (ForwardAuth to
      `http://krm-foyer.voter.svc/auth/check?identity=true`) and `no-cookie`; every
      other Voter route `no-cookie`, ungated. A NetworkPolicy admits only Traefik to
      Voter's port. `up.sh` waits for krm-foyer's 401 on `/public/storefront`.
- [x] Found while doing this: the talk has a person `kubectl apply` their own Database
      request (`participant-rbac.yaml` grants `update` for it), which step 3's policy
      refused because of `apply`'s last-applied annotation. `voter-editable-spec` now
      allows that annotation on Databases; still not on CoffeeConfigs.

### 6. Prove it before the cluster

Done 2026-10-06 on the same branch, against the fixture.

- [x] Voter's browser e2e through krm-foyer: **13 tests in five files, all passing.**
      (Until the docs pass after the cutover, `playwright.config.js` matched only
      three of the files, so `join-and-logout` and `boundaries` had not run; widened
      and run then, both passed first time.)
      - QR → join → vote: `join-and-logout.spec.js` follows the QR link from another
        origin to a join page with the code filled in; `voting.spec.js` votes, sees
        results update, is refused a second vote and a vote after close (admission's
        message).
      - Order with a voucher: checked by hand in step 5; the live-stream specs cover
        the menu the order reads.
      - The operator: `operator.spec.js` signs in as the fixture's operator, sees the
        QR code, opens and closes a round, and grants and revokes the menu. The
        live-stream specs edit the menu under that grant. No ConfigButler in the
        fixture, so "produces a commit" is left to 7.6.
      - Logout of both programs: `join-and-logout.spec.js`.
- [x] A participant cannot do what they could not before: `boundaries.spec.js` sends a
      ballot in someone else's name, one without pins, a second one, and a label patch
      on the menu, each refused by the API server in the policy's words.
- [x] **The load rehearsal, 200 participants over 60 s** (`task voter:voteload`, now
      on krm-foyer): each signs in through krm-foyer, Dex and Room Pass, holds the
      CoffeeConfig's stream open, and votes through `/k8s`. **200/200** completed;
      the tally counted 200/200. At the peak, 197 streams open, all on krm-foyer's
      one shared watch (`krm_foyer_upstream_watches_open{identity="shared"} 1`,
      `{identity="user"} 0`); the API server's CoffeeConfig watches stayed at 2.
      p95: login 43 ms, stream snapshot 4 ms, ballot 5 ms; end to end p99 95 ms.
      One shared transport, one source IP and a local fixture: an optimistic floor,
      not a room of 200 phones on conference Wi-Fi.
- [x] The fixture gained an operator: Dex's `mockCallback` connector under the
      `github` id signs in `github:kilgore@kilgore.trout`, cluster-admin as the
      cluster's operator is. Dex's own `/callback` is routed to Dex, and a second
      NetworkPolicy admits Traefik to Dex for it; Room Pass keeps
      `/callback/room-pass`.
- [x] The fixture's audience may only read the menu, as on the cluster; editing is
      the `voter-audience-coffee-admin` grant.
- [x] Found by the suite: a ballot cast in the first seconds of a round had no
      questions digest to pin and could never be sent. The page now waits for it
      (up to 8 s), and so does the harness.
- [ ] The harness leaves its participants enrolled, and the fixture Room takes 300: a
      second run of 200 found the room full. Clean up between runs
      (`kubectl -n voter delete participants` by display-name prefix), or teach
      `voteload` to.
- [ ] The Vite loop (step 1).

### 7. Cutover in `external/k8s` (pull first, see AGENTS.md)

Done 2026-10-06 in one change set, ConfigButler/k8s `2ebda2e`, applied by Flux. No
Talos change (decision 2).

1. [x] **Room Pass CRDs** (`b1b1ef0`, earlier the same day): the Room CRD is v2.1.0's.
   On a later Room Pass upgrade, refresh it again in the same change.
2. [x] **RBAC and admission:** participants hold `get` and `create` on
   `quizsubmissions`, no `list` or `watch`. Voter's ServiceAccount holds the tally's
   grants and `get` on `coffeeconfigs/demo-coffee`; the stream gateway's grants and its
   `voter-stream-access-review` ClusterRole are gone. `voter-ballot` and
   `voter-editable-spec` are in `voter-demo/admission/`, copied from
   `voter/config/admission/`. The `voter-audience-coffee-admin` RoleBinding stays out of
   Git.
3. [x] **krm-foyer for Voter** (`voter-demo/krm-foyer.yaml`): 0.3.0 in the `voter`
   namespace, chart and image by digest, client `voter` reading `voter-oidc-client`,
   `connector_id` defaulting to `room-pass` and allowing `github` and `linkedin`, shared
   watches for coffeeconfigs, quizsessions, databases, commitrequests and rooms (identity
   `krm-foyer-voter-shared`, named so the hello example's release cannot collide), its
   own session keys (`krm-foyer-session-keys.yaml`, SOPS), and a NetworkPolicy admitting
   `traefik-system` (metrics: `monitoring`). The hello example on
   `foyer.k8s.koudijs.dev` is untouched.
4. [x] **Edge** (`ingress.yaml`): krm-foyer's four prefixes; Room Pass's three paths as
   before; `/join-room` to Voter with `observability: {accessLogs: false}` (the cluster
   runs Traefik 3.7 with JSON access logs on); `/public/` through `foyer-identity` and
   `no-cookie`; everything else `no-cookie`, ungated. A NetworkPolicy admits only
   `traefik-system` to Voter.
5. [x] **Voter 2.0.0**, by hand as a major must be, with every login setting and
   `METRICS_ADDRESS` removed. Nothing scraped Voter's `:9090`. The image policy follows
   `>=2.0.0 <3.0.0`, or Flux would have put 1.3.1 back; it resolved 2.0.0 the same
   minute. `voter-app-cookie` in `secrets.yaml` is unused now but stays: its SOPS MAC is
   shared with the other secrets in that file.
6. **Check:**
   - [x] From outside: every route answers as designed: `/auth/session` and `/k8s/api`
     are krm-foyer's 401, `/public/storefront` is the identity check's 401, `/join-room`
     refuses a malformed code and redirects a valid one into krm-foyer's login, which
     ends at Dex (`client_id=voter`) and on Room Pass's join form; `/metrics` is a 404.
   - [x] A participant in a real browser: the QR link followed from another origin
     reached Room Pass with the code filled in; `/auth/session` showed `room-pass` and
     `demo:voter-audience`, `/auth/whoami` a `demo:` user; the rounds and the menu
     streamed; the storefront answered through the identity check; no request failed.
   - [ ] A QR login from a phone, the first time Safari is tried.
   - [ ] A vote that appears in Git (`ConfigButler/k8s-trail`) under the participant's
     name, and an operator save that produces a CommitRequest and a commit.
   - [ ] `/auth/whoami` for the operator (`github:…`, cluster-admin, as decided).
   - [x] Flux image automation follows 2.x.

Rollback: revert `2ebda2e`. Dex and the API server are untouched, so Voter 1.3.1 works
again as soon as it is back.

## Found along the way

- [x] **The stream-expiry fix** (#24) was squash-merged under a `test:` title and so
      released nothing; 1.3.1 carried it, and 2.0.0 deleted that code.
- [x] **The QR code's target.** `/auth/login?code=` became krm-foyer's, which ignores
      the code, so the QR on `/room` points at Voter's `/join-room`. Room Pass's `room-qr`
      tool needs `LOGIN_PATH=/join-room` too ([demo-runbook.md](demo-runbook.md)).
- [x] **The docs.** Rewritten for the running system after the cutover: ARCHITECTURE.md,
      authorization.md, talk-checklist.md, deliberate-simplifications.md, voting-demo.md,
      demo-runbook.md, the READMEs and the browser suite's README. Dated records
      (shared-streams.md, post-demo-2026-09-17.md) keep their content with a note.
- [x] **The fixture had never exercised the QR handoff.** Room Pass's join page was on
      `join.voter.test`, so Voter's host-only join cookie never reached it there. It
      shares `app.voter.test` now (step 1).
- [x] **A ballot in the first seconds of a round could never be cast**: the questions
      digest it pins appears with the round's first tally. The page waits for it (step
      6).
- [x] **The release PR for 2.0.0 was not merged by the release workflow.** It searches
      for the PR by label one second after release-please opens it, and the search did
      not see the label yet; #28 was merged by hand. Then the image job failed on a
      transient `go mod download` error from `proxy.golang.org`, which skipped the
      release until the failed job was re-run. Both are worth fixing in the workflow:
      take the PR number from release-please's `pr` output, and give `go mod download`
      a cache mount and a retry (gitops-reverser does the former).
- [x] **The live menu still held the 2026-09-17 demo's edits** (one product, a shop name
      of 312 ☕). Re-seeded the documented way, by deleting `CoffeeConfig/demo-coffee`;
      Flux recreated it from Git.

## Left over

- [ ] The three production checks in 7.6 that need a person: a phone in Safari, the
      operator's GitHub login, and a vote and a menu save reaching Git.
- [ ] **The room's menu-editing grant has been on since 2026-09-17 10:01.** Turn it off
      on `/room` before a talk, or demo 1's refusal does not happen.
- [ ] The Vite loop against the fixture (step 1), and with it deleting or shrinking
      `frontend/dev/kube-mock-plugin.ts`.
- [ ] An envtest suite for both admission policies in CI
      ([quiz-admission.md](quiz-admission.md)); today they are proven on the fixture by
      impersonation and by `boundaries.spec.js`.
- [ ] `voteload` leaves its participants enrolled; clean up between runs, or teach it to.
- [ ] Decision 2's revisit: a narrow browser identity for the operator, before the
      operator account is used on a shared machine or anything else is served from
      `demo.koudijs.dev`.

## Open points to take back to krm-foyer

- **The shared-watch identity's grants are cluster-wide.** The chart grants
  `list`/`watch` through a ClusterRole, while Voter's own ServiceAccount holds a Role in
  `voter`. A namespaced option would match Voter's scope. It is small and not blocking.
- **The Vite proxy recipe has not been run** by their suite, nor yet by ours (left over).
- **The QR flow has not been tested in Safari.** Our 7.6 phone check is the first.
- **Feedback from running it:** the Traefik ForwardAuth recipe worked as written; a
  brand-new resource's status (our questions digest) lags its creation, which a
  domain that pins status fields has to wait out in the browser.
