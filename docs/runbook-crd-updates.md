# Runbook: change a CRD or an admission policy

For Voter's own types (CoffeeConfig, QuizSession, QuizSubmission, Seat,
SeatingPlan, Database) and the policies in `voter/config/admission/`. Room Pass
CRDs are Room Pass's: they come from its release tag and are copied by hand
when its image is bumped.

## Where they live

Every CRD and policy exists twice, and the two copies must stay identical:

| Source (e2e fixture, tests) | What the cluster runs |
| --- | --- |
| `voter/config/crd/<kind>.yaml` | `external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/crds/<kind>.yaml` |
| `voter/config/admission/<policy>.yaml` | `external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/admission/<policy>.yaml` |

A new file also needs a line in `voter-demo/kustomization.yaml`. CRDs are
cluster-scoped, so the one copy serves both `voter` and `demo-production`.

## First: which kind of change is it?

**Compatible**: new optional fields, looser rules, a new printer column,
`selectableFields`, list keys on lists that already hold unique keys. The app
running now keeps working against the new schema. Ship the platform repo
first, then the app. [runbook-coffee-keyed-lists.md](runbook-coffee-keyed-lists.md)
is a worked example.

**Breaking**: a field renamed or made required, a tighter rule than live
objects meet, a policy that reads a field the running app does not send. Both
orders break something: the old app against the new schema, or the new app
against the old one. Keep the gap short and plan what happens to live objects.
The Seat `displayName` → `claimedBy` change
([seat-claimed-by.md](seat-claimed-by.md)) is a worked example: every claim
fails while app and CRD disagree, and old Seats cannot be updated afterwards.

## Steps

1. **Pull, then edit both copies.**

   ```bash
   (cd external/k8s && git pull --rebase)
   diff <(sed 's/#.*//' voter/config/crd/seats.yaml) \
        <(sed 's/#.*//' external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/crds/seats.yaml)
   ```

   Comments may differ; the YAML may not.

2. **Prove it locally.** The fixture has its own kubeconfig. **Plain `kubectl`
   in this devcontainer points at production.**

   ```bash
   K="kubectl --kubeconfig .local/kubeconfig"   # context k3d-voter-e2e
   $K apply --server-side --dry-run=server -f voter/config/crd/seats.yaml
   task e2e-up && task test-browser             # e2e-up re-applies voter/config
   ```

   The fixture runs k3s 1.31 and the cluster 1.37. A field that is beta in 1.31
   (`selectableFields` was) works in both; an alpha one would not.

3. **Check the live objects against the new schema** (read-only):

   ```bash
   kubectl get <plural> -A -o json | jq '.items[] | {ns: .metadata.namespace, name: .metadata.name, spec}'
   ```

   For a breaking change, decide now whether they are migrated or deleted.
   Seats are deleted (`kubectl -n voter delete seats --all`): a seat costs
   nobody anything (deliberate-simplifications.md, entry 9).

4. **Release the app.** Commit in this repo with `fix:` or `feat:`. `chore:`
   cuts no release, so the new image never exists, and `feat!:` is a major,
   which image automation never deploys. CI releases; Flux's image automation
   commits the new tag to `external/k8s` `app.yaml` on its own.

5. **Ship the platform copy.**
   - **Compatible:** push `external/k8s` before step 4.
   - **Breaking:** wait for image automation's commit, then pull it in and push
     right behind it, so the image and the schema land in one reconcile:

   ```bash
   cd external/k8s && git pull --rebase && git push
   flux reconcile kustomization voter-demo -n flux-system --with-source
   ```

   Then clean up the live objects as decided in step 3.

6. **Verify the object, not the Kustomization's Ready.** `voter-demo` applies
   every revision, then health-checks. While a health check fails it stays
   not-Ready and its `lastAppliedRevision` does not move, but the new YAML is
   applied all the same. So check what landed:

   ```bash
   kubectl get crd seats.examples.configbutler.ai -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.spec.required}'
   kubectl get validatingadmissionpolicy voter-seat -o yaml | grep claimedBy
   kubectl -n voter get deploy voter -o jsonpath='{.spec.template.spec.containers[0].image}'
   ```

## Empty keyed lists

A list declared `x-kubernetes-list-type: map` must not be `required` unless
`[]` is invalid too (`minItems: 1`).

`demo-production`'s CoffeeConfig is applied by Flux, with server-side apply,
from `configs/` on k8s-trail, which is what the room last saved. When the field
manager that owns a keyed list applies it as `[]`, server-side apply removes the
field instead of keeping it empty, and a required field is then refused:

```text
CoffeeConfig/demo-production/demo-coffee dry-run failed (Invalid):
CoffeeConfig.examples.configbutler.ai "demo-coffee" is invalid: spec.vouchers: Required value
```

The room deleted its last voucher on 2026-10-07 at 09:41 UTC. From then on
`configs` in `demo-production` never became Ready, and production's menu stayed
as Flux last wrote it that morning. `voter-demo`, whose health check waits on
`configs`, timed out every 10 minutes; nobody noticed until 2026-10-29. A plain create or replace with `[]` is
accepted; only an apply by the owner of the old items fails. That is why it
passed every test.

The fix: `vouchers` is optional, since the app reads a missing list as empty,
and `products` keeps `required` but gains `minItems: 1`, so the editor is
refused an empty menu where it is typed and `[]` never reaches production.
