# demo-c: putting the database requests into Git

**Applied and verified on 2026-09-17.** This page stays in the plan voice
because the reasoning is the useful part, but the GitTarget and the WatchRule are
live on `k8s.koudijs.dev` and `clusters/k8s.koudijs.dev/demo-c/` exists. What ran
and what it produced is at the bottom, under "What happened when it ran".

It makes a `Database` request land in `ConfigButler/k8s-audit-trail` the way a
quiz ballot and a coffee price already do, under the name of whoever filed it.

The whole of it is **two new objects and one environment variable**. That is
small because the application was built expecting this: `databases.md` ends with
"when a GitTarget does start watching these", and the backend already has the
empty setting waiting. The two objects are done; the variable waits for a
deploy.

## What this buys

Today the note a person types into "why are you making this change?" is written
onto the object as `platform.configbutler.ai/intent` and stops there. The list
page reads it back, and that is the end of its life — until the object is
deleted, when it goes with it.

With demo-c, that same note becomes a commit message in a repository nothing
reconciles from, authored by the human the API server authenticated. The intent
stops being a field on a live object and becomes a record: *this team asked for
this database, on this date, for this reason, and here is the diff.*

That is the sentence the page was built to be able to say. The CRD is the
interface, the form is the process, and the commit is the proof — with no
controller, no provisioning, and nothing at all from the team who will
eventually run the database.

## Three things that are already true

Worth stating, because each one is a step this plan does **not** need.

**1. The CRD is applied.** `databases.platform.configbutler.ai` is Established on
`k8s.koudijs.dev` as of 2026-09-17, applied by hand with `kubectl apply
--server-side`. `kubectl -n voter get db` answers. It is **not** in the Flux
checkout, so a namespace or cluster rebuild loses it — see the loose end at the
bottom.

**2. The reverser may already read the new type.** The cluster runs
`gitops-reverser-watch-any`, which grants `get`/`list`/`watch` on `*/*`. A type
installed after the operator just works, which is exactly the case this is. Had
the chart been on `rbac.watchTypes.mode: selected`, this plan would have needed
a Helm value and a controller restart; it does not.

**3. The intent annotation survives the write to Git.** The reverser strips
operational annotations only — `kubectl.kubernetes.io/*`,
`kustomize.toolkit.fluxcd.io/*`, Argo's tracking ids and a short fixed list
(`internal/sanitize/types.go:113`). `platform.configbutler.ai/intent` is none of
those, so it travels into the mirrored document beside the spec it explains.
This is the load-bearing fact of the whole plan and it is worth re-checking
against the release actually running before the talk.

## The change

### 1. A GitTarget and a WatchRule

One new file in the platform checkout, beside its three siblings:
`external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/git-sink-demo-c.yaml`, added to
`kustomization.yaml`.

```yaml
# demo-c: what the teams asked the platform team for.
#
# The third folder, and the first one whose documents nothing in the cluster
# reconciles -- a Database has no controller, so the mirrored file IS the
# deliverable rather than a record of one.
apiVersion: configbutler.ai/v1alpha3
kind: GitTarget
metadata:
  name: demo-c
  namespace: voter
spec:
  gitProviderRef:
    name: k8s-audit-trail
  branch: main
  # Sibling of demo1, demo2 and gitops-reverser-config, never nested inside one:
  # all of them prune with the resync mark-and-sweep, and a folder inside
  # another target's path asks a converging mirror to reason about documents it
  # does not own. Immutable -- renaming it later means delete and recreate, and
  # that is the one operation that loses the folder's history.
  path: clusters/k8s.koudijs.dev/demo-c
  placement:
    byType:
      # No Secret can reach this target: the WatchRule below names one CRD kind
      # and nothing else. The route is here because admission refuses a layout
      # that is not identity-complete unless every sensitive type has a route
      # where collision is impossible. Same reason as demo1's.
      v1/secrets: "secrets/{namespace}/{name}{sensitiveSuffix}"
    # One file per request, flat. A request is one object with one owner, so the
    # object's own name is the honest filename, and on a projector the folder
    # listing reads as the queue: checkout-postgresql.yaml, ledger-mysql.yaml.
    default: "{name}.yaml"
  serializeNamespace: false
  prune:
    # A withdrawn request leaves Git. `Always` rather than `OnEvent` so the
    # resync may also infer a deletion the reverser was not running to see.
    mode: Always
  commit:
    window: 5s
    message:
      # Only used when NOTHING closed the window early -- a kubectl-driven
      # change, or a save made while CONFIGBUTLER_DATABASE_GIT_TARGET_NAME is
      # still empty. A save from the page carries the requester's own words
      # instead, via the CommitRequest.
      #
      # No name in the subject: the Author header already carries it, same rule
      # as demo1 and demo2.
      liveTemplate: |-
        chore(demo-c): {{.Count}} database request{{if ne .Count 1}}s{{end}}

        {{range .Resources -}}
        - [{{.Operation}}] {{.Kind}}/{{.Name}}
        {{end -}}
      reconcileTemplate: "chore(demo-c): reconcile {{.Count}} {{if .Resource}}{{.Resource}}{{else}}resources{{end}}{{with .Revision}} at rv {{.}}{{end}}"
---
apiVersion: configbutler.ai/v1alpha3
kind: WatchRule
metadata:
  name: demo-c
  namespace: voter
spec:
  gitTargetRef:
    name: demo-c
  rules:
    # sourceNamespace omitted, which means this WatchRule's own namespace --
    # "objects in the voter namespace", and nothing from anywhere else.
    - apiGroups: [platform.configbutler.ai]
      apiVersions: [v1alpha1]
      resources: [databases]
      operations: [CREATE, UPDATE, DELETE]
```

### 2. One environment variable

In `voter-demo/app.yaml`, on the Deployment:

```yaml
- name: CONFIGBUTLER_DATABASE_GIT_TARGET_NAME
  value: demo-c
```

That is the entire application change. It turns a database save into a
`CommitRequest` against demo-c, so the note becomes the commit message and the
commit lands while the person is still looking at the screen instead of five
seconds later under the template above. The receipt shape and what the screen
does with it already match the coffee editor's.

Leave it empty and everything still works — the documents reach Git on the 5s
window with the generic subject. The variable buys the *message*, not the
mirroring.

### 3. The participant Role rule (already required, unrelated to Git)

The `voter-audience` Role still needs the `databases` rule from
[databases.md](databases.md#2-the-participant-role). It is a prerequisite for
the page working at all, not for demo-c specifically — noted here so it is not
discovered at the wrong moment. `commitrequests: create` is already granted, so
nothing extra is needed for "save now".

## Order, and what each step should look like

1. **Apply the GitTarget and WatchRule** (via Flux, as everything else). The
   target reconciles, creates nothing in Git yet, and reports Ready.
2. **Watch it mirror itself.** `gitops-reverser-config` watches `gittargets` and
   `watchrules` in this namespace, so demo-c's own definition is committed to
   `clusters/k8s.koudijs.dev/gitops-reverser-config/voter/gittargets/demo-c.yaml`
   within one window. The configuration writes down its own arrival — this is
   free and is the better version of the beat.
3. **Existing Databases appear on the first resync**, not on a write. If the
   namespace already holds seeded requests, the folder fills without anyone
   touching anything.
4. **Set the environment variable, deploy, file a request from the page.** The
   commit message is the requester's own sentence and the author is the
   requester.

## Decided: one folder, flat

**Every request in one folder, named after the object.** `default:
"{name}.yaml"`, no subfolders, no grouping. This is settled — not a placeholder
for something better later.

The alternative was filing per team, the way demo2 files per voter:
`teams/{label:...}.yaml`, one file per team with their requests accumulating.
It is written down here only so nobody re-opens it on the day, because it does
not work today and the reason is structural: **placement templates read labels,
and the team is in `spec.owner.team`.** A template cannot reach into a spec.

Making it work would mean the backend stamping a label from that field on
create — a real change to `participant_databases.go` — plus an answer for what
happens when someone later edits `owner.team` and the label no longer agrees
with it. The file would not move, and Git would go on filing a payments request
under retail's name. A mirror that is quietly wrong about who owns a database is
worse than a flat folder that is right.

And flat is not the consolation prize. For a queue of outstanding requests, a
directory listing that reads `checkout-postgresql.yaml`, `ledger-mysql.yaml`,
`fraud-scoring-postgresql.yaml` **is** the thing you want on a projector: one
line per ask, in a folder whose length is the size of the backlog.

## What happened when it ran

Applied with `kubectl apply -f new-page/git-sink-demo-c.yaml` (hand-applied, like
the CRD — see the loose ends). The target reported `Ready/Succeeded` with one
watch stream in under eleven seconds.

**The three seeded requests arrived by resync, not by a write.** They already
existed, so the first reconcile swept them in:

```
2ae2ee2  ConfigButler Bot <bot@configbutler.ai>
         chore(demo-c): reconcile 3 databases at rv 3463468
```

Authored by the bot, correctly: nobody made that change, the mirror simply
caught up. This is the one commit in the folder that is *not* attributed to a
person, and it is the right answer.

**demo-c mirrored its own arrival.** `gitops-reverser-config` watches
`gittargets` and `watchrules`, so the same minute produced
`gitops-reverser-config/voter/gittargets/demo-c.yaml` and its WatchRule beside
it, under `chore(config): 2 ConfigButler changes` — authored by the human who
ran the apply. The configuration wrote itself down. That was free.

**A live edit is attributed to the person who made it.** Patching one request
with `kubectl` produced:

```
commit cdb19e1
Author:    admin <admin@noreply.cluster.local>
Committer: ConfigButler Bot <bot@configbutler.ai>

chore(demo-c): 1 database request

- [UPDATE] Database/loyalty-points-mysql
```

and this diff, which is the thing to put on the projector — **the reason changes
in the same commit as the spec it explains**:

```diff
   annotations:
-    platform.configbutler.ai/intent: 'New loyalty scheme launches in Q1 ...'
+    platform.configbutler.ai/intent: 'Bumping to medium: the launch forecast
+      doubled after the campaign was signed off ...'
 spec:
   backup:
-    pointInTimeRecovery: false
-    retentionDays: 7
+    pointInTimeRecovery: true
+    retentionDays: 35
-  size: small
-  tier: standard
+  size: medium
+  tier: business-critical
```

The load-bearing claim held: the intent annotation is in the committed document.

**Still outstanding**, and both need a deploy rather than this change:

- `CONFIGBUTLER_DATABASE_GIT_TARGET_NAME=demo-c` on the Deployment. Without it a
  save from the *page* still mirrors — it just lands on the 5s window under the
  generic subject instead of carrying the requester's own sentence as the commit
  message.
- The `databases` rule on the `voter-audience` Role, without which the page gets
  a real 403 for everyone who is not cluster-admin. It is now **written and
  committed** in the platform checkout and waiting on a push;
  [databases-cutover.md](databases-cutover.md) is the order it goes out in.

## Loose ends

- **The GitTarget and WatchRule are hand-applied**, like the CRD. Flux does not
  know they exist, so they survive until something rebuilds the namespace. The
  file is already in the platform checkout at
  `voter-demo/git-sink-demo-c.yaml`; adding that one line to
  `voter-demo/kustomization.yaml` and pushing is what makes it permanent, and
  Flux will simply adopt the running objects.
- **The CRD is hand-applied.** It survives until something rebuilds the cluster.
  It belongs in `voter-demo/crds/` next to the others, and that is a five-minute
  change that should happen before it is forgotten — not because the demo needs
  it, but because the next rebuild will not know it was ever there.
- **Rehearsal reset.** `kubectl -n voter delete databases --all` — with
  `prune: Always` this also writes every removal to Git, which is worth seeing
  once and not on stage.
- **Two targets over one type is fine**, if demo-c ever wants to share
  `databases` with another folder. demo1 and demo2 already share
  `quizsubmissions`; it costs one extra watch stream and the operator
  serializes per branch.
