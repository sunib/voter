# Plan: move Voter's login, sessions and streams to krm-foyer

Status: **plan, decisions taken, not started (2026-10-06).** Nothing here is implemented
or deployed. Work starts once krm-foyer releases `/auth/check` (step 0).
It replaces the "not yet" in [k8s-front-adoption.md](k8s-front-adoption.md). The asks in
[krm-foyer-feedback.md](krm-foyer-feedback.md) are all answered on krm-foyer's `main`.

## Is krm-foyer complete enough?

**Yes, once the release after 0.2.0 is cut.** Everything Voter asked for is on `main` at
`fae70b6`, but not yet in a tagged release. 0.2.0 does not have `/auth/check`.

| Voter needed | On krm-foyer `main` | Evidence |
| --- | --- | --- |
| OIDC login, sealed cookie sessions that survive a restart | 0.2.0 | e2e against Dex |
| `connector_id` chosen by the login link, `connector` in the session | 0.2.0 | e2e |
| `/k8s` as the user, `/stream/v1` with shared watches and SubjectAccessReview rechecks | 0.2.0 | e2e, 200-identity rehearsal |
| Identity for a domain backend (`/auth/check?identity=true`, `Krm-Foyer-Identity`) | `fae70b6` | e2e through real Traefik, forged header refused |
| The Room Pass QR login, end to end | `fae70b6` | Chromium spec against Room Pass 2.0.0 and its Dex ([room-pass.md](https://github.com/ConfigButler/krm-foyer/blob/main/docs/room-pass.md)) |
| A Traefik `IngressRoute` recipe for one shared host | `fae70b6` | Run by the fixture |
| A narrow browser identity for the cluster-admin operator | `fae70b6` (docs) | e2e spec, `foyer_scope_test.go`; not used for now ([decision 2](#decision-2-cluster-admin-in-the-browser-accepted-for-now)) |
| A Vite dev-server proxy | `fae70b6` (docs) | Written, **not run** by their suite |

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
  - Only `demo:` usernames, which means only the `room-pass` connector, may create.
- The reconciler counts a ballot only if all of these hold:
  - its `spec.roundUID` equals the round's `uid`, so a ballot for a deleted round
    never counts for a new one under the same name;
  - its `spec.questionsDigest` equals the round's `status.questionsDigest`. Not
    `generation`: `state` is in the round's `spec`, so opening and closing each move
    the generation, and a ballot checked at count time would stop matching the moment
    the round closes. Today's handler gets away with it because it checks at vote
    time. The reconciler computes the digest from `spec.questions` and the browser
    copies it into the ballot, so nothing has to canonicalise JSON in two languages;
  - it was created at or after the round opened and before it closed, which needs
    `status.openedAt` and `status.closedAt`. The UID and the digest are both readable while
    the round is still `ready`, so only `openedAt` stops a ballot cast before the round
    goes live;
  - it passes `validateQuizAnswers`, which it already applies.
- The browser keeps `validateQuizAnswers`' checks for immediate feedback. The server
  answer becomes "not counted" rather than a synchronous 422.

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

### 0. Wait for the krm-foyer release

- [ ] A krm-foyer release containing `fae70b6` (`/auth/check`). Pin image and chart by
      digest, as `2-gitops/krm-foyer/release.yaml` does.

### 1. Local loop and e2e on krm-foyer

- [ ] Vite: follow krm-foyer's dev-server proxy recipe (`/auth/`, `/k8s/`, `/stream/`,
      `/_foyer/` to a `task demo` krm-foyer, on `https://foyer.localhost:8443`). Proxy
      `/public/` to a local Voter with a fixed `Krm-Foyer-Identity` header. Their
      suite does not run this recipe, so report anything that breaks back to them.
- [ ] Delete `frontend/dev/kube-mock-plugin.ts`, or shrink it to a mock of `/public/`
      only.
- [ ] `test/e2e`: install krm-foyer's chart beside Voter, with client `voter`, and take
      the routes from krm-foyer's `test/e2e/cluster/room-pass/routes.yaml` (Room Pass,
      krm-foyer and the app on one host). `authentication-config.yaml` needs no change.
- [x] `test/e2e/room-pass/kustomization.yaml`: Room Pass `v2.0.0` → `v2.1.0`, image
      pinned by digest, `config/crd?ref=v2.1.0` as a remote base in place of the two
      raw CRD URLs (2.1.0 moved them to `config/crd/bases/`). Done 2026-10-06; the
      browser suite passed 10/10 against it.
- [ ] Today `JOIN_ORIGIN` is `join.voter.test`; move it to the one shared host.

### 2. Frontend on krm-foyer's contract

- [ ] krm-stream browser client 0.4.0 → 0.7.0 (the gateway krm-foyer pins), stream URL
      `/public/stream` → `/stream/v1`. Check projections (`krm-spec/v1`) and the error
      events, which changed since 0.4.0.
- [ ] `api/session.ts` reads krm-foyer's `/auth/session` (`displayName`, `groups`,
      `connector`, `csrfToken`, `csrfHeader`, `expiresAt`). `canVote` becomes
      `connector === 'room-pass'`, for display only.
- [ ] Application settings (`namespace`, `coffeeConfigName`, `roomName`,
      `commitURLTemplate`) come from a static `/config.json` that Voter serves,
      rendered from its environment. They are no longer in the session.
- [ ] Login links: `return=` → `return_to=`, `connector=` → `oidc.connector_id=`
      (`AdminScreen.vue`, `DatabaseEditScreen.vue`, `RoomScreen.vue:221`,
      `OrderScreen.vue`). Logout: `POST /auth/logout`, then to Room Pass's `/join` for
      its own sign-out form (room-pass.md, "Logout is two programs").
- [ ] Merge the two fetch wrappers (`api/http.ts`, `api/quiz.ts:1-21`) into one, using
      the CSRF header name from the session.
- [ ] `/auth/whoami` is JSON now. `IdentityScreen.vue` and `useAuthorization.ts` post a
      SelfSubjectRulesReview to `/k8s/apis/authorization.k8s.io/v1/selfsubjectrulesreviews`
      instead of `/auth/rules`. `flattenResourceRules` moves to the browser.

### 3. Resources through `/k8s`

- [ ] CoffeeConfig: get and conditional merge patch on
      `/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/coffeeconfigs/<name>`,
      with the uid and resourceVersion precondition in the patch body, as
      `participant_save.go` does. Then `POST` the CommitRequest from the browser, with
      the same partial-success reporting as today.
- [ ] A ValidatingAdmissionPolicy keeping a CoffeeConfig update to `spec`: no label,
      annotation or finalizer changes by anyone but GitOps. It replaces
      `decodeSaveIntent` and `ValidateMergePatch`. Check gitops-reverser's own writes
      are exempt.
- [ ] Databases: list, create and patch through `/k8s`. The intent annotation and commit
      receipt (`addCommitReceipt`) move to the browser.
- [ ] Round state: `PATCH` `quizsessions/<name>` `spec.state` through `/k8s` (operator).
- [ ] The audience coffee-admin grant: the operator page creates and deletes RoleBinding
      `voter-audience-coffee-admin` through `/k8s`. The group still comes from
      `Room.spec.audienceGroup`, now read in the browser. Labels and annotations as in
      `operator_audience_grant.go:97-125`.

### 4. Votes (decision 1)

- [ ] CRD: `spec.roundUID` and `spec.questionsDigest` on QuizSubmission;
      `status.openedAt`, `status.closedAt` and `status.questionsDigest` on QuizSession. The reconciler sets
      `openedAt` on the first transition to `live` only, sets `closedAt` on each
      transition to `closed`, and clears it on a reopen. Reopening keeps the votes
      already cast, as today.
- [ ] The ValidatingAdmissionPolicy described under decision 1, with tests for:
      - another participant's name;
      - a GitHub user;
      - a second vote;
      - a missing display-name extra.
- [ ] The reconciler counts only ballots that match the round's UID and questions digest,
      were created between `openedAt` and `closedAt`, and pass validation. Tests for
      early, late, changed-questions and recreated-round ballots, and
      a reopened round keeping its first window's votes.
- [ ] The browser creates the submission at `/k8s/.../quizsubmissions`. "Already voted"
      is a `GET` of its own deterministic name, or the 409 on create.
- [ ] Delete the vote, round and results handlers from `participant_quiz.go`.

### 5. The domain backend behind the identity check

- [ ] One middleware reads `Krm-Foyer-Identity` (base64url JSON: `userInfo`,
      `displayName`, `connector`) in place of `requireParticipant`. It refuses requests
      without it. The header can only be trusted behind the route that asks
      `?identity=true`, and the NetworkPolicy below enforces that.
- [ ] Storefront and orders read CoffeeConfig with Voter's ServiceAccount: add `get` on
      `coffeeconfigs` to the `voter` Role. Order and voucher logic is unchanged.
- [ ] `/join-room?code=`: the cookie part of today's `setJoinCodeHandoff`, then a `302`
      to `/auth/login?return_to=%2F&oidc.connector_id=room-pass`. The QR code in
      `RoomScreen.vue:118` points here. Turn the access log off for this route, because
      the code is in the query.
- [ ] Delete what krm-foyer replaced (see [What Voter becomes](#what-voter-becomes)) and
      its config (`OIDC_*`, cookie keys, `APP_ORIGIN`, `STREAM_KUBECONFIG`). Delete the
      dead `normalizeJoinCodeHeader` and `clientIP` while there.

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

### 7. Cutover in `external/k8s` (pull first, see AGENTS.md)

Downtime is acceptable, so this is one change set, in this order. There is no Talos
change (decision 2), so everything goes through Flux.

1. ~~**Room Pass CRDs**~~ **Done 2026-10-06** (`b1b1ef0`): the Room CRD is v2.1.0's.
   On a later Room Pass upgrade, refresh it again in the same change.
2. **RBAC** (`participant-rbac.yaml`, `app.yaml`):
   - Participants: `quizsubmissions` loses `list` and `watch` if the results screen no
     longer needs them; keep `get` and `create` (decision 1).
   - Add `get coffeeconfigs` for Voter's ServiceAccount.
   - Add the two ValidatingAdmissionPolicies and their bindings.
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
   - `/join-room` → Voter, access log off;
   - `/public/` → Voter with the `foyer-identity` ForwardAuth and `no-cookie`
     middlewares;
   - everything else → Voter with `no-cookie`, ungated, which also serves any Room
     `appearance` pictures.

   Add a NetworkPolicy that admits only Traefik to Voter's port, so nothing else can
   send it a `Krm-Foyer-Identity` header.
5. **Voter 2.0.0.** The cutover commit in this repository is `feat!:`, so its major
   release does not reach the cluster on its own (AGENTS.md). Bump `app.yaml` by hand
   in the same change set, and remove the env vars Voter no longer reads (`OIDC_*`,
   cookie keys, `APP_ORIGIN`, `STREAM_KUBECONFIG`).
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

## Open points to take back to krm-foyer

- **The shared-watch identity's grants are cluster-wide.** The chart grants
  `list`/`watch` through a ClusterRole, while Voter's own ServiceAccount holds a Role in
  `voter`. A namespaced option would match Voter's scope. It is small and not blocking.
- **The Vite proxy recipe has not been run** by their suite. Our step 1 will be the
  first real run.
- **The QR flow has not been tested in Safari.** Our step 7.6 phone check is the first.
