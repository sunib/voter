# Voter

A phone-friendly coffee and quiz demo that uses Kubernetes as the application
API. Participants join with a room code, order coffee, vote, and edit the menu
once the operator lets them; every write is their own, so audit events and Git
commits carry their name. Live on `demo.koudijs.dev`; what is open is in
[PLAN.md](PLAN.md).

## How access works

[Room Pass](https://github.com/sunib/room-pass) admits attendees with a room
code; Dex also offers GitHub and LinkedIn. [krm-foyer](https://github.com/ConfigButler/krm-foyer)
is the OIDC client: it owns `/auth`, the session cookie, and forwards `/k8s`
requests and `/stream` watches with the person's own token. Kubernetes
authentication, RBAC and admission (`voter/config/admission/`) decide what they
may do. Voter serves the Vue app, `/config.json` and a few `/public` endpoints,
and holds no session and nobody's token.

What a participant may do is RBAC in the `voter` namespace alone. The operator
widens it live from `/room` with RoleBindings, and every page follows those
bindings and asks Kubernetes again when one changes
([runbook](docs/runbook-watchable-permissions.md)).

Operator login: https://demo.koudijs.dev/login?connector=github&next=%2Froom

## Read next

- [Architecture](ARCHITECTURE.md): components, identity, trust boundaries, and
  the current release.
- [Kubernetes as your backend](docs/kubernetes-as-a-bff.md): one vote through
  every stage of the API server. Start here for the talk.
- [Demo runbook](docs/demo-runbook.md): the talk as a stage copy.
- [Authorization](docs/authorization.md) and [talk checklist](docs/talk-checklist.md):
  who can do what, where that is enforced, and what is proven.
- [Databases](docs/databases.md): the platform-team page.
- [Frontend](FRONTEND.md) and [CI](docs/ci.md).

Everything else in [docs/](docs/) is a plan or a record of how something came to
be, dated in its first lines; ARCHITECTURE.md is what is true now.

## Build and test

```bash
task setup             # install dependencies
task lint              # Go, Dockerfiles, workflows, frontend
task test              # Go tests plus frontend type-check/build
task e2e-up            # local k3d fixture: Traefik, Dex, Room Pass, krm-foyer, Voter
task test-browser      # Chromium against that fixture (CI runs both)
task e2e-down
```

CI publishes `ghcr.io/sunib/voter` (the Go backend and the compiled frontend).
Deployment is GitOps: the Flux Kustomization `voter-demo` in
`external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/`. Never `kubectl apply` into
`voter` ([AGENTS.md](AGENTS.md)).
