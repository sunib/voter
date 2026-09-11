# Where participant commits land

Status: **shipped and carrying real traffic**, verified 2026-09-11. This document
described a design before any of it existed; it now describes what was built, and
records where the built thing diverged from the design and why.

The claim it exists to make true: *your change becomes a Git commit in your name*.
That is now a fact you can check — see [Proof](#proof-it-works) below.

## What shipped

Three objects in `ConfigButler/k8s`, namespace `voter`, in
`2-gitops/voter-demo/git-sink.yaml`, all `configbutler.ai/v1alpha3`:

| Object | Name | What it does |
| --- | --- | --- |
| `GitProvider` | `k8s-audit-trail` | `ssh://git@github.com/ConfigButler/k8s-audit-trail.git`, `allowedBranches: [main]`, robot committer, SSH-signed |
| `GitTarget` | `voter-demo` | branch `main`, path `clusters/k8s.koudijs.dev/voter` |
| `WatchRule` | `voter-demo` | `coffeeconfigs`, `quizsessions`, `quizsubmissions` on CREATE/UPDATE/DELETE |

The `GitTarget` name is API, not decoration: the backend sends `CommitRequest`s to
`CONFIGBUTLER_GIT_TARGET_NAME`, which is `voter-demo` in `app.yaml`.

Live state: all three `Ready=True`, `GitTarget` reporting `StreamsRunning 3/3`,
`RenderMatchesLive`, `GitPathAccepted`, and `GitProvider` reporting *"Repository
connectivity validated"*.

## Four ways the build diverged from the design

**The repository is `ConfigButler/k8s-audit-trail`, and it is PRIVATE.** The design
proposed a new public `sunib/coffee-demo-state` so attendees could open it on their
phones during the talk. Going private retires the risk this document called "the real
one" — attendee-typed display names becoming permanent public Git history — at the
cost of the payoff that motivated it. **If you want the phone-opening moment back,
that is a deliberate decision to re-take, not an oversight**, and it brings the whole
risk section below with it.

**The quiz is mirrored after all.** The design put `QuizSubmission` deliberately out
of scope, on the grounds that 200 vote objects would drown a readable menu diff. The
answer turned out to be placement rather than exclusion: every vote bundles into one
growing `submissions.yaml`, so the room's answers accumulate as a diffable file
instead of a folder of `vote-<64 hex>` noise.

**The layout is flat, and namespace-free.** `placement.default` is `{name}.yaml`, so
`demo-coffee.yaml` and the round sit beside `submissions.yaml` at the root of
`spec.path` rather than each in a folder of one. `serializeNamespace: false` keeps
`metadata.namespace` out of the documents because the folder already says it. Both
choices are for a projector: every extra level is a line of nothing.

**Pruning is `Always`, not `OnEvent`.** `OnEvent` leaves a gap — a DELETE the reverser
was not running to observe (restart, missed watch) strands the document in Git
forever, and the mirror drifts from the cluster with each one. `Always` adds the
resync mark-and-sweep that infers deletions from a desired snapshot.

## The Secret route that has to exist

`placement.byType` carries a rule no participant will ever trigger:

```yaml
v1/secrets: "secrets/{namespace}/{name}{sensitiveSuffix}"
```

No Secret can reach this target — the `WatchRule` covers three CRD kinds and nothing
else. It is there because bundling `quizsubmissions` into one file is a *bundling*
path, and the operator refuses a bundling default outright unless every sensitive
type has a route where collision is impossible. `{namespace}` and `{name}` make it
impossible; `{sensitiveSuffix}` renders `.sops.yaml` so the file would be encrypted.
Deleting the rule does not tighten anything — it stops the `GitTarget` validating.

## Who the commit says you are

The chain, all of it now carrying real data:

1. A participant joins through Room Pass and types a display name — validated to
   1–64 bytes, no control characters, no angle brackets, otherwise free text.
2. Room Pass sets `X-Remote-User-Name` to that name and `X-Remote-User-Email` to
   `<participant-id>@demo.invalid`, a synthetic address with no mailbox behind it.
3. The apiserver's authentication config maps those into
   `configbutler.ai/claims/display-name` and `configbutler.ai/claims/email`,
   alongside the username `demo:<opaque-dex-subject>`.
4. The audit event carries them to gitops-reverser, which uses the display name as
   the Git author when it is safe in a signature header and the username otherwise;
   the email claim when it is a valid address, and `<username>@cluster.local` otherwise.

The committer stays the robot; only the **author** is the participant. That split is
the whole point — `git show --format=fuller` then says the cluster made this commit
on a named human's behalf.

If attribution ever fails to resolve, the author is the loud
`unknown (attribution unresolved) <attribution-unresolved@gitops-reverser.invalid>`
rather than a silent fallback to the robot. A broken demo should look broken.

## Proof it works

Real commits in `clusters/k8s.koudijs.dev/voter`, newest first:

```
2d4374f  author: github:simonkoudijs@gmail.com   chore: 2 changes from github:simonkoudijs@gmail.com
1be209a  author: Simon <80ea3a9f…@demo.invalid>  chore: 1 change from demo:CkA4MGVh…room-pass
9bfce70  author: ConfigButler Bot                chore: reconcile 1 coffeeconfigs
```

`1be209a` is the one that matters. A participant who typed "Simon" into the Room Pass
join form changed the menu, and the commit is **authored by Simon**, with the opaque
`demo:<sub>` username — the identity RBAC actually authorized — in the message body.
Showing both is the demo: the subject names what Kubernetes checked, the author header
names the person who typed it. An operator write (`2d4374f`) proves the pipeline but
cannot stand in for this.

The `commitRequested: true` receipt is no longer the dishonest claim it was when this
document was written.

### Re-checking it before a room

1. `kubectl -n voter get gitprovider,gittarget,watchrule` — all `Ready=True`, and the
   `GitTarget` reporting `GitPathAccepted` and `StreamsRunning`.
2. Log in through Room Pass as a real participant, change a price, press save.
   `git show --format=fuller` must name the display name typed at the join form.
3. Confirm the `CommitRequest` reached `Ready=True` with a `status.sha` matching the
   commit you just read. These objects are transient — expect an empty list at rest.

`room-pass/test/browser/rehearse-production.mjs` drives exactly this against
production and prints the receipt.

## Prerequisites, both satisfied

**`ClusterProvider/default` is deny-by-default about which namespace may use it.**
It now reads `accessFrom.names: [gitops-reverser, voter]`, set in the HelmRelease
values at `2-gitops/gitops-reverser/release.yaml`. Without `voter` on that list the
`GitTarget` is refused.

The alternative — putting the `GitTarget` in the `gitops-reverser` namespace and
pointing `CONFIGBUTLER_COMMITREQUEST_NAMESPACE` there — would have given a room full
of strangers `create` rights inside the operator's own namespace. Widening
`accessFrom` by one name grants `voter` the right to write to its own Git folder,
which is what it should have.

**The credential is a deploy key, not a PAT** — one `ed25519` key, write access on
that one repository. A PAT carries the whole account. The private key and a pinned
`known_hosts` live in `k8s-audit-trail-git`, SOPS-encrypted in `2-gitops/voter-demo/`;
the SSH signing key is `k8s-audit-trail-signing`, deliberately *not*
`generateWhenMissing`, so it survives a namespace rebuild and the public half
registered on GitHub stays the right one.

## Risks, as they now stand

**This repository is NOT a Flux source.** Nothing reconciles from it, so there is no
path from an attendee's commit back into the cluster. The blast radius of a bad commit
is "the repo looks silly". Keep it that way: the moment something reconciles from this
folder, a stranger's typing becomes desired state.

**Attendee-typed names become permanent Git history.** Currently bounded by the
repository being private. If it is ever made public — see the divergence above — this
returns as the headline risk, and the mitigations are: treat the repo as disposable
and force-reset it after the conference, say plainly in the README what it is, and
keep as an emergency lever the ability to drop the `display-name` claim mapping from
the apiserver, which makes every author the opaque `demo:<sub>` at the cost of the
demo's whole point.

**A write credential lives in the cluster.** Bounded by construction: one deploy key,
one repository, one branch allowed, and a sink nothing trusts. Rotation is
`gh repo deploy-key delete` plus a new SOPS Secret.

**The demo depends on github.com from the venue.** If the network is bad, pushes stall
and the commit shows up late or not at all. This is why the receipt reports *saved to
Kubernetes* and *commit requested* as separate facts: the Kubernetes half still works
and the room still sees the menu change live. If that trade feels bad on the day, the
fallback is a second `GitTarget` pointing at an in-cluster Gitea, removing the internet
from the path.

**Volume.** 200 attendees editing one CoffeeConfig is not many commits: the 5s window
coalesces a burst per author and one branch worker serializes pushes. Interleaved
authors split windows, so expect roughly one commit per person per edit burst, not per
keystroke.

**GitHub push protection.** Secret scanning stays on. Nothing scannable should ever be
written, but if a push is ever rejected the `GitTarget` stalls rather than corrupting
anything, and `Ready=False` names the reason.

## Worth building next

`CommitRequest.status` carries `branch` and `sha`. The save receipt could turn those
into a link to the commit, so a participant goes from "I changed a price" to their own
commit without anyone reading a URL off a slide. That is the moment the demo is trying
to sell, and it is a small change to the receipt the app already returns.

Note the repository is private, so such a link only opens for people who can see it.
That is the same decision as the divergence above, arriving a second time.
