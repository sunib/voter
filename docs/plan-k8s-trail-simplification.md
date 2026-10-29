# Plan: a simpler k8s-trail, on gitops-reverser 0.55.0

Status: **done**, 2026-10-29, the evening before the final demo; see
[Outcome](#outcome). It replaces the six-target layout described in
[demo-gitops-reverser-layout.md](demo-gitops-reverser-layout.md), and upgraded
the operator from 0.49.1 to 0.55.0 in the same pass.

## What it becomes

`ConfigButler/k8s-trail`, branch main:

```
README.md                         what each folder is, who writes it, what reads it
seats/
  mission2.yaml                   the SeatingPlan (the room's size)
  row-02.yaml                     every claimed Seat in row 2, one document each
  …
quizzes/
  round-1.yaml                    a QuizSession: questions, state (closed/live)
  round-2.yaml
room-pass/
  rooms.yaml                      every Room Pass Room, one document each
databases/
  <name>.yaml                     one file per "I need a database" request
configs/
  coffee/
    coffeeconfig.yaml             what demo-production runs (edited on branch coffee-change)
  gitops-reverser/
    providers.yaml                KubeProvider local, GitProvider k8s-trail, GitBranches main + coffee-change
    seats.yaml                    GitTarget + WatchRule "seats"
    coffee.yaml                   GitTarget + WatchRule "coffee"
    quizzes.yaml                  GitTarget + WatchRule "quizzes"
    room-pass.yaml                GitTarget + WatchRule "room-pass"
    databases.yaml                GitTarget + WatchRule "databases"
    gitops-reverser.yaml          GitTarget + WatchRule that write this folder
    secrets/voter/
      k8s-trail-git.sops.yaml     the GitProvider's deploy key, SOPS-encrypted, and no other Secret
```

Branch `coffee-change` carries the same tree; the room's coffee edits land in
its `configs/coffee/coffeeconfig.yaml` and reach main (and production) only
through a merged PR.

Six GitTargets in namespace `voter`, all through KubeProvider `local` and
GitProvider `k8s-trail`:

| GitTarget | GitBranch | spec.path | Watches (ns `voter`) | Placement |
|---|---|---|---|---|
| `seats` | main | `seats` | seats, seatingplans | seats → `row-{label:voter.configbutler.ai/row}.yaml`; seatingplans → `{name}.yaml` |
| `coffee` | coffee-change | `configs/coffee` | coffeeconfigs | → `coffeeconfig.yaml` |
| `quizzes` | main | `quizzes` | quizsessions (**not** quizsubmissions) | `{name}.yaml` |
| `room-pass` | main | `room-pass` | rooms.room-pass.koudijs.dev (**not** participants) | → `rooms.yaml` |
| `databases` | main | `databases` | databases.platform.configbutler.ai | `{name}.yaml` |
| `gitops-reverser` | main | `configs/gitops-reverser` | kubeproviders, gitproviders, gitbranches, gittargets, watchrules; Secrets labelled `voter.configbutler.ai/mirror: "true"` | connection kinds → `providers.yaml`; secrets → `secrets/{namespace}/{name}{sensitiveSuffix}`; else `{name}.yaml` |

A GitTarget and its WatchRule share a name, so `{name}.yaml` puts each
scenario's pair in one file.

Kept from today: names `seats` and `coffee` (the app sends CommitRequests to
them by name), the committer and signing key, a 5s quiet window, `prune: Always`,
`serializeNamespace: false` on the content targets, the phone's *why* as the
seat commit message, the requester's sentence as the database commit message,
and the merge-to-production flow.

### The encrypted Secret

0.50.0 added `objectSelector` to WatchRule rules. The self-mirror's Secret rule
selects `voter.configbutler.ai/mirror: "true"`, and only `k8s-trail-git` carries
that label (added in `git-sink-deploy-key.yaml`, re-encrypted with SOPS). So
`configs/gitops-reverser/secrets/voter/k8s-trail-git.sops.yaml` is the only
Secret in the repository, next to the GitProvider that names it. Encryption is
unchanged: SOPS with age, recipient from the existing `gitops-reverser-age`
Secret, so no new key to back up.

### The room-pass folder

Status is never serialized, so the join code (a status field) and the
participant count stay out. The spec holds title, tagline, the GitOps question
with its group names, `audienceGroup`, `maxParticipants`, `endsAt`, the
join-code *policy* and `allowedReturnURLs`. Participants (attendees' typed
names) are a separate kind and are not watched.

## The upgrade: 0.49.1 → 0.55.0

Sixteen breaking changes in six releases. What each means here:

| Release | Change | Effect on this cluster |
|---|---|---|
| 0.50 | `rules[].operations` removed | dropped from every WatchRule |
| 0.50 | durations are Go duration strings | ours already are |
| 0.50 | reasons, columns, status fields renamed | `CommitRequest.status.sha` → `status.commit`: **app change** |
| 0.50 | finished CommitRequests deleted after 48h | fine; the app follows a request for seconds |
| 0.50 | `objectSelector` on rules | **used**, for the Secret |
| 0.51 | `commit.window: 5s` → `window: {idleTimeout, maxDuration}` | rewritten; a stored string window breaks listing GitTargets, so every GitTarget is deleted before the CRDs change |
| 0.51 | `CommitRequest.closeDelaySeconds` → `window`, default *attach within 5s, close 2s later* | the app sends `closeDelaySeconds` while `CONFIGBUTLER_CLOSE_DELAY_SECONDS > 0`; set to `0` so it sends nothing and the default applies |
| 0.52 | branch worker internals | none |
| 0.53 | applier claims (Flux/Argo) | not used |
| 0.54 | `ClusterProvider` → namespaced `KubeProvider`, required `spec.kubeProviderRef` | own KubeProvider `local` in `voter` (chart's disabled); the ClusterWatchRule goes, nothing cluster-scoped is left to mirror |
| 0.54 | `GitBranch` object; GitTarget `gitProviderRef`/`branch` → `gitBranchRef` | GitBranches `main` and `coffee-change` |
| 0.54 | `allowedBranches` → `allowedGitBranches` grants with `branchPatterns` | rewritten on the GitProvider |
| 0.54 | audit route becomes `<namespace>.<name>` | the API servers post to `/audit-webhook/default` (Talos file). Pinned with `spec.attribution.auditRoute: default`, so **no Talos change, no reboot** |
| 0.54 | receiver, metrics, Argo, Flux claims | not used |
| 0.55 | admission webhook removed; a CommitRequest's author comes from audit, needs `commitrequests` create/patch at `RequestResponse` | the Talos audit policy's write catch-all already logs every create/patch at `RequestResponse`: covered |
| 0.55 | `CommitRequest.gitTargetRef` may name a namespace | omitted means own, as the app does |

Checked by rendering both charts with our values: the audit Service
(`10.96.0.44:9444`), the Issuer, root CA and server certificate names are
identical, so the CA pinned in the API servers' audit webhook keeps working. The
`watch-any` ClusterRole is unchanged. The new values pass the chart's schema.

Chart values: `clusterProvider` removed; `kubeProvider.create: false`;
`controllerManager.inClusterKubeProviders: [voter/local]`.

Left behind by the upgrade (harmless, for a later hand clean-up):
CRD `clusterproviders.configbutler.ai`, Secret
`gitops-reverser/gitops-reverser-admission-server-cert`.

### The app

- `frontend/src/api/commitStatus.ts`: read `status.commit`, falling back to
  `status.sha`. Works on both operator versions, so it ships first, as a `fix:`.
- `app.yaml`: `CONFIGBUTLER_CLOSE_DELAY_SECONDS: "0"`, and
  `CONFIGBUTLER_DATABASE_GIT_TARGET_NAME: databases`.
- The seat-why admission policy reads only `spec.gitTargetRef.name`: unchanged.

## What gets removed

- From the cluster, through `external/k8s`: GitTargets and WatchRules `demo1`,
  `demo2`, `demo-c`, `gitops-reverser-config` (+ its ClusterWatchRule); `seats`
  and `coffee` are recreated at their new paths. No longer mirrored: votes,
  Roles/RoleBindings, every Secret but one.
- From `external/k8s`: `git-sink-demo1.yaml`, `git-sink-demo2.yaml`,
  `git-sink-demo-c.yaml`, `git-sink-config.yaml`; replaced by
  `git-sink-quizzes.yaml`, `git-sink-room-pass.yaml`, `git-sink-databases.yaml`,
  `git-sink-gitops-reverser.yaml`.
- From `k8s-trail` main, one hand-made commit: all of `clusters/` (67 files),
  `configs/coffeeconfig.yaml` moved to `configs/coffee/`, `configs/README.md`
  replaced by a root `README.md`.

## Order of execution

1. **App:** the `status.commit` fix, released and deployed by image automation.
2. **external/k8s, commit 1:** remove all six targets (and the ClusterWatchRule)
   from the kustomization. Reconcile; `kubectl -n voter get gittargets` is
   empty. From here until step 4, saves work but do not commit.
3. **k8s-trail, the clean-up commit** on main. Production still finds
   `configs/coffee/coffeeconfig.yaml` (Flux's generated kustomization includes
   subfolders).
4. **external/k8s, commit 2:** chart 0.55.0, KubeProvider, GitProvider,
   GitBranches, the six targets, the Secret label, the app env, the production
   path. Reconcile `gitops-reverser` (Kustomization, then HelmRelease), then
   `voter-demo`.
5. **Verify** each folder appears; a seat claim, a coffee save (creates
   `coffee-change`), a database request; merge a coffee PR and watch production.

Rollback: revert the two external/k8s commits. Going back to 0.49.1 needs the
0.55 objects deleted first, because the old CRDs cannot read them.

Choices made where the earlier draft asked: `quizzes` (spelled out), flat
`seats/row-NN.yaml`, rehearsal seats and requests kept (they appear in the new
folders on first reconcile; delete them from the operator page if the room
should start empty).

## Outcome

Executed 2026-10-29, 16:14–16:35.

| Step | Where | Commit |
|---|---|---|
| App reads `status.commit` | sunib/voter | `cfd192f`, released as 2.4.2, deployed by image automation (`4e58082` in external/k8s) |
| Targets out | external/k8s | `224246a` |
| Clean-up + root README | ConfigButler/k8s-trail | `4335ca8` |
| 0.55.0 + new targets + env + production path | external/k8s | `aea1dbc` |

Before pushing, the 16 new objects were server-side dry-run against the 0.55.0
CRDs in a throwaway k3d cluster (schemas and CEL rules, strict field
validation): all accepted.

Verified afterwards:

- Operator `ghcr.io/configbutler/gitops-reverser:0.55.0`, HelmRelease Ready, no
  error or warning in its log.
- KubeProvider `local` Ready with `FactsReceived=True`: audit events arrive on
  the pinned route `default`, so authors are attributed.
- Both GitBranches, all six GitTargets and WatchRules Ready, every stream
  running.
- k8s-trail main holds exactly the layout above; each first snapshot is a
  Verified commit by ConfigButler Bot. `room-pass/rooms.yaml` has no status (and
  no Flux labels); `secrets/voter/k8s-trail-git.sops.yaml` has only `ENC[…]`
  values and an age recipient, and is the only Secret.
- Production: Kustomization `configs` on `./configs/coffee`, Ready; the
  GitRepository ignores everything but `configs/coffee/`; `demo-coffee`
  unchanged; demo.koudijs.dev and demo-production.koudijs.dev answer 200.
- voter on both sites is 2.4.2, with `CONFIGBUTLER_CLOSE_DELAY_SECONDS=0` and
  `CONFIGBUTLER_DATABASE_GIT_TARGET_NAME=databases`.

Not yet verified, because each needs a person signed in through the app:

1. Claim or change a seat on the phone: one commit on main touching
   `seats/row-NN.yaml`, your why as its message, you as author, and the phone
   showing the commit link (that link is the 2.4.2 fix).
2. Save the coffee editor: branch `coffee-change` is created from main, with one
   commit touching `configs/coffee/coffeeconfig.yaml`.
3. Open a PR `coffee-change` → main, merge with a merge commit, and watch
   demo-production.koudijs.dev change.
4. File a database request: `databases/<name>.yaml`, with the requester's
   sentence as message.

A save now waits up to about 2s after its write is attributed, where it waited
about 1s before: the 0.55.0 default request window (`maxDuration: 2s`) replaced
the app's 1s close delay. Shortening it would need the app to send
`spec.window`.

Left for a hand clean-up (harmless; Helm does not remove them):

```bash
kubectl delete crd clusterproviders.configbutler.ai
kubectl -n gitops-reverser delete secret gitops-reverser-admission-server-cert
```
