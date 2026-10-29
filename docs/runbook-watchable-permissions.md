# Runbook: permissions the room can watch

Status (2026-10-29):

| Step | State |
| --- | --- |
| 1. krm-foyer namespaced shared watches | released in 0.4.0 (ConfigButler/krm-foyer#47) |
| 2. GitOps | **live**: RBAC and admission policy (`external/k8s` b16d590), krm-foyer 0.4.0 sharing `rolebindings` in voter (0ae667f) |
| 3. Verify the RBAC | every `kubectl` check below passes on the cluster |
| 4. Voter app | sunib/voter#46, fixture on krm-foyer 0.4.0 |
| 5. Rehearse | not yet |

## Why

To know "what may I do?", the browser polls a SelfSubjectRulesReview (a POST to
`/k8s/apis/authorization.k8s.io/v1/selfsubjectrulesreviews`) every 4 seconds (`frontend/src/api/useAuthorization.ts`) on every page that shows
permissions: the coffee menu editor, Databases, `/me` and `/room`. The operator's
page also polls the grant itself every 5 seconds, plus two 4-second polls in
`PickSomeone.vue` and `BallotSwitches.vue`. With 200 phones on the menu editor
that comes to about 50 rules reviews a second, almost all of them answering
"nothing changed".

Every live change to what the room may do is a **RoleBinding in `voter`** that the
operator page creates or deletes. Today that is:

| RoleBinding | Role | Subject | Made by |
| --- | --- | --- | --- |
| `voter-audience-coffee-admin` | `voter-audience-coffee-admin` | group `demo:voter-audience` | `/room`, coffee toggle |
| `voter-audience-coffee-admin-person` | `voter-audience-coffee-admin` | one `demo:` user | `/room`, pick someone |
| `voter-audience-ballot-<group>` | `voter-audience-ballot` | one answer group | `/room`, who may vote |
| `voter`, `room-pass`, `voter-audience` | same-named Roles | service accounts, audience group | Git (Flux) |

So the browser can **watch the RoleBindings in `voter`** and ask the rules review
again only when one of them changes. No new CRD is needed: the binding is the
truth, and watching it shows the room Kubernetes' actual RBAC, not a copy the app
keeps in sync.

## The agreement

> What a participant may do in `voter` depends only on RBAC objects in `voter`.

Concretely:

1. **Every RoleBinding in `voter` points at a Role in `voter`, never at a
   ClusterRole.** A ClusterRole could be edited outside the namespace, and nobody
   would be watching it. True today; step 2 makes it enforced.
2. **No ClusterRoleBinding grants participants anything in `voter`.** Today
   `system:authenticated` gets only `system:basic-user`, `system:discovery` and
   `system:public-info-viewer`, which grant nothing in a namespace.
3. **Group membership comes from the token**, so it changes only when someone
   signs in again, which reloads the page anyway.

What the agreement does not cover is handled by a slow backup poll (2 minutes):
Role edits arriving through Git, a ClusterRoleBinding someone adds by hand, and a
dropped stream.

## Design decision: all RoleBindings in `voter`, not by name

Participants get `get`, `list` and `watch` on **every RoleBinding in `voter`**,
instead of a `resourceNames` list:

- Binding names are dynamic (`voter-audience-ballot-<group>`, `…-person`). A name
  list would have to follow the door question's answers, and a binding that is
  missing from it fails without any error.
- One namespace-wide scope is **one shared watch** for the whole room. By-name
  scopes would be one stream per binding per phone.
- **What it reveals:** Role names, which are in Git; service account names; the
  audience and answer-group names; and the `demo:` username of the person picked
  on `/room`. Participants already see every `demo:` username through
  `Seat.spec.claimedBy`, and the pick happens on stage. Nothing here is secret.
  Re-check this table before adding a binding whose subject is a real email
  address.

**Roles are not watched.** They only change through Git, the 2-minute backup poll
covers that, and a second shared scope would double the krm-foyer access reviews
(see Load). Revisit if Roles ever change live.

## Shared watch, scoped to one namespace

`rolebindings.rbac.authorization.k8s.io` becomes a **krm-foyer shared watch**:
one watch at the API server for the whole room, with each phone checked by
krm-foyer's SubjectAccessReviews, as is already done for coffeeconfigs.

**The catch: the chart grants cluster-wide.** krm-foyer 0.3.0's chart turns every
entry in `sharedWatches.resources` into a ClusterRole rule bound with a
ClusterRoleBinding (`charts/krm-foyer/templates/shared-watches.yaml`). For
`rolebindings`, that would let `krm-foyer-voter-shared` read **every RoleBinding
in the cluster**, which is most of its access policy. krm-foyer's own
`docs/watches.md` names this as the reason it does not watch RBAC. That is not
acceptable, so this needs a small chart change first.

**Chart change (krm-foyer, its own release):** add a namespaced variant whose
grant is a Role and RoleBinding in each named namespace, and which is still
passed to `-shared-watch-resources`:

```yaml
sharedWatches:
  resources: [coffeeconfigs.examples.configbutler.ai, ...]   # unchanged: ClusterRole
  namespacedResources:
    voter: [rolebindings.rbac.authorization.k8s.io]          # new: Role in voter only
```

**Fallback, without a chart release:** set `serviceAccount.create: false`, and
manage the account, its token Secret, the ClusterRole for the CRDs and a Role for
RoleBindings in `voter` from Git. This works, but moving existing objects from
Helm to Flux is awkward. Helm deletes the account and Secret it no longer renders,
so it needs a careful two-step handover. Prefer the chart change.

## Steps

Order matters: each step works without the next, and the app must work without
the stream (step 4 falls back to polling).

### 1. krm-foyer: namespaced shared watches (`ConfigButler/krm-foyer`)

- `values.yaml`: `sharedWatches.namespacedResources` (map of namespace to
  resources).
- `templates/shared-watches.yaml`: for each namespace, a `Role`
  (`list`, `watch` on those resources) and a `RoleBinding` to the shared account.
  Add the resources to the `-shared-watch-resources` flag.
- Check that krm-foyer accepts a scope's namespace only where a namespaced
  resource has a grant. A stream with `namespace` omitted (all namespaces) must
  get `INTERNAL`/`FORBIDDEN`, not a working watch.
- Release it (e.g. 0.4.0).

### 2. GitOps (`external/k8s`, push to `main`)

Pull first: `cd external/k8s && git pull --rebase`.

**`voter-demo/participant-rbac.yaml`:** add to the `voter-audience` Role:

```yaml
  # Watched so a phone asks "what may I do?" again only when the operator's
  # page changes a grant (docs/runbook-watchable-permissions.md in voter).
  # Namespace-wide on purpose: the names are dynamic, and one scope is one
  # shared watch for the room. Never widen to roles or ClusterRoleBindings.
  - apiGroups: [rbac.authorization.k8s.io]
    resources: [rolebindings]
    verbs: [get, list, watch]
```

**`voter-demo/admission/rolebinding-roleref-policy.yaml`** (new): a
ValidatingAdmissionPolicy and binding scoped to namespace `voter` that refuses a
RoleBinding whose `roleRef.kind` is not `Role`. This enforces part 1 of the
agreement. Like the other policies, its source lives in this repo under
`voter/config/admission/`: add it there too, and list it in
`voter-demo/kustomization.yaml`.

**`voter-demo/krm-foyer.yaml`:** bump the chart (tag and digest) to step 1's
release and add:

```yaml
    sharedWatches:
      namespacedResources:
        voter: [rolebindings.rbac.authorization.k8s.io]
```

Leave `production/krm-foyer.yaml` alone: the shop-only deployment has no live
grants.

Reconcile: the owning Kustomization first, then the HelmRelease. The other order
fetches the old chart digest again.

```bash
flux reconcile kustomization voter-demo --with-source
flux reconcile helmrelease -n voter krm-foyer
```

### 3. Verify the RBAC (before any app change)

```bash
# A participant may read bindings in voter, and nowhere else.
kubectl auth can-i watch rolebindings -n voter   --as demo:probe --as-group demo:voter-audience   # yes
kubectl auth can-i watch rolebindings -n default --as demo:probe --as-group demo:voter-audience   # no
kubectl auth can-i watch roles        -n voter   --as demo:probe --as-group demo:voter-audience   # no

# krm-foyer's shared identity: voter only.
SA=system:serviceaccount:voter:krm-foyer-voter-shared
kubectl auth can-i watch rolebindings -n voter   --as $SA   # yes
kubectl auth can-i watch rolebindings -n kube-system --as $SA   # no
kubectl auth can-i watch rolebindings -A --as $SA   # no

# The admission policy: a ClusterRole ref is refused.
kubectl create rolebinding probe -n voter --clusterrole=view --group=nobody --dry-run=server   # denied
```

The last command is a server-side dry run. It goes through admission and is not
stored, so it does not break the "no `kubectl apply`" rule.

Then open the stream from a phone (`/stream/v1?group=rbac.authorization.k8s.io&
version=v1&resource=rolebindings&namespace=voter`) and check that
`krm_foyer_upstream_watches_open{identity="shared"}` goes up by one, not by one
per phone.

### 4. Voter app (`feat:`, deploys on its own)

**Needs krm-foyer 0.4.0 sharing `rolebindings` (step 2, live).** Without it
each phone's stream is its own watch at the API server, as that phone: it works,
but 200 phones are 200 watches.

Built: `frontend/src/api/grantChanges.ts` (`onGrantChange`), used by
`useAuthorization` and `RoomScreen`'s audience grant; `/me` shows which mode the
page is in (`data-testid="authz-mode"`).

**`frontend/src/api/useAuthorization.ts`:** replace the 4-second interval with a
trigger:

- Open `useLiveResources({ group: 'rbac.authorization.k8s.io', version: 'v1',
  resource: 'rolebindings', namespace: appConfig().namespace })`.
- On any added, modified or deleted event after the first `synced`, and again
  after every reconnect (events may have been missed), call `refresh()`.
  Debounce by about 300 ms: one toggle can touch several bindings.
- Backup poll: every **2 minutes**.
- **If the stream is refused or keeps failing, go back to the 4-second poll**,
  so the app never depends on step 2 having landed. Show the mode in
  `StreamDiagnostics`. *Built:* on `/me` instead. `StreamDiagnostics` renders
  only when the Room stream fails, so a mode shown there would rarely be seen.
- `AnswerScreen` keeps `poll: false` and its own marker trigger. It may also use
  the binding trigger, since ballot grants are RoleBindings too.

**Operator page:** `RoomScreen`'s `refreshGrant` (5 s), `PickSomeone` and
`BallotSwitches` (4 s each) read the same bindings, and now use the same
trigger. The operator is an admin, so this works before step 2 too, through a
per-user stream.

*Added to the plan:* `PickSomeone` and `BallotSwitches` also read Participants
(who can be picked, head counts per answer group), which change as people join
and no binding does. They follow those through their own stream
(`useLiveParticipants` in `roomGrants.ts`), so the pick pool and the counts
move as the room fills instead of every 4 s. Nothing on `/room` polls now
unless a stream is unavailable.

**Tests:**
- `frontend/src/api/grantChanges.test.ts`: the first snapshot does not count
  as a change, even when it arrives in one chunk; a burst gives one refresh;
  a reconnect gives one; `FORBIDDEN` falls back to the caller's interval; one
  stream per page, closed with its last subscriber.
- `test/browser/operator.spec.js`: no rules review for 6 s on the menu editor,
  then the coffee grant appears and disappears within 2 s, one review each.
- `test/browser/boundaries.spec.js`: the policy refuses a ClusterRole ref and
  allows a Role (server-side dry runs).
- `test/e2e/voter.yaml` carries step 2's participant rule, and
  `krm-foyer-values.yaml` its `sharedWatches.namespacedResources`, on 0.4.0.
- `test/browser/demo-groups.spec.js`: `/room` lists nothing for 6 s, a join
  moves a head count and a terminal's binding moves its switch, within 2 s.
- `test/browser/live-stream.spec.js`: two editors hold four shared
  subscriptions (the menu and the bindings each), and a withdrawn viewer
  releases both of its own and neither of the other's.
- `test/browser/operator.spec.js`: a participant on `/room` sees no grant, ballot
  or pick controls. Participants may now read the bindings, so the controls wait
  for the Room stream, which a participant is refused, rather than for the
  bindings' 403.

### 5. Rehearse

With `rehearse-production.mjs`, or a few phones:

- Flip the coffee toggle. The menu editor's Save appears within about a second.
  In the browser's network tab, `selfsubjectrulesreviews` is called once per
  flip, not every 4 s.
- Pick a person: only that phone changes.
- Kill the krm-foyer pod: the pages fall back to polling and recover when it
  returns.

## Load, before and after

200 phones on the menu editor:

| | Before | After |
| --- | --- | --- |
| Browser rules reviews | ~50/s (every phone, every 4 s) | ~1.7/s (2-minute backup) plus one burst of 200 per toggle |
| krm-foyer access reviews for the new scope | none | ~13/s (2 per phone per 30 s recheck) |
| Watches at the API server | none for RBAC | **1** (shared) |

The krm-foyer 30-second recheck is separate and stays as it is. Its own fix is
krm-foyer's "Ask 11: let a host trigger a recheck"
(`docs/investigations/krm-stream-shared-watches.md` in krm-foyer).

## Rollback

Revert step 4: the app goes back to the 4-second poll. Steps 1 to 3 can stay,
since they grant read access to a namespace's bindings and nothing more. To
remove those too, revert the `external/k8s` commit and let Flux reconcile.
