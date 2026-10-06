# Design: the round's result is its status

Status: **built.** Written for review before implementation, and kept as the
argument for why it is shaped this way. What shipped:
[`voter/quiz_tally.go`](../voter/quiz_tally.go) (one counter, two readers),
[`voter/quiz_reconciler.go`](../voter/quiz_reconciler.go) (the controller),
the status subresource and `VOTES` column in
[`voter/config/crd/quizsessions.yaml`](../voter/config/crd/quizsessions.yaml),
and both screens.

**Since 2.0.0 (2026-10-06)** the status is the *only* source. Voter's REST
results endpoint, and the screens' fallback to it, went with the move to
krm-foyer; the round arrives on krm-foyer's `/stream/v1` shared watch, and
participants can no longer list submissions at all. Where this document says
"REST", read it as the design's history, not the running system.

Two asks: the results screen should update itself, and the quizzes page should
show how many answers have been filed, also updating itself.

The first draft of this document streamed `quizsubmissions` to every phone and
tallied them in the browser. **That is not the design any more.** Simon's
proposal — put the result in `QuizSession.status`, let Voter reconcile it, and
let everyone read it — is better on every axis the first one was weakest on, and
this document is the case for it.

---

## Why it is better

The browser-tally design had three problems. This one does not have them.

|                             | Stream submissions, tally in the browser                    | **Result in status**                                              |
| --------------------------- | ----------------------------------------------------------- | ----------------------------------------------------------------- |
| Per-phone payload           | 600 objects, ~0.6–1.2 MB at 300 participants                | one QuizSession, a few KB                                         |
| Where the tally is computed | Twice — Go for REST, a TypeScript mirror for the stream     | Once, in Go                                                       |
| New stream scope            | `quizsubmissions` streamed to every phone                   | none — `quizsessions` is already streamed                         |
| New participant RBAC        | none then (they could list submissions; since 2.0.0 not)    | none                                                              |
| What lands in every browser | every answer, with the `submitter` label naming who gave it | an aggregate, no names                                            |
| Counter on the quizzes page | a second stream per idle phone                              | a field on objects that page **already streams**                  |

The last row is the one that decides it. `HomeScreen.vue` opens a `quizsessions`
stream today. A count in status arrives on that stream for free: no new
subscription, no new request, no new endpoint. The feature is then a template
change.

And the duplicated counter disappears. The tally skips submissions that fail
`validateQuizAnswers` ([`quiz_rules.go`](../voter/quiz_rules.go)), so a browser
that counted objects would have disagreed with it on a projector. With one Go
implementation writing status, every screen agrees by construction. (The phone
keeps a copy of the answer checks in
[`ballot.ts`](../frontend/src/api/ballot.ts) to say "answer required" before
sending; it never counts anything.)

## The fact this rests on: status never reaches Git

`quizsessions` is mirrored to `demo1/quiz-configs.yaml`, and the close depends on
that file showing one clean `state: closed → live` diff. A status that changes
on every vote would be a catastrophe there — unless it never arrives.

It never arrives. The reverser's sanitizer **rebuilds** each object from an
allowlist rather than deleting fields from it: `setCoreIdentityFields` plus
`spec` plus `preservedTopLevelFields`
([`internal/sanitize/sanitize.go:36–72`](../external/gitops-reverser/internal/sanitize/sanitize.go)).
`status` is not on that list, for any kind, and a test asserts its absence.
`03e2bbb` already said so in passing; this confirms it in the source.

So `quiz-configs.yaml` does not move. What a status write _does_ produce is a
watch event the reverser wakes for and then discards, because the sanitized
document is identical to what is already in Git. Cheap, but not free — worth
confirming in the fixture that a burst of votes produces no commits on that
file.

## What this reverses, deliberately

`03e2bbb` removed status from these CRDs three weeks ago, and it was right to:
the contract existed "for a controller that does not exist", so the block was
never written and `kubectl get qs` printed an always-empty column. **This builds
that controller**, which is the premise that was missing. The commit also notes
removing the subresource was free because nothing read `metadata.generation` on
a quiz object — re-adding it is free for the same reason, and now something
does read it.

---

## What gets built

### 1. CRD: a status subresource that says what was counted

```yaml
status:
  # Which spec this tally describes. With the status subresource restored,
  # metadata.generation moves only when the QUESTIONS change -- so a tally whose
  # observedGeneration lags is one computed against questions that have since
  # been edited, which the runbook tells you not to do mid-round.
  observedGeneration: 3
  # When the controller last recomputed. Rendered on screen: a controller that
  # has stopped looks exactly like a room that has stopped voting, and the only
  # thing that tells them apart is a timestamp.
  lastTallyTime: "2026-09-17T13:22:41Z"
  # Objects carrying this round's label, and how many of them counted. They
  # differ when a ballot fails validation -- a hand-written kubectl one with a
  # stale question id, which is exactly what "Casting your own answers" risks.
  # Showing both makes that visible instead of silently dropping a vote.
  filed: 187
  counted: 186
  questions:
    - id: approach
      count: 186
      choices: # singleChoice and multiChoice
        A pull request to a GitOps repo: 141
        kubectl apply: 120
      sum: 0 # number and scale0to10
      textTotal: 0
      text: []
    - id: wish
      count: 143
      choices: {}
      sum: 0
      textTotal: 143 # how many were written
      text: ["...", "..."] # a BOUNDED sample -- see below
```

Plus `additionalPrinterColumns` for `VOTES` (`.status.counted`), so
`kubectl get quizsessions` answers the question from the terminal. That is the
same move as `kubectl get room` telling you the join code (`416d6c9`), and it is
a demo beat: the result of the vote is a field on the object, next to the
questions that produced it.

**Free text is bounded and the rest is not.** Counts stay exact however many
people vote, but text cannot: 300 answers of up to 2000 characters is ~600 KB in
an object whose etcd ceiling is about 1.5 MB, rewritten on every tally. Status
keeps the most recent **25** answers plus `textTotal`; the full list is in the
submissions themselves, for someone holding `list` — the operator's harvest
command in the runbook reads it there. This is not only a
size compromise — the runbook already says 300 free-text answers on a projector
is a wall, and the presenter reads out two or three.

### 2. Voter gains a reconciler

A controller in the Voter process: watch `quizsubmissions`, recompute the
affected round, patch its status.

**It watches the API, not the app's write path — so every writer counts.** This
is the property to be deliberate about rather than to get by accident. A ballot
pasted with `kubectl`, applied by Flux, or created by anything else holding a
token produces a watch event exactly like one typed on a phone, and the
projected number moves without the application having been involved at all.

That makes "Casting your own answers" a better beat than it is today. You paste
three invented voters from the terminal, the room watches the bars move on the
projector, and nothing in the app knew it happened — which is the talk's own
argument, performed rather than asserted. The reverser already commits those
ballots in your name, so the same keystroke produces a vote, a commit and a
moving bar.

The watch is the mechanism; the resync below is only a backstop for a missed
event. It is not a poll wearing a watch's clothes.

- **Coalesced.** A 300-vote burst must not be 300 status writes. Recompute per
  round on a short timer (~1s) after the first event in a window.
- **Resync.** List-and-recompute at startup and every few minutes, so a missed
  event or a deleted ballot self-heals. The reset between runs deletes every
  submission; the tally has to follow it back to zero.
- **One replica.** No leader election, because the deployment is one replica and
  the runbook already calls that load-bearing. Two replicas would both patch;
  harmless (same value) but wasteful, and worth a comment rather than a lock.
- It reuses `validateQuizAnswers` and `quizResult.add` unchanged.

### 3. RBAC

Participants need **nothing new**. They already have `get, list, watch` on
`quizsessions`, and status arrives inside the object they can already read —
since 2.0.0 over krm-foyer's shared watch, which checks each subscriber's own
RBAC before disclosing it. That
is the part of this design that is nicest to explain on stage.

The Voter service account needs two additions in
[`app.yaml`](../external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/app.yaml):

```yaml
- apiGroups: [examples.configbutler.ai]
  resources: [quizsubmissions]
  verbs: [list, watch] # to tally
- apiGroups: [examples.configbutler.ai]
  resources: [quizsessions/status]
  verbs: [get, patch] # to write the tally, and NOT the questions
```

The subresource split is worth having for its own sake: the controller can write
the result without being able to edit the round.

### 4. The two screens

**Results** — render `round.status` from the `quizsessions` stream it opens
anyway. As built, the REST call stayed for the first paint and as the fallback;
since 2.0.0 there is none, and a round with no tally yet says it is waiting
rather than showing zeroes over real votes. Just after a vote the page says
"Counting your vote…" for the second the tally takes. Show `lastTallyTime` as
"as of 13:22:41" — a stale tally must not look live.

**Quizzes page** — each round card shows `status.counted` off the stream it
already has. No new subscription, no new request.

## What selects a ballot: `spec.sessionRef`

A submission counts for the round its own `spec.sessionRef.name` points at.
Nothing else. `sessionRef` is required by the CRD, it is a typed reference, and
it is the object's own statement of what it is a vote in.

When this was written the results endpoint selected on the
`voter.configbutler.ai/round` label instead, which was the one thing standing
between a hand-written ballot and the projector: the app always set that label,
but **the CRD does not require it**, so a pasted ballot that omits it is accepted
by the API server, committed to Git, and counted by nobody. `demo1-b.yaml`'s
header has been warning about exactly this: _"a label mismatch is silent."_

Selecting on `sessionRef` deletes that failure mode rather than reporting it.
There is no mismatch to report, because there is only one thing being read.

The label stays — a participant's ballot must carry it (the `voter-ballot`
admission policy checks it), and it is still what demo2's commit subject reads. It stops being load
bearing, which is what it should never have been.

Two consequences, both small:

- **The tally filters in Go** instead of passing a label selector. At two
  rounds and 300 participants that is ~600 objects, which the controller holds
  in cache regardless.
- **The reset deletes all of them**, because a ballot with no label is now
  countable and a label selector would leave it behind for the next room:

  ```bash
  kubectl -n voter delete quizsubmissions --all
  ```

  That is simpler than the two label-scoped deletes it replaces. Those existed
  when rounds were dated and several could coexist; there are two rounds now and
  they reset together.

## What is honestly worse

The number on screen now depends on a controller being alive, where before it
was computed on demand and was always fresh. A crashed reconciler shows a confident,
wrong, unchanging number.

That is why `lastTallyTime` is in the status and on the screen, and why the
resync loop exists. It is a real regression in failure mode, traded for
everything in the table at the top, and the mitigation is to make staleness
visible rather than to pretend it cannot happen.

## Rollout order

1. **CRD** — status subresource and printer column. Both repos; they are
   mirrored byte for byte and `03e2bbb` put the canonical copy in
   `voter/config/crd/`.
2. **RBAC** — the two rules above, via Flux.
3. **Voter image** — controller and both screens.

Steps 1 and 2 are inert without 3, and 3 degraded to the old behaviour without
them, because both screens kept the REST fallback. There was no ordering that
broke the demo. Since 2.0.0 there is no fallback, so the CRD and RBAC are
prerequisites of any Voter image, not options.

## Tests

All in [`voter/quiz_tally_test.go`](../voter/quiz_tally_test.go),
[`frontend/src/api/quizResults.test.ts`](../frontend/src/api/quizResults.test.ts)
and [`test/browser/voting.spec.js`](../test/browser/voting.spec.js).

- **Go, controller** — `TestTallyRound`: correct tally, an invalid ballot counted
  in `filed` and not in `counted`, a ballot carrying no round label at all
  counted normally, another round's ballot carrying THIS round's label not
  counted, deletion drops the count, text bounded at 25 with `textTotal` exact.
- **Go, any writer** — `TestAnyWriterMovesTheTally`: a submission created
  straight against the client, with no HTTP handler involved, moves the tally,
  and deleting it moves it back. That is the kubectl path, and it is the one
  property a test can pin that a demo cannot repeat on request.
- **Go, pins** — `TestTallyChecksPins`: a ballot pinned to another round's UID
  or questions is filed and not counted. (`TestStatusAndRESTAgree` went with the
  REST handler in 2.0.0.)
- **Browser** — Playwright: results open in one context, a vote cast in a
  second, the count increments **with no reload**; then a ballot written with
  `kubectl` moves it again on a page nobody touched. That is the feature.
- **Fixture** — not written. `status` never reaching Git is already asserted
  upstream, in gitops-reverser's own `internal/sanitize` tests, which is where
  the guarantee lives; a second fixture here would test their code from a
  distance.

## Follow-on: done with it

[`demo-runbook.md`](demo-runbook.md) told the presenter to press "Refresh
results" in demo 1 step 2, again through the five-minute Q&A, and in a
failure-mode row. All three are gone; the close now says the average moves while
the room watches it, and the failure-mode row asks whether the "as of" timestamp
is stuck instead.

"Casting your own answers" points at the projector rather than at the terminal.
The reset commands are `--all`, and the header warning in
[`demo1-b.yaml`](../voter/config/demo1-b.yaml), entry 1 of
[`deliberate-simplifications.md`](deliberate-simplifications.md) and
[`voting-demo.md`](voting-demo.md) all say what the label does now, which is
decide where a document is filed and nothing else.

## Not in scope

- No change to the results REST endpoint. (It stayed as the fallback until 2.0.0
  removed it.)
- No streaming of `quizsubmissions` to participants, and no new stream scope.
- No leader election.
