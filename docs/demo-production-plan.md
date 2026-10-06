# Coffee to production: the ending of the 2026-10-07 demo

Plan written 2026-10-06 evening. **Live since 21:50 the same night**: Voter
2.1.0 (shop-only, 3034fdd), `configs/` seeded on `k8s-trail` main (09f8d11),
`external/k8s` b8be47e, and the `k8s-trail` webhook to the production
Receiver. Still owed: the rehearsal (steps 5-7 under "Order of work tonight").

## The beat

1. The room edits the coffee menu on `demo.koudijs.dev/admin`, as in demo 2
   today. Every save is a commit in the editor's name, but now on branch
   **`coffee-change`** of `ConfigButler/k8s-trail`, not on `main`.
2. The presenter opens (or has open) a PR `coffee-change` → `main`. The diff is
   the room's price changes; each commit names its author.
3. **Merge**, as a merge commit (not squash: squash rewrites every author to
   the presenter).
4. `demo-production.koudijs.dev` changes within seconds. It is the same coffee
   order page, signed in through the same Room Pass/Dex login, but it shows only
   the shop, and Kubernetes refuses any edit there.

The loop on one slide: **the cluster wrote Git, a person approved it, Git wrote
production.**

## Shape

```text
demo.koudijs.dev (ns voter)                  demo-production.koudijs.dev (ns demo-production)
  CoffeeConfig demo-coffee  ──reverser──▶  k8s-trail@coffee-change:configs/coffeeconfig.yaml
  (the room edits it)                              │  PR + merge
                                                   ▼
                                           k8s-trail@main:configs/coffeeconfig.yaml
                                                   │  Flux GitRepository + Kustomization
                                                   ▼
                                           CoffeeConfig demo-coffee (ns demo-production)
                                                   │  read-only: RBAC grants get/list/watch only
                                                   ▼
                                           Voter (SHOP_ONLY) behind its own krm-foyer
```

Decisions taken:

| Question | Answer |
|---|---|
| Git destination | `configs/coffeeconfig.yaml` at the root of `k8s-trail` |
| Branch | `coffee-change`. gitops-reverser creates it, tracking `main` until its first push |
| Production namespace | `demo-production` |
| Host | `demo-production.koudijs.dev` (DNS added by Simon) |
| Login | krm-foyer on Dex client `voter`, same connectors as demo. The room can open production on their phones |
| "No edit" | Enforced by RBAC (no `patch`/`update` in `demo-production`), plus the UI hides the editor links |

## Part 1: Voter, a shop-only mode (`feat:` → 2.1.0, with group voting)

Production runs the same image with one switch. A separate app would mean a
second build and a second release line the night before a talk.

- `voter/config.go`: `ShopOnly bool` from `SHOP_ONLY`, published in
  `/config.json` as `shopOnly`.
- `voter/main.go`: with `ShopOnly`, do not start `newQuizReconciler`. Production
  has no rounds, and its ServiceAccount has no rights on them.
- `frontend/src/router.ts`: with `appConfig.shopOnly`, `/` and every other
  guarded route redirect to `/coffee`. Only `login`, `coffee` and `me` remain.
- `frontend/src/screens/OrderScreen.vue`: hide **Edit the menu** (`/admin`)
  and **Live orders** (`/admin/orders`) when `shopOnly`.
- `AppShell`: show only the coffee link, and a small **Production** badge, so
  the projector makes clear which screen is which.
- Tests: a router unit test for the redirect. The browser suite does not need a
  production fixture tonight.

Ordering still works in production. Orders and vouchers are in-memory per Voter
process, so production has its own, which is what a separate environment should
do.

## Part 2: `k8s-trail` (by hand, once)

1. **Seed `main`.** Commit `configs/coffeeconfig.yaml` to `main`, copied from
   `clusters/k8s.koudijs.dev/demo2/coffee-config.yaml` (the mirror of the live
   object: no namespace, no status, no seed annotation). The production
   Kustomization fails on a missing path, and the board must not be empty when
   the ending starts.
2. **Read-only deploy key for Flux.** `ssh-keygen -t ed25519`, add the public
   half with `gh repo deploy-key add --repo ConfigButler/k8s-trail` (no
   `--allow-write`). The private half goes into a SOPS Secret (Part 3).
3. **Webhook.** After Part 3 is live, add
   `https://flux-webhook.k8s.koudijs.dev` + the `k8s-trail` Receiver's
   `status.webhookPath` to `k8s-trail` (push events), with the token from
   Secret `k8s-trail-receiver-token`.
4. Leave **"Automatically delete head branches"** off (it is off today). A
   deleted `coffee-change` leaves the next edits with nowhere to go.

## Part 3: `external/k8s`

**Only after Voter 2.1.0 runs** (the image automation bumps `app.yaml`), as one
commit to `main` after `git pull --rebase`. All paths are under
`k8s.koudijs.dev/2-gitops/`.

### 3a. The sink: coffee goes to the branch

- `voter-demo/git-sink.yaml`: GitProvider `k8s-trail` `allowedBranches:
  [main, coffee-change]`.
- **New** `voter-demo/git-sink-coffee.yaml`, listed in `kustomization.yaml`:
  - GitTarget `coffee`: `branch: coffee-change`, `path: configs`,
    `placement.byType` `coffeeconfigs: "coffeeconfig.yaml"` plus the required
    identity-complete `v1/secrets` route, `default: "{name}.yaml"`,
    `serializeNamespace: false`, `prune.mode: Always`, `commit.window: 5s`,
    message templates as demo2's with prefix `chore(coffee)`.
  - WatchRule `coffee`: `coffeeconfigs`, CREATE/UPDATE/DELETE.
- `voter-demo/git-sink-demo2.yaml`: remove `coffeeconfigs` from the WatchRule
  and from `placement`, and fix the comments. Otherwise every edit also lands on
  `main` directly and the PR shows nothing. demo2 keeps the per-voter results.
- `voter-demo/app.yaml`: `CONFIGBUTLER_GIT_TARGET_NAME` `demo2` → `coffee`, so
  the editor's "save now" closes the coffee window.
- `voter-demo/git-sink.yaml` header: rewrite "deliberately NOT a Flux source".
  It stays true for everything the room edits in `voter`. Only `configs/` on
  `main` is a source, and only into `demo-production`.

### 3b. Production: `voter-demo/production/`

A subfolder of `voter-demo` on purpose: the image automation's `update.path` is
`./k8s.koudijs.dev/2-gitops/voter-demo`, so a Voter release bumps production's
image too. It is added as `- production` in `voter-demo/kustomization.yaml`, so
it gets that Kustomization's SOPS decryption.

| File | Contents |
|---|---|
| `namespace.yaml` | Namespace `demo-production` |
| `secrets.yaml` (SOPS) | `voter-oidc-client` (same value as in `voter`), `krm-foyer-session-keys` (freshly generated), `k8s-trail-read` (the Flux deploy key: `identity`, `known_hosts`), `k8s-trail-receiver-token` |
| `rbac.yaml` | SA `voter` + Role `coffeeconfigs: [get]` (the storefront read). Role `coffee-reader` `coffeeconfigs: [get, list, watch]` bound to Group `demo:voter-audience`. **No `patch`/`update` for anyone but Flux** |
| `krm-foyer.yaml` | OCIRepository + HelmRelease, a copy of `voter-demo/krm-foyer.yaml` with `publicURL: https://demo-production.koudijs.dev`, the same `oidc` block (client `voter`), `sharedWatches.resources: [coffeeconfigs.examples.configbutler.ai]` |
| `app.yaml` | Deployment `voter`, a copy of the demo's with the image line and its `$imagepolicy` marker, and env `SHOP_ONLY=true`, `COFFEE_CONFIG_NAME=demo-coffee`, `CONFIGBUTLER_GIT_TARGET_NAME=""` (disables "save now"), without the ballot, database and coffee-admin variables. Service and NetworkPolicy as the demo's. **No CoffeeConfig seed here**: it comes from `configs/` |
| `ingress.yaml` | Certificate `demo-production-koudijs-dev` (letsencrypt-prod, DNS-01). Middlewares `foyer-identity` (→ `krm-foyer.demo-production.svc`) and `no-cookie`. IngressRoute: `/auth/ /k8s/ /stream/ /_foyer/` → krm-foyer, `/public/` → voter with `foyer-identity`, the rest → voter. No Room Pass or `/join-room` routes: joining happens on `demo.koudijs.dev` |
| `configs.yaml` | GitRepository `k8s-trail` (`ssh://git@github.com/ConfigButler/k8s-trail`, `main`, `interval: 1m`, `secretRef: k8s-trail-read`, `ignore: "/*\n!/configs/"`). SA `configs-applier` + Role (all verbs on `coffeeconfigs`) + RoleBinding. Kustomization `configs`: `path: ./configs`, `targetNamespace: demo-production`, `prune: true`, `serviceAccountName: configs-applier`, `interval: 1m`. Receiver `k8s-trail` for the webhook |

The `configs-applier` ServiceAccount is the safety line. The room writes to
`configs/` (through the reverser, and through the merge). Whatever lands there,
Flux can only create CoffeeConfigs in `demo-production`.

The admission policy `voter-editable-spec` matches people only, so Flux's
applies in `demo-production` pass it.

### 3c. Around it

- `auth/dex/dex.yaml`: add `https://demo-production.koudijs.dev/auth/callback`
  to client `voter`'s `redirectURIs`. Reusing the client means the API server
  already accepts these tokens (audience `voter` in
  `1-talos/files/authentication-config.yaml`), so **no Talos change or reboot**.
- The webhook gets its own Receiver in `demo-production` (in `configs.yaml`),
  not a line in `flux-receiver/`. Every vote pushes to `k8s-trail`, and those
  pushes should not wake the cluster's own GitRepository.
- The same commit drops `create` on `quizsubmissions` from `voter-audience`
  (the ballot gate from `demo-2026-10-07-plan.md`), which also waits for 2.1.0.
- demo2 prunes with `Always`, so once it stops watching `coffeeconfigs` it
  deletes `clusters/k8s.koudijs.dev/demo2/coffee-config.yaml` from main once.

## Order of work tonight

1. Voter `feat:` on main (Part 1) → release PR → 2.1.0 → the image
   automation deploys it to `voter`.
2. `k8s-trail` by hand (Part 2): seed `configs/` and the deploy key (done);
   the webhook after step 3.
3. `external/k8s` (Part 3), one commit, then
   `flux reconcile ks voter-demo --with-source`. Dex reconciles in its own
   Kustomization.
4. Check:
   - `kubectl -n demo-production get coffeeconfig demo-coffee` exists, owned by
     Flux.
   - `https://demo-production.koudijs.dev` signs in with GitHub and with a
     Room Pass phone, and shows only the shop.
   - As a participant: `kubectl auth can-i patch coffeeconfigs -n demo-production`
     → `no`.
5. Rehearse the ending: grant coffee-admin, edit a price on `demo`, see the
   commit on `coffee-change`, open the PR, merge, see production change. Then
   reset the PR state for the talk (point 7).
6. Rehearse a refusal: an edit on production is not offered, and a hand-made
   `PATCH` through `/k8s` returns 403.
7. For the talk: leave `coffee-change` level with `main` (no open diff), so the
   PR on stage contains only the room's edits.

## On stage

- The PR: `gh pr create -R ConfigButler/k8s-trail -B main -H coffee-change -t "Coffee: the room's menu to production"`,
  or open it earlier and let it fill. An open PR updates as commits arrive.
- Merge with **Create a merge commit**.
- If production does not move within ~10 s:
  `flux reconcile ks configs -n demo-production --with-source`.

## Risks and checks

- **Branch creation.** The reverser creates `coffee-change`, tracking `main`
  until its first push. With the seed already on `main`, an unchanged menu
  produces no commit. A live menu that differs from the seed produces one
  reconcile commit on the branch, which is harmless.
- **Diverging histories.** `main` keeps moving (demo1, demo2 and config
  targets), and `coffee-change` only touches `configs/`. The merge never
  conflicts.
- **DNS.** From this machine `demo-production.koudijs.dev` resolves to public
  Cloudflare IPv6 addresses, while `demo.koudijs.dev` resolves to
  `192.168.20.44` on the LAN. Check that it reaches the cluster from the venue
  the same way `demo` does.
- **Session.** krm-foyer cookies are per host, so a phone signs in again on
  production. With a Room Pass session already on Dex's host, that should be
  one redirect, not a new join. Verify on a real phone.
- **Shared-watch RBAC.** The krm-foyer chart's `sharedWatches` ServiceAccount
  needs list/watch on `coffeeconfigs` in `demo-production`. Check what the chart
  creates and add a Role if it does not.

## Rollback

- Production stuck: `flux reconcile ks configs -n demo-production --with-source`.
  As a last resort, show the merged file on GitHub. The point survives without
  the screen.
- The room's edits not reaching the branch: `kubectl -n voter get gittarget coffee`
  and the reverser's logs. Demo 2 itself is unaffected; only the PR is empty.
