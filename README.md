# Voter and Room Pass

A phone-friendly coffee and quiz demo for showing Kubernetes as an application
API, with participant identity carried into audit events and ConfigButler's Git
workflow. The presentation narrative is in [talk-outline.md](talk-outline.md).

Login works. The coffee and quiz journeys still need application API restoration.
See [state-of-the-repo.md](state-of-the-repo.md) for the current gaps.

## How access works

Room Pass admits attendees using a room code. Dex also supports GitHub and
LinkedIn login. Voter is the OIDC client and serves the Vue frontend and Go
backend from one image. It sends the signed-in person's Dex ID token to Kubernetes;
Kubernetes authentication, RBAC and admission decide what that person can do.

Being able to log in does not imply being allowed to edit. Room attendees receive
explicit demo group grants. Other identities need their own matching grants.
Opening GitHub login to everyone is a proposed change, not the current configuration.

## Read next

- [Login explained](room-pass/login-explained.md): follow a login from browser to API.
- [Architecture](room-pass/advised_architecture.md): components and trust boundaries.
- [Authorization and tests](docs/authorization.md): who can do what, and what is proven.
- [Implementation plan](room-pass/implementation_plan.md): completed work and next steps.
- [Room Pass](room-pass/README.md): enrollment component and local fixture.
- [Frontend](FRONTEND.md): frontend design notes.
- [Documentation index](docs/README.md): current references and historical notes.

## Build and test

```bash
task setup             # install dependencies
task lint              # Go, Dockerfiles, workflows, frontend
task test              # Go tests plus frontend type-check/build
task test-integration  # Room Pass API tests with envtest
task test-network      # real Dex pod isolation in a disposable cluster (CI)
task test-e2e          # local k3d + Dex + Traefik fixture; not in CI
```

CI runs the same tasks in the repository's container. See [CI](docs/ci.md).
It publishes `ghcr.io/sunib/room-pass` and `ghcr.io/sunib/voter`; Voter contains
both `voter/` and the compiled `frontend/`.

Deployment currently lives in the external platform checkout under
`external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/`. The legacy root `k8s/` deployment
and `k8s-examples/` overlay have been removed. Room Pass retains its component
manifests and disposable local fixture under `room-pass/`.
