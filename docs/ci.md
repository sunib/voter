# CI and container images

This repository's pipeline is adapted from [gitops-reverser][reverser], with one
rule carried over deliberately:

> The workflow file knows how to move things around. It never knows how to check
> them.

Everything that decides whether a commit is good runs as a `task` inside the
container built from [`.devcontainer/Dockerfile`](../.devcontainer/Dockerfile) —
the same container you develop in. [`.github/workflows/ci.yml`](../.github/workflows/ci.yml)
checks out the code, provides a Docker daemon, and moves images between jobs.

Three things follow from that:

- A red job reproduces locally with the command printed in its own log.
- Changing *what* CI checks means editing [`Taskfile.yaml`](../Taskfile.yaml),
  not YAML in `.github/`.
- Moving to another CI system means writing a file that calls the same tasks.

[reverser]: https://github.com/configbutler/gitops-reverser

## The jobs

| Job | What it does | Reproduce with |
|---|---|---|
| `ci-container` | Builds `.devcontainer/Dockerfile --target ci` and asserts every tool is present | `task ci-image` |
| `devcontainer` | Builds `--target dev` and asserts the developer tools are present | `docker build --target dev -f .devcontainer/Dockerfile .` |
| `lint` | golangci-lint (both Go modules), hadolint, actionlint, ESLint | `task lint` |
| `test` | Go tests, frontend type-check and build, CRD staleness, envtest | `task test && task room-pass:verify-generate && task test-integration` |
| `images` | Builds `room-pass` and `voter`; publishes on a push to `main` | `task build` |

`ci-container` runs first because every other job runs inside its output. On a
pull request that image is handed along as an artifact; on a trusted run it is
pushed to `ghcr.io/sunib/voter-ci` and pulled.

`images` waits for `lint` and `test`, so nothing untested is ever published.

## Trust model

One rule: **untrusted code is built and tested, but never meets a write token.**

Pull requests build the CI container and pass it between jobs as an artifact,
because a fork's `GITHUB_TOKEN` is read-only and cannot pull from or push to
`ghcr.io`. They build both application images and push neither. Only a push to
`refs/heads/main` publishes — deliberately not "any event that is not a pull
request", which would let a `workflow_dispatch` on a topic branch publish.

GitHub enforces the fork half of this regardless of the permissions the workflow
declares; the declarations exist so the intent is reviewable.

## The two images

| Image | Built from | Contents |
|---|---|---|
| `ghcr.io/sunib/room-pass` | `room-pass/` | The Room Pass gate: Room/Participant controller, join page, session and Dex header adapter |
| `ghcr.io/sunib/voter` | `frontend/` | The Vue quiz and coffee UI, served by unprivileged NGINX |

The legacy backend paths are deliberately not published separately: they are on their way out. Section 7 of
the platform implementation plan replaces its impersonation paths with participant
ID tokens forwarded from Room Pass, and it is not to be deployed again. It stays
in the tree, linted and tested, only so the coffee handlers can be read while
their replacement is written -- there is no image to pin, and no matrix entry to
add later.

Both are built by `task image-room-pass` and `task image-voter` — the same
commands CI runs, with the same build args. There is no workflow-only definition
of how a shipped artifact is built. To publish somewhere else:

```bash
task image-voter REGISTRY=zot.z65.nl IMAGE_OWNER=voter TAG=coffee PUSH=true
```

### Tags

A push to `main` publishes two tags per image:

- `sha-<short sha>` — immutable, one per commit.
- `main` — moves with the branch.

The job summary prints the pushed digest as `ghcr.io/sunib/<image>@sha256:…`.
**That is what belongs in `platform/`**: section 8 of the implementation plan
asks for release images referenced by digest, not by tag. `main` is for a quick
manual pull, not for Flux.

### Architecture

`linux/amd64` only. The demo cluster is Talos on Proxmox, and multi-arch doubles
every build for a platform nothing currently runs on. Add `linux/arm64` to
`PLATFORMS` in the root `Taskfile.yaml` when something needs it.

## Running the checks locally

Inside the devcontainer, just run the tasks:

```bash
task lint
task test
task build
```

To run them in the *CI* container instead — worth doing when CI fails and your
devcontainer does not — mount the workspace the way the workflow does:

```bash
task ci-image
docker run --rm -v "$HOST_PROJECT_PATH:/workspaces/voter" -w /workspaces/voter \
  voter-ci:local bash -lc 'git config --global --add safe.directory "$PWD"; task lint'
```

Two notes on that command. `HOST_PROJECT_PATH` rather than `$PWD`: the
devcontainer talks to the host's Docker daemon, so the bind mount needs the path
as the *host* sees it. And the container runs as root, so it can leave
root-owned files in your working tree — `.git/index` in particular. Prefer the
plain `task` form for everyday work.

## Linter configuration

The waivers are deliberate and each one records why, so they can be revisited:

- [`.golangci.yml`](../.golangci.yml) — `ST1005` is off because Room Pass renders
  its errors to participants as prose; `ST1013` is off because the handlers use
  numeric HTTP statuses consistently and staticcheck flags only a subset. The
  `unused` waiver on three `voter` functions is tied to the migration that
  will delete them.
- [`.hadolint.yaml`](../.hadolint.yaml) — unpinned distro packages and `cd` in
  `mktemp -d` installers, both intentional in the tooling image.

Markdown and prose linting are not wired up yet: `markdownlint-cli2` and `vale`
are in the container, but this repository's existing documents have never been
checked against them, so enabling the tasks would start red. That is a separate
cleanup.

## Not yet in CI

The backlog lives in [`.github/README.md`](../.github/README.md), next to the
workflow it describes, so there is one list rather than two that drift.

The short version: the Room Pass end-to-end suite is the significant gap — until
it runs, a green pipeline does not mean a participant can log in.
