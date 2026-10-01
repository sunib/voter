# Browser tests: editor, live stream and voting

```bash
task e2e-up
task test-browser
task e2e-down   # when finished
```

This runs real Chromium against the Voter application in the local fixture
([`../e2e/up.sh`](../e2e/up.sh)): Traefik, Dex, a released Room Pass and the Voter
image built from this checkout. Every test signs in the way an attendee does --
Voter starts the OIDC flow, Dex hands off to Room Pass, the browser fills in the
room code and a display name -- and then exercises the application.

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

The suite runs the real Voter editor across independent browsers and completes a
voting round with two enrolled identities. It checks required-answer rejection, text
serialization, refreshed results, duplicate prevention and closure. It does not
claim production capacity or GitHub/LinkedIn login works. CI builds the local
fixture, runs these tests and retains recordings/reports for seven days.

The shared-stream regression withdraws the fixture audience grant while retaining
one named viewer, then restores the original subjects in `finally`. It proves warm
cache refusal, draft preservation and continued delivery to the authorized viewer.
Do not run other fixture clients during this test. The separate [200-identity load
rehearsal](../../docs/shared-streams.md) also requires exclusive fixture use.

Metrics assertions use the authenticated Kubernetes pod proxy to port 9090. The
browser also verifies that the application origin returns 404 for `/metrics`.

[`rehearse-production.mjs`](rehearse-production.mjs) is different: a one-off run
against the real demo at demo.koudijs.dev. Read its header before using it.
