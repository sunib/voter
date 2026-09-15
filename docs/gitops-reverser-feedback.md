# Feedback for gitops-reverser

Notes from running `gitops-reverser` against Voter's demo cluster, kept for the
project's maintainers. Same contract as
[krm-stream-feedback.md](krm-stream-feedback.md): each entry says what we hit,
what the controller does today, and whether we think it should change —
including the entries where we concluded it should not.

These entries come from **operating** the controller rather than from consuming
a library, so they are pinned by what the cluster reported — events, conditions
and controller logs, quoted verbatim — and by the source lines that produce them,
rather than by a test in this repository. Where we assert what the code does, the
file and line are given so a maintainer can check the reading.

Against **0.44.0** (`ghcr.io/configbutler/gitops-reverser:0.44.0`) unless an
entry says otherwise.

---

## 1. A routine watch re-establishment is reported as a Warning

**2026-09-15 · suggested change · `StreamReasonWatchError`**

Voter's namespace produces a steady trickle of Warning events, roughly one every
forty minutes, rotating between the three watched CRDs:

```
Warning  WatchError  gittarget/voter-demo   2/3 streams running; 1 blocked (coffeeconfigs.examples.configbutler.ai)
Warning  WatchError  watchrule/voter-demo   2/3 streams running; 1 blocked (quizsessions.examples.configbutler.ai)
```

Over sixteen hours: 22 occurrences for `coffeeconfigs`, 21 for `quizsessions`,
22 for `quizsubmissions`. Nothing is wrong. The controller log shows the whole
life of one:

```
05:45:41  watch.target-watch  target watch session ended; reconnecting
          gvr=...Resource=coffeeconfigs  err="target watch result channel closed"
05:45:41  GitTargetReconciler  streams="2/3"  converged=false  requeueAfter=10s
05:45:43  GitTargetReconciler  streams="3/3"  converged=true   requeueAfter=5m0s
```

Two seconds, self-healing, `writeLost: false` throughout, and `render:
True/RenderMatchesLive` on both sides of it. This is the kube-apiserver closing a
watch on its normal randomized timeout — the thing every watch client is built to
expect — and the controller reconnecting from its cursor exactly as designed.

**What produces it.** `targetWatchReplayAndStream` returns the package's own
sentinel, `errTargetWatchClosed` (`internal/watch/target_watch.go:38`), and the
reconnect loop marks the stream on any non-nil error:

```go
if err != nil {
    m.markTargetStreamState(gitDest, stream.key.Cell(), StreamStateBlocked, StreamReasonWatchError, err.Error())
    log.Info("target watch session ended; reconnecting", ...)
}
```

— `internal/watch/target_watch.go:530`. From there it is mechanical:
`streamReasonIsStalled` counts `WatchError` as stalled
(`internal/controller/stream_status.go:109`), so Ready goes False with reason
`WatchError`, and `recordReadyTransition` emits `EventTypeWarning` for any
persisted Ready transition away from True (`internal/controller/status.go:260`).
Each flap therefore costs a Warning and a Normal event, and drops the GitTarget
to the 10s requeue cadence for one cycle.

**Why we think this one is worth changing.** The doc comment on
`recordReadyTransition` states the intent plainly: *"Events are how a transient
failure that clears before anyone looks stays visible at all."* We think that is
right, and we are not asking for transient failures to be swallowed. The argument
is narrower: an apiserver watch timeout is not a transient *failure*. It is the
protocol working. Reporting it at the same severity as a genuinely blocked stream
means the severity stops carrying information — on this cluster, 100% of the
Warning events in the namespace are this, so a real blocked stream would arrive
as the sixty-fifth identical-looking Warning and nobody would pick it out.

There is also a presentation cost specific to how Voter uses the project: the
cluster is driven live on stage, and any event feed on screen shows a rotating
Warning against a demo that is in fact perfectly healthy.

**The loop already has the shape for the fix.** Three lines above, the same
function special-cases a sentinel by identity rather than by "err != nil":

```go
if errors.Is(err, errGitTargetGone) { ... return }
```

So `errors.Is(err, errTargetWatchClosed)` is available at the same spot, and
would let a clean session end reconnect without passing through
`StreamStateBlocked` at all — no condition flip, no event pair, no cadence
change. Whether the stream should instead report a distinct non-stalled reason
(`StreamReasonReconnecting`, say) so the state stays observable without being
graded a failure, is the maintainers' call; we would be happy with either, and
the distinct reason is probably the more honest of the two since the stream
genuinely is momentarily down.

**What we are not asking for.** Not a change to the reconnect behaviour, the
backoff, or the cursor resume — all three do the right thing and the two-second
recovery is why this is a cosmetic complaint rather than an outage report. Not
suppression of repeat events either; Kubernetes' own aggregation already handles
that, and it is what let us count the occurrences above.

Pinned by the cluster's own record rather than by a Voter test: the event counts
come from `kubectl -n voter get events --field-selector reason=WatchError`, and
the stream state from `kubectl -n voter get watchrule voter-demo -o yaml`, which
reports `streams: {blocked: 0, ready: 3, summary: 3/3}` and
`StreamsRunning/AllStreamsReady` between flaps.

---

## 2. `closeDelaySeconds` defaults to the one value its own documentation warns against

**2026-09-15 · suggested change · `CommitRequest.spec.closeDelaySeconds` · against 0.46.0**

Voter's coffee editor is the project's "save now" path: a participant patches a
`CoffeeConfig` and the backend immediately creates a `CommitRequest` so the edit
reaches Git while the operator is still standing in front of it. It had never
worked, and nothing reported a failure. Every request resolved:

```yaml
status:
  conditions:
  - type: AuthorAttributed
    status: "True"
    reason: AttributedFromAdmission
    message: the submitter was captured at admission and named as the commit author
  - type: Ready
    status: "True"
    reason: NoWindowInGrace
    message: no matching open commit window was collected within the grace; nothing
      was pending to save
```

`Ready=True` and a correct, benign reason. But something *was* pending to save —
it simply had not arrived yet. The controller log, in order, for one save:

```
10:47:32  admission.validate-operator-types  recorded command author at admission
          name=coffee-save-xbdxw  author=demo:CgZzaW1vbjQSCXJvb20tcGFzcw
10:47:32  worker-manager.branch-worker  CommitRequest registered with worker
          request=voter/coffee-save-xbdxw  target=voter/demo2  closeDelaySeconds=0
10:47:32  worker-manager.branch-worker  CommitRequest resolved
          outcome=NoOpenWindow  sha=""
10:47:32  CommitRequestReconciler  CommitRequest finalized
          reason=NoWindowInGrace  age=153.411009ms
10:47:32  worker-manager.branch-worker  Opening commit window        <-- after
          resource=examples.../coffeeconfigs/demo-coffee
10:47:37  worker-manager.branch-worker  Finalizing open commit window
          reason=timer  attachedCR=false  messageOverride=false
10:47:40  git commit created
          message="chore(demo2): 1 change from demo:CgZzaW1vbjQSCXJvb20tcGFzcw"
```

The request was created, attributed, evaluated and finalized — all before the
write it exists to publish reached the worker. The edit still committed, five
seconds later on the window's own timer, under the target's `liveTemplate`
instead of the message the participant typed. From the cluster's point of view
nothing failed; from the stage's, "save now" silently did nothing.

**What produces it.** Voter omitted the field, so it was `0`, and
`finalizeAt` is stamped unconditionally from it:

```go
finalizeAt: time.Now().Add(time.Duration(req.CloseDelaySeconds) * time.Second),
```

— `internal/git/commit_request_attach_loop.go:90`. The deadline is therefore
*now*, and the question is only whether the matching window can already exist.
It structurally cannot, because the two inputs are asymmetric **by design**:

- The request's author comes from the validating webhook, resolved
  synchronously and present-or-never: *"There is no audit wait on this path"*
  (`docs/architecture.md:1215`).
- The write's author comes from the audit fact index, and the watch event is
  held **head-of-line** in the watch goroutine until it arrives or the grace
  expires (`internal/watch/author_resolver.go:21-25`, `:30-37`).

So the request is evaluated on the fast path against a window still travelling
the slow one. On this cluster the slow path cannot beat the API server's own
audit batching, `--audit-webhook-batch-max-wait=1s`. A `0` deadline is not
merely tight; it is shorter than the minimum possible window-open latency.

**Why we think the default should change.** The documentation already contains
the diagnosis:

> `0s` leaves little opportunity to attach, and a request delay does not reserve
> a transaction — `docs/configuration.md:1368`

We agree with every word, which is the point: the field's default is the value
its own reference warns against, for the field's primary use case. Both worked
examples in the project's docs (`configuration.md:1353`, `UPGRADING.md:1742`)
use `closeDelaySeconds: 2`, and none uses `0`. A default of `2s` — or one
derived from `--author-attribution-grace` and the configured audit batching,
which is what actually bounds the wait — would have made our save work
unmodified. We have set `2` on our side either way; the request here is for the
next integrator, who will otherwise ship the same bug and, as we did, not notice.

**A subtlety worth documenting alongside it.** Tuning this field invites an
obvious mistake, and we made it before reading carefully: sizing the delay
against the 3s attribution grace. That is the wrong bound. When the grace
expires with no fact the event still ships, as an *unresolved* — unnamed —
window, and the attach rules then exclude it:

> A request with a named submitter can attach only to that actor's named window.
> — `docs/spec/commitrequest-design.md:23-25`

Every `CommitRequest` created by an authenticated user has a named submitter, so
in the attribution-miss case it can never attach however long it waits; the
honest outcome there is `WindowMismatch`, not a longer deadline. The delay only
ever needs to cover **audit-fact arrival**, not the grace ceiling. Saying so in
the field's reference would stop others from over-sizing it and giving up the
early close they asked for.

**A diagnostic would have saved us a demo.** `NoWindowInGrace` is correct but
not discriminating: "you asked too early" and "there was genuinely nothing
pending" are the same `Ready=True` with the same reason and the same message.
The worker knows the difference — in the trace above it opened a matching window
for the same target microseconds later. An event, a distinct reason, or a metric
label on "a matching window opened within one grace of a request that had just
expired" would turn a silent misconfiguration into something a first-time
integrator can see.

**The shape we would most like, if you want the race gone rather than mitigated.**
The delay is a timer standing in for a happens-before that the API cannot
express: the request names a `GitTarget`, never the write it means to capture.
An optional causal token would close that gap —

```yaml
spec:
  gitTargetRef: {name: demo2}
  after:                        # do not finalize until this has passed the join
    uid: 3f0a…
    resourceVersion: "2577872"
```

— letting the worker wait exactly until that event has cleared attribution
instead of guessing. The caller already holds the value; ours is in the PATCH
response it makes one line earlier. We raise it knowing two things cut against
it: `spec` is immutable, so this has to be a new optional field; and *"the delay
does not reserve a transaction"* reads like a deliberate refusal of
transactional semantics, which this edges toward. Treat it as a suggestion, not
a request — the default change above is the one that matters.

**What we are not asking for.** Not a change to the head-of-line attribution
wait: the strict per-object ordering it buys is worth more than our latency, and
the reasoning in `docs/facts/watch-event-ordering-and-attribution-grace.md`
convinced us. Not removal of the window timer, which is what made this benign
rather than data-losing — the edit did reach Git, correctly attributed, on time
for everything except the message. Not a change to `NoWindowInGrace` being
`Ready=True`; a save with nothing pending is a success, and we would not want it
graded a failure to suit our case.

Pinned by the cluster's own record: the conditions come from `kubectl -n voter
get commitrequest coffee-save-xbdxw -o yaml`, the trace from `kubectl -n
gitops-reverser logs deploy/gitops-reverser`, and the batching from the Talos
`apiServer.extraArgs` this cluster boots with.

---

## 3. `branchWorkerQueueSize` is a constant, and the workload it is sized against is not the one that overruns it

**2026-09-15 · suggested change · `internal/git/branch_worker.go:36` · against 0.46.0**

We load-tested the demo for a conference talk: 200 distinct participants, each
logging in through Dex and creating one `QuizSubmission` with **their own**
token, spread over 60 seconds, against a three-node Talos cluster. Two
GitTargets (`demo1`, `demo2`) share one GitProvider and branch, so they share
one branch worker — which the metric confirms, a single series for both:

```
gitopsreverser_git_queue_depth{branch="main",provider_name="k8s-audit-trail",provider_namespace="voter"} 0
```

The vote burst was comfortable. Peak `git_queue_depth` **44 of 100**, no drops,
`git_commits_total{author_kind="user",message_source="live"}` +400 (two targets ×
200 ballots), fully drained ~12s after the last ballot.

Then we cleaned up — `kubectl delete` of the 203 submissions, batches of 40, no
pacing — and the same queue overran:

```
gitopsreverser_git_queue_drops_total{branch="main",kind="write",provider_name="k8s-audit-trail",provider_namespace="voter"} 20
```

200 ballots over 60s is ~3.3 write requests/s, paced by humans and by an app.
203 deletes × 2 targets, unpaced, is hundreds per second. **The queue is
comfortable with a room and fragile against a script**, and the depth that a
realistic workload reaches tells you very little about the depth an
administrative one will.

**Why we think the constant is the wrong shape, not just the wrong number.**
A slot is a whole `WriteRequest` (`internal/git/types.go:306`), not an event, so
the sizing input is *how many concurrent write requests a bounded burst can
produce* — and that is a deployment property: roughly (concurrent writers) ×
(GitTargets sharing one branch worker). Ours is a room capped at 300 across two
targets, so ~600. A cluster with six targets on one branch has a different
number again. We do not think one compile-time value can be right for both, and
today there is no flag and no Helm value to say so.

There is a second-order version of the same problem: because the worker is keyed
by `(GitProvider namespace, name, branch)`, adding a GitTarget to an existing
branch silently halves the per-target headroom, with nothing to warn the operator
that they have changed a capacity they cannot see.

**The failure is quiet in the place it matters.** The enqueue is non-blocking
with a `default:` that drops the write (`internal/git/branch_worker.go:546-570`).
Convergence is genuinely fine — mark-and-sweep healed ours, the GitTarget
reported `RenderMatchesLive=True`, and the mirrored file was byte-correct
afterwards. But the *live, attributed* commit for those 20 writes never
happened. For a product whose value is "who changed what, as it happened",
eventual state with a missing live commit is precisely the loss a user would
care about, and the only signals are a counter and an `Error` log line.

**What we are asking for**, in order:

1. **Make it configurable** — a flag and a Helm value. This is the part that
   generalises; the default alone does not.
2. **Raise the default**, and say what it is sized for. We intend to run 1000
   locally, on the reasoning that a bounded burst which cannot exceed the queue
   cannot drop at all, whatever its arrival shape.
3. **Document the sizing input and its cost.** Worth stating explicitly that
   `--branch-buffer-max-size` does *not* cover the channel — it bounds the
   window and pending writes *after* dequeue — so queue depth × payload is
   additional memory. Negligible for our ~1–2KB CRDs; not negligible for a
   cluster mirroring large ConfigMaps or Secrets.
4. Secondary: consider surfacing sustained drops on `GitTarget` status, not only
   in metrics. An operator who is not scraping has no way to learn that history
   went missing.

**What we are not asking for.** Not backpressure on the enqueue: a slow or
unreachable Git remote must not stall the watch path, and dropping-then-healing
is the right trade. Not removal of the drop path. Not a larger default *instead*
of configurability — a bigger constant would have fixed our cluster and left the
next one guessing.

Pinned by the controller's own `/metrics` before and after each burst, and by a
harness that drives the real login and ballot path rather than the API directly
(`room-pass/test/loadtest/main.go` in the Voter repository).
