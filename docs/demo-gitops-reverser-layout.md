# gitops-reverser on k8s.koudijs.dev: what it writes, and where

> **Superseded on 2026-10-29.** This describes the 0.49.1 layout under `clusters/`. The current layout, and how it got there, is in [plan-k8s-trail-simplification.md](plan-k8s-trail-simplification.md).

Status on 2026-10-29, the evening before the final demo. Sources: `external/k8s`
at `d884c03` (pulled), the live cluster, and `ConfigButler/k8s-trail` on GitHub.

## Verdict for tomorrow's three beats

| Beat | Where the room looks | Configured | Live right now |
|---|---|---|---|
| 1. Seats, one file per row | `k8s-trail` **main**, `clusters/k8s.koudijs.dev/seating/seats/mission2/row-NN.yaml` | yes | yes: `row-02`, `row-04`, `row-06` exist, signed, authored by the participant, with their *why* as the message |
| 2. Coffee edits on demo.koudijs.dev | `k8s-trail` branch **`coffee-change`**, `configs/coffeeconfig.yaml` | yes | **the branch does not exist at the moment**; the first save recreates it from main (see [Before the demo](#before-the-demo)) |
| 3. Merged menu on demo-production.koudijs.dev | `k8s-trail` **main** `configs/` → Flux → namespace `demo-production` | yes | yes: Flux at `main@3ef891e`, production `demo-coffee` equals the room's `demo-coffee` spec |

All six GitTargets, six WatchRules, the ClusterWatchRule and the GitProvider are
`Ready=True` with every stream running. The operator is chart **0.49.1**
(`2-gitops/gitops-reverser/release.yaml`).

## The shape of it

One GitProvider, six targets, one repository. Everything lives in namespace
`voter` because `GitTarget.spec.gitProviderRef` is namespace-local, and the
operator's `default` ClusterProvider admits `voter` (`accessFrom.names` in the
HelmRelease values).

```
GitProvider voter/k8s-trail
  url:             ssh://git@github.com/ConfigButler/k8s-trail.git
  write key:       Secret k8s-trail-git          (git-sink-deploy-key.yaml)
  signing key:     Secret k8s-audit-trail-signing (git-sink-secret.yaml)
  allowedBranches: main, coffee-change
  committer:       ConfigButler Bot <bot@configbutler.ai>   author = the human from the audit event
```

| GitTarget | Branch | Path in k8s-trail | Watches (ns `voter`) | File layout |
|---|---|---|---|---|
| `seats` | main | `clusters/k8s.koudijs.dev/seating` | seats, seatingplans | `seats/{plan label}/row-{row label}.yaml`, `seatingplans/{name}.yaml` |
| `coffee` | **coffee-change** | `configs` | coffeeconfigs | `coffeeconfig.yaml` |
| `demo1` | main | `clusters/k8s.koudijs.dev/demo1` | quizsessions, quizsubmissions, roles, rolebindings | `quiz-configs.yaml`, `submissions.yaml`, `authorization.yaml` |
| `demo2` | main | `clusters/k8s.koudijs.dev/demo2` | quizsubmissions | `results/{submitter label}.yaml` |
| `demo-c` | main | `clusters/k8s.koudijs.dev/demo-c` | databases | `{name}.yaml` |
| `gitops-reverser-config` | main | `clusters/k8s.koudijs.dev/gitops-reverser-config` | gitproviders, gittargets, watchrules, Secrets (+ ClusterWatchRule: clusterproviders) | `{namespace}/{resource}/{name}.yaml`, Secrets SOPS-encrypted |

Common to every target: `prune.mode: Always` (a deleted object leaves Git, also
on resync), `commit.window: 5s` (a burst is one commit; a CommitRequest closes
the window early with the person's own message), no name in the commit message
(the Author header carries it), and each body line ends in `@<resourceVersion>`.
Every target except `gitops-reverser-config` sets `serializeNamespace: false`.

## 1. Seats on main, one file per row

`voter-demo/git-sink-seats.yaml`:

```yaml
placement:
  byType:
    examples.configbutler.ai/v1alpha1/seats: "seats/{label:voter.configbutler.ai/seatingplan}/row-{label:voter.configbutler.ai/row}.yaml"
    examples.configbutler.ai/v1alpha1/seatingplans: "seatingplans/{name}.yaml"
```

- Every Seat carries `voter.configbutler.ai/seatingplan`, `.../row` (zero-padded,
  so `row-03` sorts before `row-10`) and `.../column`. Admission
  (`admission/seat-policy.yaml`) holds the labels equal to the spec for a
  participant, so a participant's seat cannot miss its row file. A seat without
  labels (only an operator could create one) lands in `row-_unlabeled.yaml`.
- Each seat is one YAML document in its row's file; a second person in row 4
  makes `row-04.yaml` a two-document file, and the diff shows exactly that.
- The phone writes the Seat, then a CommitRequest naming target `seats` with the
  participant's *why* (3 to 80 characters, `admission/seat-why-policy.yaml`).
  That sentence becomes the commit message, the participant is the author, the
  bot is the committer, and GitHub shows *Verified*.
- The active plan is `mission2` (8 rows × 12 columns), named in
  `seating-plan.yaml`, `admission/seat-in-plan-policy.yaml` and app.yaml's
  `SEATING_PLAN_NAME`.

What is there tonight:

```
clusters/k8s.koudijs.dev/seating/
  README.md, .gittargetignore
  seatingplans/mission2.yaml
  seats/mission2/row-02.yaml   Simon Koudijs   🐙 #9b5de5
  seats/mission2/row-04.yaml   Siempie2        🦖 #ffd23f
  seats/mission2/row-06.yaml   simon-on-stage  🐧 #a8e05f
```

Link to put on screen:
<https://github.com/ConfigButler/k8s-trail/tree/main/clusters/k8s.koudijs.dev/seating/seats/mission2>

## 2. Coffee edits from demo.koudijs.dev, on a branch

`voter-demo/git-sink-coffee.yaml`: GitTarget `coffee`, branch `coffee-change`,
path `configs`, the CoffeeConfig always to `configs/coffeeconfig.yaml`, no
namespace in the document.

- The voter app on demo.koudijs.dev has `CONFIGBUTLER_GIT_TARGET_NAME=coffee`
  (app.yaml), so "save now" sends a CommitRequest to this target. The target's
  name is load-bearing: rename one and the other breaks.
- Every save is a commit on `coffee-change` authored by whoever saved.
  Nothing on `coffee-change` reaches production by itself.
- `coffee-change` is in the GitProvider's `allowedBranches`; no other branch
  may be created by this key.
- **State tonight:** the branch is absent on GitHub. The last coffee PR
  (#8, merged 2026-10-07) was its last use; the branch has since been removed,
  even though the repository's *Automatically delete head branches* is off. The
  target is still Ready. Per its own configuration, the operator recreates
  `coffee-change` from main on the next write. That path last ran on
  2026-10-07, so rehearse it tonight rather than discovering it on stage.

## 3. The merged menu on demo-production.koudijs.dev

`voter-demo/production/configs.yaml`, applied in namespace `demo-production`:

```
PR coffee-change → main (merge commit)
  → GitHub push webhook → Receiver demo-production/k8s-trail      (seconds)
  → GitRepository demo-production/k8s-trail  ref main, ignores all but /configs/   (1m fallback)
  → Kustomization demo-production/configs    path ./configs, targetNamespace demo-production,
                                             runs as SA configs-applier (may only write coffeeconfigs here)
  → CoffeeConfig demo-production/demo-coffee
  → voter on demo-production.koudijs.dev shows it
```

- Production's voter has `CONFIGBUTLER_GIT_TARGET_NAME=""` and its ServiceAccount
  may only `get` coffeeconfigs; the audience group `demo:voter-audience` gets
  `get, list, watch`. Production is read-only: the only way in is the merge.
- Why `serializeNamespace: false` on the coffee target matters: it is what lets
  this Kustomization drop the document into `demo-production` with
  `targetNamespace`.
- **Merge with a merge commit**, not squash: a squash makes the person who merges
  the author of everyone's change. (Rebase-merge keeps the authors but rewrites
  the commits, so the bot's signatures no longer show as Verified.) The
  repository currently allows both squash and merge commits, so pick it by hand.
- Live tonight: GitRepository and Kustomization Ready at `main@3ef891e`, Receiver
  Ready, `deployment/voter` 1/1 on `ghcr.io/sunib/voter:2.4.1`, both sites answer
  200, and production's `demo-coffee` spec is identical to the room's. So
  tomorrow's first diff starts from a clean "production equals the room".

## The other three targets, for completeness

- **demo1** (quiz, flat): every round in `quiz-configs.yaml`, every vote in one
  growing `submissions.yaml`, Roles and RoleBindings together in
  `authorization.yaml` so the live grant (the runtime-created
  `voter-audience-coffee-admin` RoleBinding) appears as a diff.
- **demo2** (a file per voter): the same QuizSubmissions as demo1, filed as
  `results/<submitter label>.yaml`; unlabelled ones in `results/_anonymous.yaml`.
  The commit subject names the round when every resource in the window agrees.
- **demo-c**: one `{name}.yaml` per Database request; nothing reconciles them, the
  file is the deliverable.
- **gitops-reverser-config**: the configuration mirroring itself (GitProvider,
  GitTargets, WatchRules, ClusterProvider) plus every Secret in `voter`, encrypted
  with SOPS/age to an operator-generated key (`gitops-reverser-age`). Status is
  stripped and unchanged trees skip the commit, so it does not loop.
  CommitRequests are deliberately not watched.

## Before the demo

1. **Recreate `coffee-change` with a rehearsal save.** Change one thing in the
   coffee editor on demo.koudijs.dev and hit save now. Expected: branch
   `coffee-change` appears on k8s-trail, one commit ahead of main, authored by
   you. Then either open the PR from it as the demo's starting point, or put the
   value back (a second commit) so the branch is "main + noise" you can live with.
   Leave the branch alone after a merge: deleting it is what put us here.
2. **`k8s-trail` is public.** GitHub reports `private: false` (it was made private
   on 2026-09-24). If that is on purpose so the room can browse it, note what is
   in it: every Seat's `spec.claimedBy.username`. For GitHub sign-ins that is the
   email address; `row-02.yaml` contains `github:simonkoudijs@gmail.com` today.
   Room Pass participants show as an opaque `demo:…` token and their commit
   emails are `<name>@koudijs.dev.test`, so the audience is fine; the operator
   (you) is the one who leaks. Either claim your stage seat as a Room Pass
   participant, or accept it. Git history keeps it either way. No live quiz round
   asks for an email (round-2's questions: framework, role, diy, conflict).
3. **Decide about the rehearsal seats.** The three seats above are rehearsal
   data. Deleting them (as the operator) removes `row-02/04/06.yaml` from main,
   since prune is `Always`, so the room starts from an empty `seats/mission2/`.
   Keep them if you want the folder to already show the pattern.
4. **Merge method:** merge commit (see section 3).

## Small inconsistencies found (not blocking)

- `git-sink.yaml`'s header says "one GitProvider backs all three targets" and
  lists four files; there are six targets (`seats` and `demo-c` are missing
  from that list).
- `git-sink-demo1.yaml` calls k8s-trail "a public repository" while other notes
  call it private. It is public today.
- `git-sink-secret.yaml` still holds the retired `k8s-audit-trail-git` deploy key
  next to the signing key (known; shared SOPS MAC).
