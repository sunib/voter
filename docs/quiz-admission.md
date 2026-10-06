# Plan: ballot rules in admission, ahead of krm-foyer

Status: **phase 1 merged (#21, `be6a268`, 2026-10-06), phase 2 not started.** Preparation for step 4 of
[krm-foyer-migration.md](krm-foyer-migration.md) that does not wait for krm-foyer's
release. Every phase below ships on Voter 1.x and is useful on its own.

## Why now

Today's vote handler already creates the QuizSubmission **with the participant's
token** (`participant_quiz.go:146-148` says so, and that anything with direct API
access bypasses its rules). So a policy on `quizsubmissions` create protects the
current deployment as well as the krm-foyer one. Nothing in it depends on krm-foyer.

## The idea: a ValidatingAdmissionPolicy that can read the round

Not a webhook. A ValidatingAdmissionPolicy runs CEL inside the API server, with no
service, certificate or availability risk of our own. Both clusters support it: the
cluster runs 1.37 and the e2e fixture runs k3s 1.31.

The migration plan assumed admission cannot see the round, and so moved "is it live,
are these its questions" into the reconciler as "not counted". That is not quite true.
A policy can take **`paramKind: QuizSession`** with a binding whose `paramRef.selector`
matches every round in `voter`. The API server then evaluates the policy once per
round, and denies the request if any evaluation fails. Each rule is written as
"not this ballot's round, or the ballot is valid for it":

```cel
params.metadata.name != object.spec.sessionRef.name ||
  (params.spec.state == 'live'
   && object.spec.roundUID == params.metadata.uid
   && object.spec.questionsDigest == params.status.questionsDigest)
```

`parameterNotFoundAction: Deny`, so with no rounds there are no ballots. Each rule gets
a `messageExpression`. That returns what decision 1 gave up: **a synchronous refusal
the browser can show** ("this round is closed", "the questions changed, reload"), in
place of a silent "not counted".

The rules, in the order the migration plan lists them:

| Rule | Needs the round |
| --- | --- |
| A `demo:` user, or a ballot that declares itself an operator's (below) | no |
| Name is `<sessionRef.name>-<lowerAscii(display-name extra)>`; extra present | no |
| Labels `voter.configbutler.ai/round` and `/submitter` match `spec` and the extra | no |
| The round is `live` | yes |
| `roundUID` and `questionsDigest` match | yes |
| Answers pass `validateQuizAnswers` | yes, phase 3 |

The display-name extra is `configbutler.ai/claims/display-name`
(`1-talos/files/authentication-config.yaml`).

**The operator's path.** A flat "only `demo:` users" rule would refuse the "Casting
your own answers" interlude, where the operator applies Ada Lovelace's and Grace
Hopper's ballots with kubectl. `talk-checklist.md` already names that trap and the
better version: the operator may cast a ballot, but it has to say so. A non-`demo:`
user's ballot must carry a label such as `voter.configbutler.ai/cast-by: operator`,
and the name and label rules above apply only to `demo:` users. The round rules apply
to everyone, so a pasted ballot also needs a live round. `voter/config/demo1-b.yaml`
and the runbook snippet get the label. The line for the talk: "I can still stuff the
ballot box, I just cannot do it quietly." 

## Does it make counting cheaper?

**Barely, and that is not the reason to do it.** The tally already runs from the
informer cache in memory (`quiz_reconciler.go:209-258`): one pass over the cached
ballots per reconcile. At 200 ballots that costs microseconds. Admission makes no
measurable difference to it.

What admission changes is **what the tally has to trust**. A ballot that got stored
was valid against the live round at that moment, so:

- the live window is enforced when a ballot is created, which a tally can never do
  reliably. Voter stamps `openedAt` and `closedAt` when it observes a change, so a
  round that changes state while Voter is restarting is stamped late. They stay
  status for people to read. The policy reads rounds from an informer, so a ballot
  can still land milliseconds after a close; that edge is accepted;
- re-running `validateQuizAnswers` in the tally stays cheap, and stays as the belt.

The real gains are: refusals at the moment of voting, today's bypass closed, and the
reconciler's rules no longer the only thing between a script and the projector.

## Phases

Each phase is one PR and one release. The order matters: **the policy must not reach
the cluster before a Voter that fills the new fields**, or every vote is refused.

### 1. Fields and digest (`feat:`, 1.x, deploys itself)

- [x] CRD: `spec.roundUID`, `spec.questionsDigest` on QuizSubmission (optional, so
      1.2.0's ballots stay valid); `status.openedAt`, `status.closedAt`,
      `status.questionsDigest` on QuizSession. Lower `answers` `maxItems` from 100 to
      25, the questions' bound, so CEL cost estimates fit in phase 3.
- [x] The reconciler writes `status.questionsDigest` (sha256 over the decoded
      `spec.questions`, re-marshalled by Go). It writes `openedAt` on the first
      `live` and `closedAt` on each close, clearing it on reopen.
- [x] The vote handler fills `roundUID` and `questionsDigest` from the round it already
      loads. The browser does not change yet.
- [x] The tally checks each pin that is present. Ballots without pins (the pasted
      interlude) are counted as today. `openedAt`/`closedAt` are not a counting rule.
- [x] `sameTally` treats a null status field as absent, or a closed round's
      `closedAt: null` would make every resync rewrite it.
- [x] Unit tests (`quiz_tally_test.go`, `participant_quiz_test.go`): digest stable
      across a state change, changed questions change it; recreated-round,
      changed-questions and unpinned ballots; `roundTimes` through open, close and
      reopen; a pinned ballot still counting after its round closes.
- [x] Copy the CRDs to `external/k8s` `voter-demo/crds` in the same change set as
      the release (image automation bumps only the image).

### 2. The policy, identity and round rules (`feat:`)

- [ ] `voter/config/admission/quizsubmission-policy.yaml`: the policy and binding,
      next to the CRDs. `failurePolicy: Fail`, `validationActions: [Deny]`.
- [ ] `test/e2e/up.sh` applies it after the CRDs. The cluster copy goes to
      `external/k8s` `voter-demo/`, pulled first, after phase 1 is deployed.
- [ ] Before `Deny`, run it once on the cluster with `validationActions: [Audit]`
      and read the audit log during a test round.
- [ ] The operator's declared path: `voter.configbutler.ai/cast-by: operator` on
      `voter/config/demo1-b.yaml` and the runbook's interlude snippet, in the same
      PR as the policy.
- [ ] The handler maps a policy refusal (403 `Forbidden`, reason in the message) to
      the message shown on the phone.

### 3. Answers in CEL (optional)

- [ ] Port `validateQuizAnswers` to CEL against `params.spec.questions`: known and
      unique question ids, exactly one answer field, choices in the list, number
      bounds, required questions answered. Check the cost estimate with the phase-1
      bounds. If it does not fit, stop here: the tally's Go check covers it.

## Tests for the policy

A policy only means something against a real API server, and `user.extra` is the
hard part to fake. Two layers:

- **envtest** (`voter/admission_test.go`, build tag `admission`): starts a real
  kube-apiserver and etcd, applies the CRDs and the policy, and creates ballots
  **as impersonated users** with `Impersonate-Extra-configbutler.ai/claims/display-name`.
  It is fast and needs no k3d, so it can run in CI on every push. Cases:
  - accepted: a `demo:` user's first ballot on a live round;
  - refused, each with its message: another participant's name, a GitHub user, a
    missing extra, a second ballot (409 from the name), a `ready` round, a closed
    round, a stale UID, a stale digest, and wrong labels;
  - the operator: refused without the `cast-by` label, accepted with it on a live
    round, refused with it on a closed one;
  - a round deleted and recreated under the same name.
- **e2e**: one Playwright case already votes. Add a vote after close that is refused
  with the policy's message on screen, to show the refusal reaches the phone.

Add `setup-envtest` to `.github/workflows` for the version the cluster runs.

## Effect on the migration plan

- Decision 1 keeps its shape. The participant still creates the ballot through `/k8s`,
  but most refusals become synchronous again.
- Step 4's admission bullet becomes "done in phase 2". Its reconciler bullet shrinks to
  the defence-in-depth checks above.
- If the vote screen can accept learning "already voted" only from the 409 on create,
  participants can lose `get` on ballots. That closes the gap where anyone can read
  another participant's ballot by name. The cost is that a reloaded phone shows the
  form again until it tries to vote.
