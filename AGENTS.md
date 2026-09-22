# Working in this repository

## Always pull `external/k8s` before you touch it

`external/k8s` is a **separate checkout** of `github.com/ConfigButler/k8s`, the
GitOps repository that deploys this app. It is not a submodule and it is not
tracked by this repository, so nothing about `git status` here tells you
anything about its state.

Before reading or editing anything under `external/k8s`:

```bash
cd external/k8s && git pull --rebase
```

**This is not hygiene, it is correctness.** That repository now has a writer
that is not a person. Flux's image automation
(`k8s.koudijs.dev/2-gitops/voter-demo/image-automation.yaml`) commits to `main`
on its own whenever a new 1.x release of this app is published — it rewrites the
`image:` lines in `app.yaml` and `room-pass.yaml` with the elected version and
its digest.

So a local checkout goes stale without anybody doing anything, and the two ways
that bites are both quiet:

- You edit `app.yaml` against a stale copy and your push is rejected as
  non-fast-forward. Annoying, but at least it tells you.
- You read `app.yaml` to answer "what is deployed?" and confidently report a
  version that was replaced an hour ago. Nothing tells you.

Pull first, every time. If a rebase conflicts on an `image:` line, Flux's
version wins — it is the one that matches what is running.

## Do not `kubectl apply` into this cluster

Everything reaches `k8s.koudijs.dev` through Git. Changing the cluster means
committing to `external/k8s` and letting Flux reconcile; `flux reconcile` to
hurry it along is fine, because it only asks Flux to do its job sooner.

An object created by hand survives until something rebuilds the namespace and
then vanishes with no record it ever existed. Two CRDs and a GitTarget already
had to be adopted back into Git after exactly that.

The one deliberate exception is the `voter-audience-coffee-admin` RoleBinding,
which the app creates and deletes at runtime from the operator page. It is
documented as absent from Git in `participant-rbac.yaml`, and Flux would fight
the demo if it were added.

## Releases are cut from commit messages

The conventional-commit prefix is load-bearing: `fix:` is a patch, `feat:` a
minor, `feat!:` a major. A patch or minor reaches the cluster on its own; a
major never does. See [docs/ci.md](docs/ci.md#releases).
