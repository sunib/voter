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
