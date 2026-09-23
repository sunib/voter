# Feedback for gitops-reverser

Notes from running `gitops-reverser` against Voter's demo cluster, kept for the
project's maintainers. Same contract as
[krm-stream-feedback.md](krm-stream-feedback.md): each entry says what we hit,
what the controller does today, and whether we think it should change —
including the entries where we concluded it should not.

These entries come from **operating** the controller rather than from consuming
a library, so they are pinned by what the cluster reported — events, conditions
and controller logs — and by the source lines that produce them, rather than by
a test in this repository.

Against **0.49.1** (`ghcr.io/configbutler/gitops-reverser:0.49.1`), which the
demo cluster now runs. Four earlier entries have been resolved and dropped from
this page rather than kept as history: a routine watch reconnect graded as a
Warning, `closeDelaySeconds` defaulting to `0` and a compile-time branch worker
queue, in 0.47.0 by `d0601a59`, `46bbf3c5` and `c6127229`; and **a live commit
message that could not name the resourceVersion it wrote, in 0.48.0 by
`d814ecfe`**. That last one was this page's item 3, it is what we most wanted,
and it is now on every body line these four targets commit.

What remains is two items, neither urgent, plus one narrow residue of item 3
that the fix did not close. The second item is a suggestion we expect may be
declined.

---

## 1. `NoWindowInGrace` still does not distinguish "too early" from "nothing pending"

**2026-09-15 · suggested change · `CommitRequest` terminal reasons**

Defaulting `closeDelaySeconds` to `2` removed the acute version of this: a save
that omits the field now clears the reference audit configuration with headroom,
and ours works unmodified. What it did not change is what a save looks like when
the deadline *is* missed — on a loaded or distant cluster, where
`docs/configuration.md:1671`
itself advises `4` to `5`, or on any integration that sets `0` deliberately.

In that case the request resolves:

```yaml
status:
  conditions:
  - type: Ready
    status: "True"
    reason: NoWindowInGrace
    message: no matching open commit window was collected within the grace; nothing
      was pending to save
```

"You asked too early" and "there was genuinely nothing pending" are the same
condition, the same reason, and the same message. The edit still reaches Git
seconds later under the target's `liveTemplate`, so from the cluster's point of
view nothing failed; from the caller's, a save button did nothing and said so in
words that mean success.

The worker knows the difference. In the trace we originally reported, it opened
a matching window for the same target microseconds after resolving
`NoOpenWindow`. An event, a distinct reason, or a metric label on "a matching
window opened within one grace of a request that had just expired" turns a
silent misconfiguration into something a first-time integrator can see.

This is already scoped as **Phase 4** of
`docs/design/commitrequest-save-wait-options.md:439`,
at a day's work, and that page's own framing of it matches ours. We are
recording that we still want it, not proposing a design. We would rank it below
anything else on the maintainers' list: with the default repaired, this is a
diagnostic for a case most integrators will no longer reach.

**What we are not asking for.** Not a change to `NoWindowInGrace` being
`Ready=True` — a save with nothing pending is a success, and we would not want
it graded a failure to suit our case. Not a retroactive change to the terminal
reason, which is already written by the time the worker learns the answer; a log
line, a counter, or a follow-up Event is enough.

---

## 2. A dropped write is only ever visible to something that scrapes

**2026-09-15 · suggestion, and we think it may be rightly declined ·
`git_queue_drops_total`**

Making `branchWorkerQueueDepth` configurable and raising the default to 1000
fixed our overrun, and the sizing guidance at
`docs/interpreting-metrics.md:212`
says the part we most wanted said — that the depth a realistic workload reaches
tells you nothing about the depth an administrative one will. We run the new
default and no longer set the knob.

The residual is narrow and unchanged by that work: when the queue *does* drop a
write, the loss appears in a counter and an `Error` log line and nowhere else.
Convergence heals the mirror, the target goes on reporting
`RenderMatchesLive=True`, and the live attributed commit for that write never
happens. For a product whose value is "who changed what, as it happened", that
is the loss a user cares about, and it is the one the end state hides. A larger
queue lowers the odds; it does not bound an unpaced `kubectl delete`.

**We asked for this on `GitTarget` status, and that was refused for a good
reason.**
`docs/design/gittarget-red-status-plan.md:162`
declines it outright — *"Those paths are shared across targets or metrics-only
today; forcing them onto one target would be a diagnosis lie"* — because a
branch worker is keyed by `(GitProvider namespace, name, branch)` and serves
every target pointed at it. We agree, and we would not want the lie.

**The narrower shape we think survives that objection** is to report it on the
`GitProvider` instead. That object already carries `status.conditions`, and it
is the honest owner of the thing that dropped the write: attributing a shared
worker's saturation to the shared object is simply true, where attributing it to
one of its targets is not. It is coarser than what we originally asked for —
one GitProvider may serve several branches — but coarse is not the same as
wrong, and the plan's stated blocker is owner identity, which the GitProvider
supplies today without waiting for durable queue work.

We raise it knowing our own case for it is weak. Voter scrapes
(`monitoring.serviceMonitor.enabled: true` against kube-prometheus-stack), so we
*can* see this; the alert expression is in
`docs/design/metrics-observability-plan.md` and the operator guidance is already
written. Our real complaint is narrower than "we cannot see it": nobody watches
Grafana while a cluster is being driven live on stage, and `kubectl get
gitprovider` is where we look when something is off. That is a presentation
concern specific to how Voter uses the project, and it is not a good enough
reason on its own to widen a status surface the maintainers have deliberately
kept narrow.

**What we are not asking for.** Not backpressure on the enqueue: a slow or
unreachable Git remote must not stall the watch path, and dropping-then-healing
is the right trade. Not removal of the drop path. Not the `GitTarget` surfacing
we originally proposed — that one we withdraw.

---

## 3. `liveTemplate`'s field list is still not the field list

**2026-09-23 · documentation · `spec.commit.message.liveTemplate` CRD description**

This is what is left of the item 0.48.0 closed, and it is the cheap half of it.

The feature we asked for shipped and we are running it. Every live commit these
four targets make now ends its body lines in the version that commit wrote:

```text
chore(config): 4 ConfigButler changes

- [UPDATE] GitTarget voter/demo-c@6210582
- [UPDATE] GitTarget voter/demo1@6210583
- [UPDATE] GitTarget voter/demo2@6210586
- [UPDATE] GitTarget voter/gitops-reverser-config@6210589
```

The residue is the sub-point we filed alongside the request, which the rewrite
did not clear. `liveTemplate`'s description now names `ResourceVersion` and
`Generation`, and it still says `Resources` exposes "Operation, Group, Version,
Resource, Namespace, Name, APIVersion" — with no `Kind` and no `Labels`. Both
exist, both work, and both are load-bearing here: `demo-c` and the config target
render `{{.Kind}}` on every body line, and `demo2` reads a round name out of the
labels. So the list was edited without being corrected, which is the failure
mode that makes a list worse than no list: a reader who checks it concludes a
field is absent and goes to `internal/` to find out, exactly as we did.

The fix is one sentence in `api/v1alpha3/gitprovider_types.go`, and we would put
it below anything on this page that involves behaviour.

**What we are no longer asking for.** The other sub-point — that nothing catches
a template typo before commit time — we withdraw as filed. `ValidateCommitConfig`
dry-renders all three templates against sample windows, and 0.48.0 widened those
samples to carry labels and a kind, so an unknown field is refused when the
`GitTarget` is validated rather than months later mid-window. We proved this the
direct way before deploying this upgrade: we ran our four templates through that
function against 0.49.1's own structs, with the retired `{{.Revision}}` spelling
as a negative control, and it refused the control with a sentence naming both
spellings. It is still not *admission* — `kubectl apply --dry-run=server` accepts
a bad template — and we would still prefer it there. But "the failure arrives at
commit time" is not what the code does, and we should not have written it.

---

## What we are tracking rather than asking for

Phases 1 to 3 of
`docs/design/commitrequest-save-wait-options.md`
— carrying write identity on the event, and the named-write `waitFor` that
replaces the timer with a happens-before. The causal token we sketched in the
original version of this page became that plan, in a better form than we
proposed, and there is nothing useful we can add to it from here. `2` works for
us; we are not blocked on the race being removed rather than mitigated, and we
would rather the maintainers sequence those phases against other consumers than
against us.
