# Plan: demo-production takes its menu and keys from the settings image

_Written 2026-11-04. Covers why, and what changes in `external/k8s` (`ConfigButler/k8s`). How Voter itself is built is
in [voter-settings-mode-plan.md](voter-settings-mode-plan.md). This plan replaces the promotion path of
[demo-production-plan.md](demo-production-plan.md), and overrides the 2026-11-03 rule that production
stays as it is and only a third app consumes the image._

## The goal

`demo-production.koudijs.dev` stops reading from Kubernetes. Its menu and its two keys come from the
settings image, and the keys are opened in Voter's memory. A person promotes a new image by merging a PR
on `ConfigButler/k8s`, and rolls back by reverting that PR.

Login stays, and so do names on orders. krm-foyer keeps doing both.

```text
Today
  /room save -> k8s-trail coffee-change -> PR merged by a person -> k8s-trail main
    -> Flux applies a CoffeeConfig -> Voter GETs it per request -> order screen
                                      phones also watch it live through /k8s and /stream

After
  /room save -> ConfigButler/coffee-settings -> image in zot
    -> Flux pushes the new reference to a branch of ConfigButler/k8s -> PR merged by a person
    -> new Voter pod mounts /settings, runs sops -d, holds the result -> order screen
                                      phones notice the new menu by polling /revision.json
```

| | Goes | Stays |
| --- | --- | --- |
| **Voter** | ServiceAccount token, per-request CoffeeConfig read | `requireIdentity`, orders, vouchers, the feed |
| **Browser** | `/k8s` and `/stream`: the live menu, the permissions table on `/me` | Sign-in, name badge, `/me` with `/auth/whoami` |
| **krm-foyer** | Shared watch on CoffeeConfigs, its `/k8s` and `/stream` routes on this host | Login, `/auth/check?identity=true` |
| **Git** | k8s-trail `configs/coffee/`, Flux's Kustomization and Receiver for it | Dex callback, Room Pass return URL, the metrics scrape |

A new menu still reaches an open phone without a touch: the order screen polls once a second and refetches when the
revision changes, which happens once a person has merged. See
[The browser: noticing a new pod](voter-settings-mode-plan.md#the-browser-noticing-a-new-pod).

## Promoting: the options

| | What a person does | What is new | Rollback | Verdict |
| --- | --- | --- | --- | --- |
| **A. Flux writes a branch, a small workflow opens the PR** | Merges a PR | 1 automation, 1 label, 1 selector, 1 workflow of about 25 lines. No credential: Flux's write key and `GITHUB_TOKEN` already exist | Revert the PR. It sticks | **Recommended** |
| **B. coffee-settings' publish workflow opens the PR** | Merges a PR | A token that can write to the private k8s repo, held by a public repo. No Flux change | Revert the PR. It sticks | Works, but that credential is the wrong trade when A needs none |
| **C. A "Promote" button** (`workflow_dispatch` on k8s, input: a tag) | Picks a tag, then merges a PR | 1 workflow. No Flux change, no marker | The same button with an older tag | Simplest, and the clearest rollback. Nothing tells you a new image is waiting. Easy to add to A later |
| **D. Kargo** (already installed, no Project yet) | Clicks Promote in Kargo's UI, then merges a PR | Project, Warehouse, Stage, a Git credential. Warehouse minimum 5 min | Promote the older Freight | The best picture on stage, and promotion is its job. Never run here yet, so a good second step rather than the first |
| **E. A promotion tag** (policy filters on `prod-*`) | Runs a retag workflow | 1 workflow, a filter. Flux commits to `main` | Awkward: highest tag wins, so a revert is overwritten. You retag the old digest with a newer number | No |
| **F. No gate**, as on coffee-settings | Nothing | 1 marker | Awkward: a revert is overwritten within a minute unless you suspend the automation | No. Every save would restart production |

The rest of this plan uses **A**. Only "Promotion" in Part 2 changes if you pick another.

### What a promotion looks like with A

1. A `/room` save publishes an image. coffee-settings follows it straight to `main` as today, so
   `coffee-settings.k8s.koudijs.dev` is where you look at it first.
2. Flux pushes the new reference to `promote/demo-production`, and the workflow opens a PR. While a PR is
   open, newer images move the same PR forward, so a busy `/room` gives one PR and not twenty.
3. The diff is two lines: `main-<time>-<sha>@sha256:…`, old and new. The PR body links to the
   coffee-settings compare view, which shows which files changed. For a key that is a new ciphertext, so the
   reviewer can see *that* a key changed and cannot see the key.
4. Merging restarts Voter with the new image, in seconds through the existing GitHub Receiver.

### What a rollback looks like

Press Revert on the merged PR and merge that. The previous digest returns, a new pod mounts the previous
image, and it opens the previous ciphertext with the same age key. **Nobody reads, retypes or holds the old
key at any point.** Further back: revert more, or set the line to any reference from `git log` on that file.

- Flux then offers the newer image again in a new PR. That is correct, and nothing moves until it is merged.
  Close it; it stays closed until a newer image arrives.
- The image holds the menu and the keys together, so a rollback moves both.
- It restores the old value, not its validity: a key revoked at the provider stays revoked.
- It needs the age key that sealed the old image. Do not delete that key from OpenBao when rotating.

## Part 1: Voter

**The plan is [voter-settings-mode-plan.md](voter-settings-mode-plan.md).** In short: `SHOP_ONLY`, `SETTINGS_DIR`,
`SETTINGS_KEYS` and `DONT_DO_THIS` are independent switches, all off by default, so demo and today's production run
unchanged.

One `feat:`, so 2.12.0.

**When to merge: not before Friday 6 November 11:55.** A `feat:` reaches the cluster by itself and restarts
Voter in both namespaces, which forgets orders and voucher counts. Talks are Thursday 14:55 and Friday 11:15.

## Part 2: `external/k8s`

All under `k8s.koudijs.dev/2-gitops/`, pushed to `main` after `git pull --rebase`.

### OpenBao: a second reader of the same key (by hand, you)

```text
sys/policies/acl/voter-demo-production
  path "secret/data/coffee-settings/age" { capabilities = ["read"] }

auth/kubernetes/role/voter-demo-production
  bound_service_account_names:      [voter]
  bound_service_account_namespaces: [demo-production]
  audience: openbao
  token_policies: [voter-demo-production]
  token_ttl: 5m
```

Record it in the comment in `openbao/cluster/openbao.yaml`, beside coffee-settings'.

### Cutover: one commit

**`voter-demo/production/app.yaml`**, the parts that change. It is copied from `coffee-settings/app.yaml`,
which already runs this mount live.

```yaml
kind: Deployment
spec:
  strategy:
    rollingUpdate: {maxUnavailable: 0, maxSurge: 1}   # was Recreate
  template:
    metadata:
      annotations:
        configbutler.ai/settings-reference: cr.k8s.koudijs.dev/configbutler/coffee-settings:main-…@sha256:… # {"$imagepolicy": "flux-system:coffee-settings"}
    spec:
      automountServiceAccountToken: false
      containers:
        - name: voter
          env:
            # SHOP_ONLY and COFFEE_CONFIG_NAME as they are
            - {name: SETTINGS_DIR, value: /settings/resources}
            - {name: SETTINGS_KEYS, value: openbao-age}
            - name: SETTINGS_REFERENCE
              valueFrom: {fieldRef: {fieldPath: "metadata.annotations['configbutler.ai/settings-reference']"}}
            - {name: OPENBAO_ADDR, value: https://openbao.k8s.koudijs.dev}
            - {name: OPENBAO_ROLE, value: voter-demo-production}
            - {name: OPENBAO_AGE_SECRET_PATH, value: secret/data/coffee-settings/age}
            - {name: OPENBAO_TOKEN_FILE, value: /var/run/secrets/openbao/token}
            - {name: DONT_DO_THIS, value: "true"}
            - name: KUBERNETES_NAMESPACE      # nothing is mounted to read it from
              valueFrom: {fieldRef: {fieldPath: metadata.namespace}}
          lifecycle:
            preStop: {sleep: {seconds: 5}}   # the edge still sends requests until it has caught up
          volumeMounts:
            - {name: settings, mountPath: /settings, readOnly: true}
            - {name: openbao-token, mountPath: /var/run/secrets/openbao, readOnly: true}
      volumes:
        - name: settings
          image:
            reference: cr.k8s.koudijs.dev/configbutler/coffee-settings:main-…@sha256:… # {"$imagepolicy": "flux-system:coffee-settings"}
            pullPolicy: IfNotPresent
        - name: openbao-token
          projected:
            sources:
              - serviceAccountToken: {audience: openbao, expirationSeconds: 600, path: token}
```

Start both references at the digest coffee-settings runs at that moment. The existing NetworkPolicy stays: it is
what makes the identity header trustworthy.

**`production/ingress.yaml`:** drop `/k8s/` and `/stream/` from the krm-foyer route. `/auth/` and `/_foyer/` stay,
and so does `foyer-identity` on `/public/`.

**`production/krm-foyer.yaml`:** remove `sharedWatches`.

**Promotion (option A):**

```yaml
# coffee-settings/image-automation.yaml: label the existing ImagePolicy coffee-settings
metadata:
  labels: {promotion: pull-request}
---
# voter-demo/image-automation.yaml: without this, the voter-demo automation, whose path covers
# production/, would write the settings reference straight to main and skip the PR
spec:
  policySelector:
    matchExpressions:
      - {key: promotion, operator: DoesNotExist}
---
# New, beside it
apiVersion: image.toolkit.fluxcd.io/v1
kind: ImageUpdateAutomation
metadata: {name: demo-production-settings, namespace: flux-system}
spec:
  interval: 5m
  sourceRef: {kind: GitRepository, name: voter-demo-write}
  policySelector:
    matchLabels: {promotion: pull-request}
  git:
    checkout: {ref: {branch: main}}
    commit:
      author: {name: flux, email: flux@k8s.koudijs.dev}
      messageTemplate: |
        chore(demo-production): promote {{range .Changed.Changes}}{{.NewValue}}{{break}}{{end}}
    push: {branch: promote/demo-production}
  update:
    path: ./k8s.koudijs.dev/2-gitops/voter-demo/production
    strategy: Setters
```

The coffee-settings automation has no selector and its own path, so it keeps following every image to `main`. A
Voter release still goes to `main` on its own: the PR gates settings, not releases.

New `.github/workflows/promote-demo-production.yml`, `on: push` to `promote/demo-production`, with
`pull-requests: write`: if no PR is open for the branch, `gh pr create`, with a body that links
`ConfigButler/coffee-settings/compare/<old sha>...<new sha>`. The repository already allows Actions to create PRs.

**By you, on GitHub:** turn on "Automatically delete head branches" on `ConfigButler/k8s` (it is off). Every PR
then starts from `main`. Without it, Flux reuses the old branch, whose lines then conflict after a squash merge
or a revert.

**Also in this commit:** correct the `voter-demo` automation's commit message, whose "1.x" is out of date.

**Rollback of the cutover is reverting this commit**, because the next commit has not removed anything yet.

The menu visibly changes at this moment: production shows "Koudijs Demo Coffee" with 4 products, the image has
"Simons coffee ordering" with 3.

### Clean-up: a second commit, once it has run for a while

| File | Change |
| --- | --- |
| `production/configs.yaml` | Delete. Flux prunes the CoffeeConfig. Check afterwards, because its applier's RBAC goes in the same commit and one may be left behind, harmless and unread |
| `production/rbac.yaml` | Delete; move ServiceAccount `voter` into `app.yaml` with `automountServiceAccountToken: false`. `coffee-reader` goes with it |
| `production/secrets.yaml` | Remove `k8s-trail-read` and `k8s-trail-receiver-token`. `voter-oidc-client` and `krm-foyer-session-keys` stay |
| `production/kustomization.yaml` | Update the list and the header |
| Comments and docs | `voter-demo/git-sink-coffee.yaml:7`, `voter-demo/git-sink.yaml:14`, `coffee-settings/kustomization.yaml:5`, `settings-image-loop.md:20`, `settings-image-publishing-plan.md:17,105,150,400`: "demo-production is untouched" stops being true |

**By you, on GitHub, after that commit:** delete k8s-trail's webhook to the `k8s-trail` Receiver, and the deploy key
"flux demo-production (read-only)".

**Stays:** GitTarget `coffee` keeps writing to k8s-trail `coffee-change` as a record, though nothing promotes it.

## Order of work

| # | Step | Where | Effect on the live site |
| --- | --- | --- | --- |
| 1 | [The Voter plan](voter-settings-mode-plan.md), on a branch and in a PR | sunib/voter | None |
| 2 | **After Friday 11:55:** merge, 2.12.0 reaches both namespaces | sunib/voter | Voter restarts in both, switch off |
| 3 | OpenBao policy and role | By hand, you | None |
| 4 | Cutover, and the branch setting | `external/k8s`, you | Production switches, menu changes |
| 5 | Check "Done when" | | None |
| 6 | Clean-up, then the GitHub clean-up | `external/k8s`, you | None |

## Done when

- [ ] The pod has no `kube-api-access` volume, and `kubectl auth can-i get coffeeconfigs -n demo-production --as=system:serviceaccount:demo-production:voter` says `no` (after the clean-up commit).
- [ ] `/dont-do-this` shows both real keys, opened, and the image's digest.
- [ ] Signed in, an order completes and `GET /public/orders` lists it under your name; no request goes to `/k8s/` or `/stream/`, on the order screen or on `/me`.
- [ ] A `/room` save opens a PR. Merging it brings a new pod with the new values. Note the time from save to PR.
- [ ] A second save while that PR is open moves the same PR forward.
- [ ] Reverting the PR brings the previous digest and the previous key back on `/dont-do-this`, with nobody having read it. The automation's next PR changes nothing until merged.

## Risks taken on purpose

| Risk | What happens | Why that is fine for now |
| --- | --- | --- |
| **krm-foyer still calls the API** | An API outage stops login and all of `/public/`, so ordering stops too. Voter's pod itself needs nothing from the API | You want the login and the names |
| **No trial host** | The first real run is on the public host | Rollback is one revert, and coffee-settings already proves the mount and the files. Do it away from a talk |
| **Voter keeps its OpenBao token mount for its whole life.** Coffee-settings is stronger: only its init container has it | A compromised Voter could read the age key, which opens the published ciphertext | One path, 5-minute token. The later OpenBao route shrinks this (below) |
| **Every merge restarts production** | Orders and voucher counts go with the pod (`coffee_vouchers.go`) | It now happens when a person merges, not on every save |
| **The PR is a convention** | GitHub refuses branch protection on this private repository on this plan, so a push to `main` still works | It records who decided what, and that is the point here |
| **A stuck rollout holds up `voter-demo`** | `wait: true`, `timeout: 10m`, and coffee-settings depends on it. A bad image could delay both for up to 10 minutes | The old pod keeps serving, and a person chose that image. Production's own Kustomization can follow if this ever bites |
| **zot is one pod on one node's disk** | A pod moved to a node without the image, while zot is down, cannot start | `ghcr.io/configbutler/coffee-settings` has the same digest. A re-run of the workflow restores it |
| **OpenBao's policy, role and key are hand-written** | After its disk is lost, orders still work and keys show as unavailable | Move to `selfInit.requests` when a restart is fine. Also back up `voter/coffee-settings-age`, still owed |
| **`/dont-do-this` is public** | Whatever was typed on `/room` and merged is readable | Made-up values, already public, escaped, and it comes out before a real key goes in |

Left out to keep this small: the `-next` host, production's own Flux Kustomization, closing egress, a rate limit,
live drills for a bad image or a refusing OpenBao, and a browser spec for this mode.

## Later: OpenBao does the decrypting

This is why Voter runs the `sops` binary. Transit is already enabled in OpenBao, and `sops` speaks to it natively.

1. `coffee/.sops.yaml` gains an `hc_vault_transit_uri` beside the age recipients, so a file opens either way.
2. Voter hands the child `VAULT_ADDR` and `VAULT_TOKEN` instead of `SOPS_AGE_KEY`. That is one more `SETTINGS_KEYS`
   value (`openbao-transit`) in `settings_keys.go`, and no format code changes.
3. The role's policy allows `transit/decrypt/<key>` instead of reading the KV path.

The age key then never leaves OpenBao, and every opening shows in its audit log. Keep the age key in OpenBao for
as long as an old image might be rolled back to.

**Not checked:** gitops-reverser encrypts by running `sops` too (`internal/git/sops_encryptor.go`), so it needs a
token of its own to seal to transit. Does it have a way to get one?

## What was checked for this plan

| Fact | How |
| --- | --- |
| `sops` 3.13.3 opens a Secret on `debian:bookworm-slim`, Voter's base, as uid 65532, with a read-only root, a wiped environment, only `SOPS_AGE_KEY` set, and a file named `voter-coffee-mail.sops.yaml`. `data:` comes back base64, non-ASCII intact | Spike, throwaway key, built with the `COPY --from` above |
| A wrong key exits 128, changed `metadata` exits 51 | The same spike |
| The binary is static and 52,121,762 bytes | Inspected in the pinned image |
| The real files open, and the mount pulls from zot | Live on coffee-settings: `STATUS.txt` says both opened |
| A marker under `production/` would go straight to `main` | `voter-demo` automation's `update.path` |
| With a separate push branch, Flux works from that branch's tip if it exists, and pushes nothing when there is no diff. An ImagePolicy change triggers a run at once | Source, image-automation-controller v1.3.0 and `fluxcd/pkg` `SwitchBranch`, defaults. **Flux's docs say the branch is overwritten instead.** Deleting the branch on merge makes the plan work under either reading |
| `policySelector` limits which policies an automation applies | Flux docs |
| `/k8s` in the shop's screens: `useLiveCoffeeConfig` and `useAuthorization` only. `invalidItems` depends on the stream | `grep` and read: `OrderScreen.vue`, `IdentityScreen.vue`, `stores/cart.ts` |
| Actions may create PRs on k8s; head branches are not deleted on merge; branch protection answers 403 | `gh api`, `gh repo view` |
| Old digests stay in zot: no retention rule, and `gc` sweeps only untagged manifests, while `main-<time>-<sha>` tags never move | `registry/zot.yaml`, plus how zot's `gc` works (not tested here) |

**Not checked:** the workflow and the branch automation have never run here, so the first PR proves them. The Flux
version on the cluster against v1.3.0. A push by the deploy key triggering `on: push` is expected GitHub behaviour,
not tested here. Whether Thursday's or Friday's talk uses production: neither `docs/talk-checklist.md` nor
`ordev-turing-switch.md` mentions it.
