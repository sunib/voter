# Room Pass 2.0.0 field report

2026-10-01 · Simon Koudijs

Voter and the k8s.koudijs.dev demo cluster moved to Room Pass 2.0.0 on
2026-10-01, one commit in each repository. The CHANGELOG's upgrade note was
accurate and enough to do it. Three things made consuming the release harder
than it needed to be; none blocked us.

## What worked

- **The upgrade note.** The BREAKING CHANGES entry in the 2.0.0 CHANGELOG named
  every step we needed: new CRDs, a recreated Room, RBAC rules on the new group.
  We followed it as written.
- **The schemas.** The v2.0.0 CRDs differ from the 1.x ones only in the group,
  so nothing else in the platform manifests had to change.
- **Identity stayed stable.** Cookie keys, the `room-pass` connector ID and
  subject format are unchanged, so Dex, the apiserver's claim rules and Voter's
  login code needed no edits.
- **The live check.** After Flux applied it, a fresh login at demo.koudijs.dev
  went Voter → Dex → the 2.0.0 join page. Voter's ten browser specs pass against
  the released image in Voter's own fixture.
- **Image automation.** Flux's ImagePolicy resolved `2.0.0` with its digest as
  soon as the range moved to `>=2.0.0 <3.0.0`.

## Friction, with suggested fixes

| What we hit | Effect on a consumer | Suggested fix |
| --- | --- | --- |
| `deploy/base` hardcodes the fixture's origins: `JOIN_ORIGIN`, `ISSUER_ORIGIN` and `ALLOWED_RETURN_URLS` point at `*.room-pass.test`. | Env is a list, so a consumer's overlay has to replace the WHOLE `env` array by JSON patch. If 2.1 adds a variable to the base, that overlay drops it silently. | Keep environment-specific values out of the base: make those three required with no default so a missing one fails at startup, and set the fixture's values in `test/e2e`. |
| `config/crd/` has no `kustomization.yaml`. | It cannot be used as a remote kustomize base (`github.com/sunib/room-pass//config/crd?ref=v2.0.0`). Voter lists two `raw.githubusercontent.com` URLs instead, which breaks if a CRD is added or renamed. | Add a two-line `kustomization.yaml` there, or publish a single `crds.yaml` (or full `install.yaml`) as a release asset. |
| The upgrade note does not say what happens if the old CRDs stay installed. | We kept the 1.x Room and its 70 Participants as a record. Two kinds named `Room` now coexist, so `kubectl get room` picks one silently. Under a pruning GitOps tool, deleting the old CRDs deletes every old Participant. | Add two sentences to the upgrade note: qualify kubectl commands (`rooms.room-pass.koudijs.dev`) while both exist, and removing the old CRDs is the step that discards the old records. |

One item is for the package owner, not the team: `ghcr.io/sunib/room-pass`
still grants write access to sunib/voter, which no longer publishes there and
should lose that access.
