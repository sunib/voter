# Voter and Room Pass

A phone-friendly coffee and quiz demo for showing Kubernetes as an application
API, with participant identity carried into audit events and ConfigButler's Git
workflow. The presentation narrative is in [talk-outline.md](talk-outline.md).

Login works. The coffee and quiz journeys still need application API restoration.
See [PLAN.md](PLAN.md) for the current gaps and the work left.

## How access works

Room Pass admits attendees using a room code. Dex also supports GitHub and
LinkedIn login. Voter is the OIDC client and serves the Vue frontend and Go
backend from one image. It sends the signed-in person's Dex ID token to Kubernetes;
Kubernetes authentication, RBAC and admission decide what that person can do.

Being able to log in does not imply being allowed to edit. Room attendees receive
explicit demo group grants. Other identities need their own matching grants.
Opening GitHub login to everyone is a proposed change, not the current configuration.

## Read next

- [Architecture](ARCHITECTURE.md): how it works now — components, the identity
  model, the trust boundaries, and a login walked through end to end.
- [What is left](PLAN.md): the single remaining-work list.
- [Authorization](docs/authorization.md): who can do what, and what is proven.
- [Room Pass](room-pass/README.md): the enrollment component and local fixture.
- [Frontend](FRONTEND.md): UI design notes.
- [Talk outline](talk-outline.md): the presentation narrative.

Earlier design notes — the ForwardAuth/impersonation model, the `auth-service`
split, the alternative gateway proposals — have been removed rather than
archived. They are in Git history; nothing in the working tree describes a
system that no longer exists.

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

The application deployment is GitOps, owned by the Flux Kustomization `voter-demo`
in the platform checkout at `external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/`.
Do not `kubectl apply` into the `voter` namespace. Room Pass keeps its own
component manifests and disposable local fixture under `room-pass/`.
