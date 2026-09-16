# Authoring a QuizSession

A reference for writing the rounds the room votes in. Written to be handed to
someone — or something — that has not read the rest of this repository.

A `QuizSession` is one round. It holds a title, a state, and an ordered list of
questions. Participants answer it from their phones, one `QuizSubmission` per
person per round, and the results screen aggregates it live on the projector.

```
apiVersion: examples.configbutler.ai/v1alpha1
kind: QuizSession
namespace: voter            (the demo's namespace; nothing looks elsewhere)
```

The authoritative definitions are [`voter/config/crd/quizsessions.yaml`](../voter/config/crd/quizsessions.yaml)
and [`voter/config/crd/quizsubmissions.yaml`](../voter/config/crd/quizsubmissions.yaml).
Everything below is derived from those two plus the server-side validator in
[`voter/participant_quiz.go`](../voter/participant_quiz.go) and the renderer in
[`frontend/src/components/questions/QuestionRenderer.vue`](../frontend/src/components/questions/QuestionRenderer.vue).
Where this file and a CRD disagree, the CRD is right and this file is stale.

## The shape

| Field            | Required              | Rules                                                                                                                                                                                                                                                        |
| ---------------- | --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `metadata.name`  | yes                   | `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`. **The room page orders rounds by this**, so name them so they sort in the order you want them shown.                                                                                                                      |
| `spec.title`     | yes                   | Non-empty. Shown as the round's heading and on the results screen.                                                                                                                                                                                           |
| `spec.state`     | no, but always set it | `draft` \| `live` \| `closed`. Only `live` accepts votes. A `draft` is filtered out of the room page entirely — invisible, with no button. A `closed` round renders with an **Open** button. Leaving it unset is not the same as `draft`; do not rely on it. |
| `spec.questions` | yes                   | A list of question objects, below. It is a Kubernetes _map list_ keyed by `id`, so ids must be unique within the round, and a server-side apply merges questions by id rather than replacing the list.                                                       |

## The five question types

Every question takes `id`, `type` and `title`. Everything else depends on the
type.

| `type`         | Participant sees                    | Answer field on the submission           | Results screen shows                         |
| -------------- | ----------------------------------- | ---------------------------------------- | -------------------------------------------- |
| `singleChoice` | A column of buttons, one selectable | `singleChoice: "<one choice>"`           | A bar per choice, with counts                |
| `multiChoice`  | Checkboxes, any number selectable   | `multiChoice: ["a", "b"]`                | A bar per choice; each selection counts once |
| `scale0to10`   | A 0–10 slider, unset until dragged  | `number: 7` — **not** a field of its own | The average, to one decimal                  |
| `number`       | A number input, with min/max hints  | `number: 42`                             | The average, to one decimal                  |
| `freeText`     | A textarea, auto-growing            | `freeText: "..."`                        | **Every answer, printed in full**            |

### Per-question fields

| Field         | Applies to                               | Rules                                                                                                                                                                                                                                                    |
| ------------- | ---------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `id`          | all                                      | Required. `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`. **Keep it to 63 characters.** The QuizSession CRD does not cap it but `QuizSubmission.spec.answers[].questionId` does, so a longer id produces a round nobody can answer.                                   |
| `type`        | all                                      | Required. One of the five above.                                                                                                                                                                                                                         |
| `title`       | all                                      | Required, non-empty. This is the question as the room reads it.                                                                                                                                                                                          |
| `required`    | all                                      | Boolean, default false. Enforced at submit time, and the phone shows a REQUIRED tag. A required `freeText` rejects whitespace-only; a required `multiChoice` rejects an empty selection.                                                                 |
| `choices`     | `singleChoice`, `multiChoice`            | **Required for exactly these two and forbidden on the other three** — a CEL rule on the CRD enforces both halves. At least one, each non-empty. Keep each under 256 characters, which is the cap on the submission side.                                 |
| `min`, `max`  | `number`, and mandatory for `scale0to10` | Numbers, `min <= max`. For `scale0to10` the CRD _requires_ `min: 0` and `max: 10` — omitting them is rejected. For `number` both are optional and are enforced server-side on submit. Legal but pointless elsewhere; only the number input renders them. |
| `placeholder` | `freeText`                               | Grey hint text in the textarea. Ignored by every other type.                                                                                                                                                                                             |

## Rules that will get a round rejected

The first four are CEL rules on the CRD, so they fail at `kubectl apply` with a
readable message. The rest fail later, which is worse. Every message quoted here
was produced by a real `--dry-run=server` against this cluster.

1. `choices` must be set on `singleChoice` and `multiChoice`, and unset on
   everything else.

   > `spec.questions[0]: Invalid value: choices must be set for choice questions and unset otherwise`

2. `min <= max` whenever both are given.
3. `scale0to10` must carry `min: 0` and `max: 10`.

   > `spec.questions[0]: Invalid value: scale0to10 requires min=0 and max=10`

4. Question `id`s must be unique within the round.
5. **Quote any choice that YAML reads as a boolean.** This is the trap that
   actually catches people, because the round looks right and the error talks
   about types rather than about the word you wrote:

   > `spec.questions[0].choices[1]: Invalid value: "boolean": ... in body must be of type string: "boolean"`

   Verified as booleans, all of them rejected bare: `Yes`, `No`, `On`, `Off`,
   `True`, `False`, and the single letters `y` and `n` — in any capitalisation.
   Write `- "No"`. The same quoting applies to a choice that looks like a number
   or a date.

6. At most 100 questions — that is the cap on a submission's `answers` list.
7. At most 20 selectable options can actually be _chosen_ in a `multiChoice`
   answer. Offering more than 20 choices is legal; expecting all of them to be
   selected is not.
8. `freeText` answers are capped at 2000 characters.

## Writing questions for a room of 300 phones

Constraints that come from the talk rather than the schema:

- **Two to three questions per round.** People answer on a phone, standing up,
  while a projector waits. Round one in the live demo has two; round two has
  three and that is the ceiling.
- **At most one `freeText`, and put it last.** The results screen prints every
  free-text answer in full, so thirty of them is a wall on the projector and
  three hundred is unreadable. Mark it optional and label it as public — the
  shipped rounds use `placeholder: Optional — your answer will be visible to
the room`.
- **Lead with `singleChoice`.** It is the only type that produces a bar chart
  the back row can read while the answers are still coming in.
- **`scale0to10` reduces to one number.** Good for a temperature check, useless
  for anything you want to discuss.
- **Choices should be short enough not to wrap on a phone**, and mutually
  exclusive enough that the bars mean something. Four is a good number; six is
  the most that stays readable.
- **Write choices that let someone be honestly negative.** A round where every
  option is a flavour of yes produces a chart nobody believes.

## A complete example, using all five types

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: QuizSession
metadata:
  # Sorts by name on the room page. Date-suffixed so a re-run is a new object:
  # reusing a name keeps the old votes, because ballots select by round NAME.
  name: example-round-2026-09-16
  namespace: voter
  annotations:
    # Seeded once, then left alone. Without this, Flux's next reconcile would
    # reset `state` -- closing a round while the room is still voting in it.
    kustomize.toolkit.fluxcd.io/ssa: IfNotPresent
spec:
  title: How do you change Kubernetes configuration today?
  # live accepts votes. closed renders an Open button. draft is invisible.
  state: closed
  questions:
    # 1. singleChoice -- bars on the results screen. Lead with this.
    - id: approach
      title: Which approach do you use most?
      type: singleChoice
      required: true
      choices:
        - GitOps
        - kubectl or a cluster UI
        - A mix of both
        - I am still exploring

    # 2. multiChoice -- also bars; each selection counts once.
    - id: blockers
      title: What gets in the way today? Pick any.
      type: multiChoice
      choices:
        - Waiting for a pull request review
        - Nobody outside the team can change anything
        - No record of who changed what
        - "No" # quoted: bare No is the boolean false

    # 3. scale0to10 -- a slider. min and max are MANDATORY and must be 0 and 10.
    - id: confidence
      title: How confident are you in your current process?
      type: scale0to10
      min: 0
      max: 10

    # 4. number -- a plain input. min and max are optional and enforced.
    - id: teamsize
      title: How many people can change production config in your org?
      type: number
      min: 0
      max: 10000

    # 5. freeText -- printed in full to the whole room. One, last, optional.
    - id: feedback
      title: What would make configuration changes easier for you?
      type: freeText
      placeholder: Optional — your answer will be visible to the room
```

## Check it before you push

The CEL rules run server-side, so a dry run catches everything in the first list
above without creating anything:

```bash
kubectl -n voter apply --dry-run=server -f round.yaml
```

Then confirm what the room will see:

```bash
kubectl -n voter get quizsessions        # STATE and TITLE columns
```

## If you are also generating submissions

Seeded votes — the invented voters in the demo runbook — have to agree with the
round exactly:

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: QuizSubmission
metadata:
  # <round name>-<display name, lowercased>
  name: example-round-2026-09-16-ada-lovelace
  namespace: voter
  labels:
    voter.configbutler.ai/round: example-round-2026-09-16
    # Original casing: this becomes results/Ada-Lovelace.yaml in the mirror.
    voter.configbutler.ai/submitter: Ada-Lovelace
spec:
  sessionRef:
    group: examples.configbutler.ai
    kind: QuizSession
    name: example-round-2026-09-16
  submittedAt: "2026-09-16T10:00:00Z"
  answers:
    # Exactly ONE answer field per entry -- a CEL rule enforces it.
    - questionId: approach
      singleChoice: GitOps # must be one of the round's choices
    - questionId: blockers
      multiChoice: # each must be one of the choices
        - Waiting for a pull request review
    - questionId: confidence
      number: 7 # scale0to10 answers go in `number`
    - questionId: teamsize
      number: 12
    - questionId: feedback
      freeText: I would like the cluster to write this down for me.
```

Three ways this goes wrong: an answer to a `questionId` the round does not
define, two entries for one question, or a `singleChoice` value that is not in
that question's `choices`. All three are refused, and the first two are refused
by the CRD before the app sees them.

The round must be `state: live` for the application's own submit path to accept
an answer. Applying a submission with `kubectl` bypasses that check, because it
is the app that enforces it and you are talking to the API server directly.
