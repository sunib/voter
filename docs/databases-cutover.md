# Switching the live app over to the Databases page

What has to change on `demo.koudijs.dev` to go from what is running now to the
three-tab demo, in what order, and how to tell after each step whether it
worked. Written to be followed on a quiet afternoon, not fifteen minutes before
a talk.

## Where things stand

| | Running now | After |
| --- | --- | --- |
| Image | `ghcr.io/sunib/voter:sha-b8beaca` | `sha-4651525` — first fully green run since `b8beaca` |
| Tabs | Quizzes, Coffee, Room | Quizzes, Coffee, **Databases**, Room |
| `voter-audience` Role | ~~no `databases` rule~~ **done 2026-09-17** | `get, list, watch, create, patch, update` |
| `CONFIGBUTLER_DATABASE_GIT_TARGET_NAME` | unset | `demo-c` |
| CRD, GitTarget, WatchRule | ~~hand-applied~~ **adopted by Flux 2026-09-17** | in the Flux checkout |

The CRD is already Established and three requests from three teams are already
in `voter`. Nothing below needs to create them again.

## The one thing to be honest about first

**This is not an additive deploy.** The same change that added the Databases
page moved the coffee menu editor onto shared code — `liveEditableResource.ts`
in the frontend, `participant_save.go` in the backend. The coffee tests pass
unchanged and that is real evidence, but "the unit tests pass" is not "demo 1
still works in front of two hundred people".

So the coffee journey gets re-walked on the live deployment after the image
lands, before anything else is called done. It is step 4 and it is not optional.

## The four steps

Each is its own commit and its own reconcile. They are separable on purpose: if
something is wrong, the blast radius is one of these and the revert is obvious.

### 1. RBAC — done, 2026-09-17 (`db3205e`)

Applied and verified. Left here because the reasoning is the useful part and
because a namespace rebuild would need it again.

```sh
cd external/k8s && git push origin main
# voter-demo sits three deep in the dependency chain, each link on a 10m
# interval: infra -> infra-config -> voter-demo. Left alone it takes ~30 min.
flux -n flux-system reconcile kustomization infra-config --with-source
flux -n flux-system reconcile kustomization voter-demo
```

This grants verbs on a type that exists, for a page that is not in the running
image. Nobody can reach it, so nothing changes for anyone. Doing it first means
that when the image does land, the page simply works rather than needing a
second change under time pressure.

Check:

```sh
kubectl -n voter get role voter-audience -o yaml | grep -A4 platform.configbutler.ai
kubectl auth can-i create databases.platform.configbutler.ai \
  -n voter --as-group=demo:voter-audience --as=demo:someone     # yes
kubectl auth can-i delete databases.platform.configbutler.ai \
  -n voter --as-group=demo:voter-audience --as=demo:someone     # no
```

The second `no` is as important as the first `yes`. Both answered correctly on
the live cluster, for all seven verbs, as `demo:someone` in group
`demo:voter-audience`.

### 2. The CRD and demo-c into Git — done, 2026-09-17 (`3b37533`)

Three objects are alive in the cluster that Flux has never heard of: the CRD,
the `demo-c` GitTarget and its WatchRule. They survive until something rebuilds
the namespace or the cluster, and then they are gone with no record that they
were ever there.

- Copy `voter/config/crd/databases.yaml` into `voter-demo/crds/databases.yaml`
  and add it to `kustomization.yaml` beside the other four.
- Add the existing `voter-demo/git-sink-demo-c.yaml` to `kustomization.yaml`.

Flux adopts running objects rather than recreating them, so this is a no-op at
runtime — which is exactly why it is worth doing while it is a no-op. Doing it
*after* the image would mean debugging a rebuild during the week of the talk.

Check: `flux -n flux-system get kustomization voter-demo` reports the new
revision and `Applied`, and `kubectl -n voter get gittarget demo-c` still shows
the same `creationTimestamp` it had before — adopted, not replaced.

It did. Both kept their original timestamps and the three Database objects were
untouched. The CRD now carries **two** Apply owners, `simon-kubectl` and
`kustomize-controller`, which is harmless while the two agree — they applied the
same bytes. Hand-applying that CRD again with `--field-manager=simon-kubectl`
and a *different* body would be a real conflict; let Flux own it from here.
`demo-c` needed no such care: Flux takes `kubectl-client-side-apply` over
automatically, and it is now the sole owner.

### 3. The image

Wait for a green run on `main` and take the digest it reports. Do not take
`:main` — the deployment pins by digest and should keep doing so.

```sh
gh run list --branch main --limit 1          # completed / success
docker buildx imagetools inspect ghcr.io/sunib/voter:sha-<short> --format '{{.Manifest.Digest}}'
```

As of 2026-09-17 that build exists and every job in its run is green:

```
image: ghcr.io/sunib/voter:sha-4651525@sha256:5a832e76be37c6ee68c3b460f563ec35c0e68c9d5fabf1f3f6050e2b7d248e87
```

**A published image does not mean a green run.** The `images` job is
`needs: [ci-container, lint, test]`, and `Browser login` is not in that list —
so a build whose Playwright specs failed still publishes `sha-<short>` and still
moves `:main`. That is not a bug in the workflow (an image is useful for
debugging the very failure), but it does mean the registry cannot be used as a
signal of health.

It has already happened once: `sha-927c61a` exists and `:main` points at it,
from a run whose browser specs failed on a reworded notice. Check the run, not
the registry.

Four runs in a row before that were `cancelled` rather than failed, because each
push cancelled its predecessor through the concurrency group. A cancelled run
publishes nothing. If `sha-<short>` is missing for a commit that looks fine,
that is usually why — push once and let it finish.

In `voter-demo/app.yaml`, one line:

```yaml
image: ghcr.io/sunib/voter:sha-<short>@sha256:<digest>
```

and, in the same commit, the env var that turns a page save into a commit
message:

```yaml
            # The GitTarget whose window a Database save finalizes. demo-c, not
            # demo2: pointing it at the coffee target would close demo 1's open
            # window from a page that never touched the menu.
            - name: CONFIGBUTLER_DATABASE_GIT_TARGET_NAME
              value: demo-c
```

Push, reconcile, then:

```sh
kubectl -n voter rollout status deploy/voter --timeout=120s
kubectl -n voter get deploy voter -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
```

The build badge in the top bar carries the commit; if it reads the new short sha
the browser has the new bundle and not a cached one.

### 4. Walk both demos on the live deployment

**Coffee, in full, because this deploy changed its internals.** Sign in as a
participant, open `/admin`, confirm the read-only refusal is still the refusal;
have the operator grant from `/room`; edit a price; watch the dirty dot, the
change summary and the save; confirm the commit lands in the audit-trail repo
under the participant's name. If any of that is different from last rehearsal,
stop and say so — that is what this step is for.

**Databases.** Open `/databases`, see three requests grouped by cost centre.
Open one, change the tier, type a reason, save; watch the dot, the summary, the
commit. File a new one from `/databases/new`. Then, from a terminal:

```sh
kubectl -n voter patch database checkout-postgresql --type=merge \
  -p '{"spec":{"size":"large"}}'
```

and watch it move on the open page without a reload. That one is worth
rehearsing because it is the beat that proves the page is a view of the API
rather than an application with a database behind it.

## Rollback

Per step, and each one is a `git revert` in the platform checkout followed by a
reconcile. The image is pinned by digest, so reverting step 3 puts back exactly
the bytes that were running, not whatever `:main` points at by then.

The one thing a revert does **not** undo is Database objects people created
while the page was up. They are ordinary namespaced objects and survive the
rollback; `kubectl -n voter delete databases --all` is the reset, and it also
deletes the three seeded ones, so re-apply `new-page/example-databases.yaml`
afterwards.

## Order, and why it is this order

1. **RBAC first** because it is invisible until the image lands, so it can be
   wrong for a day without anyone noticing, and right for a day before anyone
   needs it. Which is what happened: it went out while CI was still red, and
   cost nothing.
2. **Git adoption second** because it is a no-op now and an incident later.
3. **Image third** because it is the only step that changes what a browser sees,
   and it should change one thing at a time.
4. **Rehearsal fourth** because the deploy is not the deliverable; the demo is.

The tempting shortcut is one commit with all of it. It saves ten minutes and
costs the ability to say which of four changes broke the coffee editor.

## Not in this cutover

- **The operator's switch does not cover databases.** `/room` grants and revokes
  the coffee Role only; `AUDIENCE_COFFEE_ADMIN_ROLE` names one Role and the
  backend derives both the Role and the binding from it. Putting databases
  behind a switch too would be a code change, not a manifest one — and the demo
  reads better with the room able to write from the start. See
  [databases.md](databases.md#2-the-participant-role) for the argument.
- **Nothing reconciles a `Database`.** `status.phase` stays empty and the list
  says "Not provisioned". That is the point, not a gap to close before shipping.
