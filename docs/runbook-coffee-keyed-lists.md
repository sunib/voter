# Runbook: give a CoffeeConfig list an identity

**Done 2026-10-07 for `spec.vouchers` (by `code`).** `spec.products` (by `sku`)
went out in 2.1.1 the day before.

## Why

The menu editor merges a list one element at a time only when the list is keyed.
An unkeyed list is one value: two people editing *different* vouchers, one
saves, and the other's whole voucher block turns red as a single conflict.

Keying it takes two things, and either one alone does nothing visible:

- the CRD declares `x-kubernetes-list-type: map` with the key field, and
- the frontend's `coffeeConfigKeyedLists` (`frontend/src/api/liveCoffeeConfig.ts`)
  says the same, because krm-stream only merges by key when it is handed the
  schema.

## Preconditions

- The key field is `required` in the CRD (`code` and `sku` both are). The API
  server refuses a map list keyed on an optional field.
- Every live object has unique, non-empty keys, or its next write is refused:

  ```bash
  kubectl get coffeeconfigs -A -o json | jq -r '.items[] |
    "\(.metadata.namespace)/\(.metadata.name) \([.spec.vouchers[].code])"'
  ```

  On 2026-10-07 both objects held only `FRONTMANIA`.

## Steps

1. **CRD, in both copies.** They are identical and must stay so:
   - `voter/config/crd/coffeeconfigs.yaml` (e2e fixture, the source)
   - `external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/crds/coffeeconfigs.yaml`
     (what the cluster runs; `cd external/k8s && git pull --rebase` first)

   Check it without changing anything:
   `kubectl apply --dry-run=server -f voter/config/crd/coffeeconfigs.yaml`.

2. **Frontend.** Add the list to `coffeeConfigKeyedLists`, then add a test in
   `liveCoffeeConfig.test.ts` where two people edit different elements. The test
   `keys exactly the lists the CRD keys` fails until the CRD copy and the
   frontend copy agree.

3. **Ship the platform repo first.** Commit and push `external/k8s` to `main`,
   then `flux reconcile kustomization voter-demo -n flux-system`. Verify:

   ```bash
   kubectl get crd coffeeconfigs.examples.configbutler.ai -o json | jq \
     '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.vouchers["x-kubernetes-list-map-keys"]'
   ```

4. **Then the app.** Push a `fix:` commit to `main`. CI goes green, release.yml
   merges the release PR itself (its `action_required` CI is noise), and Flux's
   image automation writes the new tag into `app.yaml`. To hurry it:
   `flux reconcile image repository voter -n flux-system`, pull `external/k8s`,
   `flux reconcile kustomization voter-demo -n flux-system`.

5. **Verify what the browser gets**, then reload every open editor tab:

   ```bash
   a=$(curl -s https://demo.koudijs.dev/ | grep -oE '/assets/[^"]+\.js' | head -1)
   curl -s https://demo.koudijs.dev$a | grep -o '"x-kubernetes-list-map-keys":\["code"\]'
   ```

## Rollback

Revert the frontend commit (another `fix:`) to go back to the atomic merge. The
CRD change can stay: on its own it changes nothing the editor does, because the
editor sends a JSON merge patch, which replaces the list whole either way.
