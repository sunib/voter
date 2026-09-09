# CI/CD

This directory holds the pipeline. It is also where the CI/CD to-do list lives —
if you want a check to exist one day, write it under [Backlog](#backlog) rather
than leaving it in a chat log.

For *how the pipeline works* — the trust model, the jobs, which digest to pin in
`platform/` — see [`docs/ci.md`](../docs/ci.md). This file is the map and the
backlog, not a second copy of that explanation.

> Note: a root `README.md` exists, so GitHub uses that as the repository front
> page and this file stays an ordinary document.

## What is here

| Path | Purpose |
|---|---|
| [`workflows/ci.yml`](workflows/ci.yml) | The whole pipeline: validate on every PR, publish on a push to `main` |
| [`actions/ci-container/`](actions/ci-container/action.yml) | Makes the CI container available to a job — pulled on trusted runs, loaded from an artifact on PRs |
| [`dependabot.yml`](dependabot.yml) | Keeps the pinned action SHAs, base-image digests and dependencies moving |

## The one rule

**The workflow knows how to move things around. It never knows how to check
them.** Every check runs as a `task` inside the container built from
[`.devcontainer/Dockerfile`](../.devcontainer/Dockerfile) — the same container we
develop in.

So when you pick up something from the backlog below, the shape of the work is
almost always:

1. Add or change a task in [`Taskfile.yaml`](../Taskfile.yaml) (or a component
   Taskfile). Run it locally until it does what you want.
2. Only then add a step to `ci.yml` that runs that task inside the CI container.

If you find yourself writing the logic of a check in YAML, that is the signal to
stop and put it in a task instead. The payoff is that a red job reproduces
locally with the command printed in its own log.

## Backlog

Roughly in the order they earn their keep.

### 1. Room Pass end-to-end suite in CI

`task test-e2e` creates a k3d cluster, deploys Dex and Traefik and drives a real
login, including the cross-host gate handoff. It is the check that matters most
for stage 2 of the platform implementation plan, and right now nothing in CI
runs it — the `test` job only covers unit tests, the frontend build and envtest.

The work: a job that installs k3d on the runner (or runs it via the CI
container's docker-outside-of-docker socket, as the `images` job already does),
runs `task room-pass:e2e-up`, `task room-pass:e2e`, and tears down in an
`if: always()` step. Budget for it being the slowest job by far, and for it being
the flakiest — the reverser's e2e job is the model for how to keep it honest.

Until this lands, "CI is green" does not mean a participant can actually log in.

### 2. Image vulnerability scanning

`trivy` is already in the CI container and unused. Missing before it can gate:

- an agreed severity threshold (the reverser fails only on **fixable CRITICAL**,
  which is actionable signal rather than noise from unfixed CVEs);
- a `.trivyignore.yaml`, with the gate reading it explicitly and treating a
  missing file as fatal — a security control must not be able to quietly stop
  honouring its own suppressions;
- DB caching in CI. The vulnerability DB unpacks to ~1.3 GB and goes stale in
  24h, which is why it is deliberately *not* baked into the container image.

Put the two-pass structure (report everything, then gate on a subset) in a
`task scan-image`, not in workflow steps.

### 3. Markdown and prose linting

`markdownlint-cli2` and `vale` are in the container and inert. This repository's
existing documents have never been checked against either, so switching them on
today starts red — that is a documentation cleanup, not a CI task, and it should
be its own change. `vale` additionally needs a style under `.vale.ini` before it
does anything at all.

### 4. Retire `auth-service`

It is not published and not to be deployed again; it stays linted and tested only
while its replacement is written. When section 7 of the plan is done and the
participant-ID-token paths have taken over:

- delete the directory and its Taskfile;
- remove the `unused` waiver in [`.golangci.yml`](../.golangci.yml) that exists
  solely for its three leftover functions;
- drop it from `dependabot.yml`, `task lint-dockerfiles` and the root Taskfile.

### 5. Restore the two staticcheck checks

Both waivers in [`.golangci.yml`](../.golangci.yml) name the condition for
lifting them:

- **ST1005** (capitalized error strings) — lift once Room Pass join errors carry
  a separate user-facing message field instead of putting participant prose in
  the error itself.
- **ST1013** (numeric HTTP statuses) — lift after one sweep converting all ~50
  call sites in `room-pass/internal/server` to `http.StatusX`. Enabling it before
  that gates on the arbitrary subset staticcheck happens to flag.

### 6. Frontend tests

`task frontend:test` currently runs `npm run build`, which does type-check via
`vue-tsc` — a real check, but not a test suite. When a runner lands (Vitest), put
it in that task; CI calls `task test` and needs no edit.

### 7. Release versioning

Images are identified by commit (`sha-<short>`), not semver, and there is no
changelog. That is fine while the demo is the only consumer. If `platform/` ever
needs to pin a *release* rather than a commit, the reverser's release-please
setup is the model — and that also brings a `pr-title.yml` conventional-commit
check as a prerequisite.

### 8. Smaller things

- **Retry the `curl` tool downloads** in `.devcontainer/Dockerfile` the way the
  `go install` layer now retries. A cold CI image build is ~7 minutes and any one
  of ~20 downloads can flake it; `--retry 3 --retry-delay 2 --retry-connrefused`
  would cover them cheaply.
- **`linux/arm64`** — add to `PLATFORMS` in the root Taskfile only when something
  actually runs on it. The demo cluster is Talos on Proxmox (amd64), and
  multi-arch doubles every image build.
- **Runtime smoke test of the published images.** Both are built and pushed
  without ever being started. Even `docker run --rm <image> --help` would catch a
  broken entrypoint before the cluster does.
- **Coverage reporting.** No coverage is collected or tracked for either module.
