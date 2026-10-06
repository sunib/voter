# Voter and Room Pass

A phone-friendly coffee and quiz demo for showing Kubernetes as an application
API, with participant identity carried into audit events and ConfigButler's Git
workflow.

Login works, and so does the coffee journey: browse the menu, order, hit the
depleted voucher, edit the CoffeeConfig, and watch every order — placed or
refused — land on the live feed at `/admin/orders`. Voting and conditional live
editing also work. Since 2.0.0 (2026-10-06) Voter runs behind
[krm-foyer](https://github.com/ConfigButler/krm-foyer); what is still open is in
[PLAN.md](PLAN.md) and the migration's [Left over](docs/krm-foyer-migration.md#left-over).

## How access works

Room Pass admits attendees using a room code. Dex also supports GitHub and
LinkedIn login. krm-foyer is the OIDC client: it owns `/auth`, keeps the session
in a sealed cookie, and forwards the browser's `/k8s` requests and `/stream`
watches to Kubernetes with the signed-in person's own token. Votes and edits are
the browser's own writes; Kubernetes authentication, RBAC and admission
(`voter/config/admission/`) decide what that person can do. Voter serves the Vue
frontend, `/config.json` and a few `/public` endpoints behind krm-foyer's identity
check. It holds no session and nobody's token.

Being able to log in does not imply being allowed to edit. Room attendees receive
explicit demo group grants. Other identities need their own matching grants.
Opening GitHub login to everyone is a proposed change, not the current configuration.

## Login url for room admin

https://demo.koudijs.dev/login?connector=github&next=%2Froom

## Read next

- [Architecture](ARCHITECTURE.md): how it works now — components, the identity
  model, the trust boundaries, and a login walked through end to end.
- [What is left](PLAN.md): the single remaining-work list.
- [Demo runbook](docs/demo-runbook.md): the talk as a stage copy — what to
  press, in order, the commands behind each step, and what to do when it breaks.
- [krm-foyer adoption](docs/k8s-front-adoption.md) and
  [krm-foyer feedback](docs/krm-foyer-feedback.md): history, superseded by the
  migration below. Kept for the reasoning, not as current state.
- [Kubernetes as your backend](docs/kubernetes-as-a-bff.md): one vote through every stage of
  the API server (OIDC, RBAC, CRD validation, an admission policy that reads the round,
  the operator, GitOps), with YAML. Start here for the talk.
- [krm-foyer migration](docs/krm-foyer-migration.md): done, live since 2026-10-06 —
  how Voter's own auth, session and stream code went to krm-foyer, and what is left over.
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
task e2e-up            # local k3d fixture: Traefik, Dex, Room Pass, krm-foyer, Voter
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
