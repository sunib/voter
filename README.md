# Voter and Room Pass

A phone-friendly coffee and quiz demo for showing Kubernetes as an application
API, with participant identity carried into audit events and ConfigButler's Git
workflow.

Login works, and so does the coffee journey: browse the menu, order, hit the
depleted voucher, edit the CoffeeConfig, and watch every order — placed or
refused — land on the live feed at `/admin/orders`. Voting and conditional live
editing also work. Shared-stream capacity and observed Git commits remain
release gates; see [PLAN.md](PLAN.md) for implementation and deployment status.

## How access works

Room Pass admits attendees using a room code. Dex also supports GitHub and
LinkedIn login. Voter is the OIDC client and serves the Vue frontend and Go
backend from one image. It sends the signed-in person's Dex ID token to Kubernetes;
Kubernetes authentication, RBAC and admission decide what that person can do.

Being able to log in does not imply being allowed to edit. Room attendees receive
explicit demo group grants. Other identities need their own matching grants.
Opening GitHub login to everyone is a proposed change, not the current configuration.

## Login url for room admin

https://demo.koudijs.dev/auth/login?connector=github&return=%2Froom

## Read next

- [Architecture](ARCHITECTURE.md): how it works now — components, the identity
  model, the trust boundaries, and a login walked through end to end.
- [What is left](PLAN.md): the single remaining-work list.
- [Demo runbook](docs/demo-runbook.md): the talk as a stage copy — what to
  press, in order, the commands behind each step, and what to do when it breaks.
- [krm-foyer adoption](docs/k8s-front-adoption.md): Voter-specific recommendation,
  pilot prerequisites and the boundary between prototype work and deployment.
- [Databases](docs/databases.md): the platform-team page — what it is built on,
  what has to exist in the cluster before it works, and where the intent goes.
- [Databases cutover](docs/databases-cutover.md): the four steps that take the
  live deployment from two tabs to three, in order, with the checks.
- [demo-c plan](docs/demo-c-plan.md): the last-minute gitops-reverser change that
  turns a database request into a commit — two objects and one variable.
- [Authorization](docs/authorization.md): who can do what, and what is proven.
- [Talk checklist](docs/talk-checklist.md): where authorization actually lives —
  application, RBAC and admission, and the honest limits of each.
- [Room Pass](https://github.com/sunib/room-pass): the enrollment component, its
  own project since 2.0.0. Voter consumes its releases.
- [Frontend](FRONTEND.md): UI design notes.

Earlier design notes — the ForwardAuth/impersonation model, the `auth-service`
split, the alternative gateway proposals — have been removed rather than
archived. They are in Git history; nothing in the working tree describes a
system that no longer exists.

## Build and test

```bash
task setup             # install dependencies
task lint              # Go, Dockerfiles, workflows, frontend
task test              # Go tests plus frontend type-check/build
task e2e-up            # local k3d fixture: Traefik, Dex, released Room Pass, Voter
task test-browser      # Chromium against that fixture (CI runs both)
task e2e-down
```

CI runs the same tasks in the repository's container. See [CI](docs/ci.md).
It publishes `ghcr.io/sunib/voter`, which contains both `voter/` and the compiled
`frontend/`. `ghcr.io/sunib/room-pass` is published by
[sunib/room-pass](https://github.com/sunib/room-pass), not from here.

The application deployment is GitOps, owned by the Flux Kustomization `voter-demo`
in the platform checkout at `external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/`.
Do not `kubectl apply` into the `voter` namespace. The Room Pass version the e2e
fixture runs is pinned in [`test/e2e/room-pass/`](test/e2e/room-pass/kustomization.yaml).
