# Browser tests: voting, operator, live stream, join and boundaries

```bash
task e2e-up
task test-browser
task e2e-down   # when finished
```

This runs real Chromium against the Voter application in the local fixture
([`../e2e/up.sh`](../e2e/up.sh)): Traefik, Dex, a released Room Pass, krm-foyer and
the Voter image built from this checkout, on `app.voter.test` with the same edge as
production. Every participant signs in the way an attendee does -- krm-foyer starts
the OIDC flow, Dex hands off to Room Pass, the browser fills in the room code and a
display name -- and then exercises the application through krm-foyer's `/k8s` and
`/stream/v1`.

The fixture also has an operator. Dex's `mockCallback` connector, registered under the
`github` id, signs in one fixed user, `github:kilgore@kilgore.trout`, bound to
cluster-admin as the cluster's GitHub operator is. It has no login page of its own.

The specs:

| File | What it proves |
| --- | --- |
| [`voting.spec.js`](voting.spec.js) | Two participants vote, see results from the round's `status`, and cannot vote twice or after closure (admission's refusal, through `/k8s`) |
| [`operator.spec.js`](operator.spec.js) | A participant is refused the operator page and never sees a join code; the operator sees the QR code, opens and closes a round, and grants and revokes the menu |
| [`live-stream.spec.js`](live-stream.spec.js) | The editor across browsers: live updates, conflicts, the 409 re-send, a price over €10 refused by `voter-coffee-price` in its own words, and the shared-watch withdrawal below |
| [`join-and-logout.spec.js`](join-and-logout.spec.js) | The QR link followed from another origin reaches Room Pass with the code filled in; logout ends krm-foyer's session and Room Pass's |
| [`boundaries.spec.js`](boundaries.spec.js) | What the pages never send -- a ballot in someone else's name, one without pins, a second one, a label on the menu -- is refused by Kubernetes, in the policy's words |

All five files run: 16 tests, passing against the fixture on 2026-10-07.
[`playwright.config.js`](playwright.config.js) matches every `*.spec.js`; it used to
name three files, which kept the last two out of every run until that was found.

Room Pass's own login tests (enrollment, returning participants, invalid codes,
tampered CSRF forms, cookie flags, closed enrollment) live with Room Pass in
[sunib/room-pass](https://github.com/sunib/room-pass), not here.

The test runner reads only `.local/kubeconfig` at the repository root, never the
current user context. Kubernetes supplies the rotating code. Tests delete only
Participants with their unique test display name. The voting test also deletes its
uniquely named round and the QuizSubmissions labeled with that round UID. Do not run
concurrently with the other fixture suites or against a shared presentation room.

The local fixture uses self-signed TLS and synthetic identities. Chromium ignores
that certificate only for this test configuration; host resolver rules map the
`*.voter.test` hosts to the isolated Docker gateway. No external provider
credentials are needed. The task installs Chromium and its OS libraries; the latter
may use sudo.

Videos are retained for every test. Inspect `test-results/` or run
`npx playwright show-report` from this directory. Failure traces can be opened with
`npx playwright show-trace`. Artifacts are gitignored and contain local test
sessions; treat them as sensitive if adapting the tests for a different environment.

The suite does not claim production capacity or that real GitHub/LinkedIn login
works. CI builds the local fixture, runs these tests and retains recordings/reports
for seven days.

The shared-watch spec signs in two viewers on krm-foyer's shared watch of the
CoffeeConfig, withdraws the fixture audience grant from one of them, then restores
the original subjects in `finally`. It proves the withdrawn viewer is refused within
60 seconds with its unsaved draft kept, that a fresh `/stream/v1` request gets no
object from the warm cache, and that the other viewer keeps receiving updates. Do
not run other fixture clients during this test.

Metrics are krm-foyer's: `krm_foyer_*`, read through the authenticated Kubernetes
service proxy to the `krm-foyer-metrics` Service on port 9090. The browser also
verifies that the application origin returns 404 for `/metrics`; Voter exports none.

The 200-participant rehearsal is a separate Go program, `task voter:voteload` (needs
exclusive use of the fixture): each participant signs in through krm-foyer, holds the
menu's stream open and casts a ballot through `/k8s`. Results are in
[krm-foyer-migration.md](../../docs/krm-foyer-migration.md), step 6.

[`rehearse-production.mjs`](rehearse-production.mjs) is different: a one-off run
against the real demo at demo.koudijs.dev. Read its header before using it.
