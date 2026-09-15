# Reverse-GitOps for demo.koudijs.dev: upgrade and configuration plan

Three folders in `ConfigButler/k8s-audit-trail`, one per thing being shown:

```
clusters/k8s.koudijs.dev/
├── demo1/                        ← GitTarget "demo1"
│   ├── quiz-configs.yaml             both rounds, one file
│   └── submissions.yaml              every vote, one file
├── demo2/                        ← GitTarget "demo2"
│   ├── coffee-config.yaml            the menu the room edits
│   └── results/
│       ├── Ada-Lovelace.yaml          one file per person, every round
│       └── Grace-Hopper.yaml          they voted in, appended
└── gitops-reverser-config/       ← GitTarget "gitops-reverser-config"
    ├── voter/gittargets/…            the config that produced the two above
    ├── voter/secrets/*.sops.yaml     encrypted
    └── _cluster/clusterproviders/default.yaml
```

The contrast between `demo1/submissions.yaml` and `demo2/results/` is the point:
**the same QuizSubmission objects, filed two ways, and the difference is one
line of placement config.** demo1 puts every vote ever cast in one file; demo2
gives each person a file, which their second round's vote appends to. Same
objects, read through a label. That needs the `{label:key}` variable, which
ships in 0.46.0 — hence the upgrade first.

This plan replaces the single `voter-demo` target. Because `spec.path` is
immutable, that means delete-and-recreate; you have said resetting the trail
repo is fine, so [Cutting over](#cutting-over) does exactly that.

Everything goes through Flux. Nothing here is `kubectl apply`.

---

## Part 1: upgrade 0.44.0 → 0.46.0

| Release | Breaking | Affects us |
|---|---|---|
| 0.45.0 | — | no (metrics only) |
| 0.45.1 | — | no (scoped-path refusal reporting) |
| 0.46.0 | `{namespaceOrCluster}` removed | **no** — nothing uses it |

`{namespace}` now renders the literal `_cluster` for a cluster-scoped resource
instead of rendering empty, and `{namespaceOrCluster}` is gone. The current
[git-sink.yaml](external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/git-sink.yaml)
never named it, so the rename costs nothing — and Part 2 turns it into a feature:
the cluster-scoped `ClusterProvider` lands at
`_cluster/clusterproviders/default.yaml`, which is legible only because of it.

0.46.0 also refuses any `{…}` naming no variable, and any brace that never
closes, at the `Validated` gate rather than writing it into the repo as literal
text. Our templates contain no stray braces. Commit templates additionally gain
`.Kind`, `.Label`/`.Labels`, `.LabelValue`/`.LabelValues` — all additive, and
Part 2 uses them.

### Steps

1. **Resolve the 0.46.0 chart digest** — `release.yaml` pins by digest, so the
   tag alone is not enough:

   ```console
   crane digest ghcr.io/configbutler/charts/gitops-reverser:0.46.0
   ```

2. **Edit** `2-gitops/gitops-reverser/release.yaml`, replacing `ref.digest`. Add
   `ref.tag: 0.46.0` beside it if you want the version legible; the digest still
   wins.

3. **Commit and push.** `upgrade.crds: CreateReplace` is already set, so the
   GitTarget CRD picks up the `{label:key}` contract with no manual CRD step.

4. **Verify before touching any config:**

   ```console
   kubectl -n gitops-reverser get pods
   kubectl -n voter get gittarget voter-demo -o wide
   ```

   Still `Validated=True`, `Ready=True`, `3/3` streams. If `Validated` went
   `False` the message names the offending placeholder.

No RBAC change needed: the chart's `rbac.watchTypes.mode` defaults to `any`
(wildcard `get/list/watch`) and `release.yaml` does not override it, so the
operator can already read Secrets and `configbutler.ai` resources.

---

## Part 2: two rounds

`demo1/quiz-configs.yaml` is only interesting with more than one document in it,
and demo2 needs a round to open. So the second round is created up front,
**closed**, and opened live during demo2.

This matters for how you write it: the room UI
([RoomScreen.vue:73](frontend/src/screens/RoomScreen.vue#L73)) **filters out
`state: draft` entirely**, while a `closed` round renders with an **"Open"**
button. So:

- `state: closed` → visible in the room, opened in demo2 with one click, and the
  state flip is itself mirrored — `demo1/quiz-configs.yaml` changes on screen,
  authored by whoever clicked.
- `state: draft` → invisible until something sets it live, and the `/state`
  endpoint refuses `draft` as a target on purpose
  ([participant_quiz.go:251](voter/participant_quiz.go#L251)), so you would open
  it with a **Git commit** and a Flux reconcile instead.

Use `closed`. The click is the tighter beat, and the draft variant is there if
you would rather open round two by commit (a nice forward-GitOps bookend, but it
costs a reconcile wait on stage).

Rounds sort by `metadata.name`
([RoomScreen.vue:73](frontend/src/screens/RoomScreen.vue#L73)), so the names
below also fix their display order.

Replace `demo-round.yaml` with two documents:

```yaml
# Round one: voted on in demo1. Live from the start.
apiVersion: examples.configbutler.ai/v1alpha1
kind: QuizSession
metadata:
  name: demo1-round-2026-09-15
  namespace: voter
spec:
  title: How do you change Kubernetes configuration today?
  state: live
  questions:
    - id: approach
      title: Which approach do you use most?
      type: singleChoice
      required: true
      choices:
        - GitOps
        - kubectl or a cluster UI
        - A mix of both
        - I am still exploring
    - id: feedback
      title: What would make configuration changes easier for you?
      type: freeText
      placeholder: Optional — your answer will be visible to the room
---
# Round two: created closed, opened from the room during demo2. It is here from
# the start so demo1's quiz-configs.yaml already holds two documents.
apiVersion: examples.configbutler.ai/v1alpha1
kind: QuizSession
metadata:
  name: demo2-round-2026-09-15
  namespace: voter
spec:
  title: Now that you have seen it — would you run this?
  state: closed
  questions:
    - id: trust
      title: Would you let a cluster write to your Git repository?
      type: singleChoice
      required: true
      choices:
        - Yes, with signed commits and a repo nothing reconciles from
        - Only as an audit trail nothing reads back
        - Only in a lab
        - No
    - id: confidence
      title: How readable was the mirror on screen?
      type: scale0to10
      min: 0
      max: 10
    - id: missing
      title: What would you need before running this for real?
      type: freeText
      placeholder: Optional — your answer will be visible to the room
```

Both pass the CRD's validations: `choices` is set for the choice question and
unset elsewhere, and `scale0to10` carries `min: 0` / `max: 10` as its rule
requires.

**Keep the date convention.** A vote is one QuizSubmission per identity per round
UID; reusing a name reuses the object, so the room that voted last time cannot
vote again. New talk, new names.

### One round label, replacing the UID

`{label:key}` files by a label *value*, and a QuizSubmission carries one label
today: `voter.configbutler.ai/round-uid`, a UID that reads as `a3f2c1d8-…` on a
projector and cannot be written down in advance.

**Drop the UID and key on the round's name instead** — the same ns/name
reference `spec.sessionRef` already makes, and the shape every other Kubernetes
controller uses. It is a real simplification rather than a cosmetic one:

- Two round labels collapse into **one**. The label the results page selects on
  and the label the mirror files by become the same label.
- `demo1-b.yaml` stops needing a lookup. No `envsubst`, no
  `jsonpath='{.metadata.uid}'` — just `kubectl create -f`.
- The cleanup command becomes typeable:
  `kubectl -n voter delete quizsubmissions -l voter.configbutler.ai/round=demo1-round-2026-09-15`
  where the UID form needed you to go and find the UID first.

**What the UID was actually protecting.** Only one thing: a QuizSession deleted
and recreated *under the same name*. Old submissions are not owner-referenced, so
they survive, and keyed by name they would be counted into the new round — and
the participants who cast them could not vote again, because the submission name
would already exist.

That case is already forbidden by the convention the round file documents:
**a new `metadata.name` for every session, one per talk.** The UID defends
against breaking a rule you control and have written down. For a demo cluster
that is over the top, and you are right to cut it.

Staleness is **not** what you lose. The vote handler's other check —
`req.ResourceVersion != round.GetResourceVersion()`
([participant_quiz.go:195](voter/participant_quiz.go#L195)) — already covers it,
recreation included: a recreated object gets a fresh resourceVersion, so a
browser holding the old one still gets the 409 and is told to reload. The UID
comparison beside it is redundant for that job.

So the labels become:

```go
const (
	roundLabel     = "voter.configbutler.ai/round"     // was round-uid, now the name
	submitterLabel = "voter.configbutler.ai/submitter" // new
)

// ... in the QuizSubmission the vote handler builds:
"labels": map[string]any{
	roundLabel:     round.GetName(),
	submitterLabel: s.DisplayName,
},
```

Four edits carry it through:

1. **The results selector**
   ([participant_quiz.go:299](voter/participant_quiz.go#L299)):
   `roundLabel + "=" + round.GetName()`, replacing `string(round.GetUID())`.
2. **`submissionName`** ([participant_quiz.go:117](voter/participant_quiz.go#L117))
   stops hashing entirely — see [below](#the-submission-name-becomes-readable).
3. **The vote request drops `uid`.** Remove the field from the `req` struct and
   the `req.UID` clause from the 409 check
   ([participant_quiz.go:195](voter/participant_quiz.go#L195)), keeping the
   `resourceVersion` comparison.
4. **The SPA stops sending it**
   ([quiz.ts:24](frontend/src/api/quiz.ts#L24)). This one is not optional and
   must ship in the same release: the handler sets `DisallowUnknownFields()`, so
   a browser still sending `uid` to a backend that dropped it gets a flat 400.

A round name is an object name, so it is already a legal label value — no
slugging needed on that side. The submitter is the half that needs work.

### The submission name becomes readable

With the round keyed by name, the submission name can stop being a hash too:

```go
// <round>-<participant-id>: "demo1-round-2026-09-15-ada-lovelace".
//
// Not a hash any more. The hash existed to fold a round UID and an opaque
// subject into something name-shaped; with the round named and the display
// name label-proof, both halves are already legal DNS-1123 on their own.
func submissionName(roundName, displayName string) string {
	return roundName + "-" + strings.ToLower(displayName)
}
```

**This keeps the protection exactly as it was**, and the reason is worth being
precise about, because it rests on a Room Pass property rather than on anything
here. A Participant's object name is `p-<participantID(displayName)>`, and
enrolment under a name already taken is **refused** rather than shared —
[server.go:757](room-pass/internal/server/server.go#L757) says why: *"The name is
the identity now, so a name already present is someone else's enrollment and
must not be handed out twice: two browsers sharing one Participant would be one
voter with two ballots."* So one display name is one Participant is one Dex
subject. Keying the vote on the name is keying it on the identity Room Pass
already established, and API-server name uniqueness still enforces one ballot per
person per round.

`strings.ToLower` is doing real work: object names are DNS-1123 and must be
lowercase, where a label value may be mixed. Lowercasing `labelName`'s output
reproduces `participantID` exactly — the invariant the Room Pass test should
assert — so the submission name and the participant's own address agree by
construction.

**What you gain, and it is the point:** the submission list is readable. Leave
Lens open on `quizsubmissions` and the room watches
`demo1-round-2026-09-15-ada-lovelace` arrive as Ada votes, instead of
`vote-9f2a…`. Everyone can pick out their own ballot.

**What it makes visible:** who voted is now legible to anyone who can list
submissions, which is every participant — they already hold `get, list, watch`
([participant-rbac.yaml](external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/participant-rbac.yaml)),
so they could always read every ballot; the hash only obscured whose it was.
This is consistent with demo2 already filing each vote at
`results/Ada-Lovelace.yaml`, so it reveals nothing Git would not. Worth knowing
you are making that choice in two places rather than one.

Two bounds to keep in mind. Object names cap at 253 characters and a participant
id at 40, so a round name has ~210 to play with — never a real constraint, but
keep round names sane. And `spec.sessionRef.name` stays the canonical reference;
the object name is a convenience, not the link.

### Make Room Pass accept only label-proof names

A Kubernetes **label value** is at most 63 characters of `[A-Za-z0-9._-]`,
alphanumeric at both ends. An illegal one makes the API server refuse the vote's
`Create` outright, so a participant whose name cannot be expressed as a label
**cannot vote at all**. Fixing that at the source is the right call: constrain
what Room Pass stores, rather than repairing it downstream in three places.

Most of the machinery is already there.
[`participantID`](room-pass/internal/server/server.go#L252) folds a typed name
into `[a-z0-9-]`, ≤40 characters, never leading or trailing `-` — which is
already a legal label value — and
[`validName`](room-pass/internal/server/server.go#L211) already refuses a name
that folds to nothing. What is *stored* as `DisplayName` is still the raw typed
string, though, and that is the value that reaches the label.

So: **store the folded form.** Three edits.

**1. A case-preserving fold, beside `participantID`.** Same rules, same combining
-mark skip, so the two cannot drift:

```go
// labelName folds a typed name into a value legal both as a Kubernetes label
// value and as one path segment in the mirror.
//
// It keeps CASE where participantID lowercases: this value is read on a
// projector and in a Git author line, where "Ada-Lovelace" beats
// "ada-lovelace". Lowercasing the result reproduces participantID exactly, and
// a test should assert precisely that -- it is what keeps the displayed name
// and the address in step.
func labelName(display string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFKD.String(display) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		case r >= 0x0300 && r <= 0x036F: // combining marks NFKD split off
		default:
			dash = true
		}
	}
	name := b.String()
	if len(name) > maxParticipantID {
		name = name[:maxParticipantID]
	}
	return strings.TrimRight(name, "-")
}
```

**2. `validName` returns the folded name**, so `DisplayName` is label-proof by
construction. Keep both existing checks and both existing messages — they still
describe what is refused — and fold at the end instead of returning `v`:

```go
	name := labelName(v)
	if name == "" {
		return "", errors.New("Choose a name with at least one letter or number.")
	}
	return name, nil
```

Diacritics keep folding rather than being rejected: "Renée Descartes" is
accepted and stored as `Renee-Descartes`. A space becomes a dash rather than a
refusal, which matters when fifty people are typing at once — a validator that
rejected "Ada Lovelace" would be a bad moment on stage.

**3. The CRD pattern, as the hard gate.** `validName` covers the join form;
this covers anything that writes a Participant directly.
[types.go:134](room-pass/api/v1alpha1/types.go#L134):

```go
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=40
// +kubebuilder:validation:Pattern="^[A-Za-z0-9]([-A-Za-z0-9]*[A-Za-z0-9])?$"
DisplayName string `json:"displayName"`
```

Two deliberate narrowings against what a label value would allow. `40` rather
than 63, matching `maxParticipantID`, so the display name and the address share
one limit instead of two that disagree past 40 characters. And **no `_` or `.`**,
though a label value permits both: the display name lowercased is now also the
tail of the submission's object name, and `_` is illegal in DNS-1123. Keeping the
pattern to exactly what `labelName` emits means every stored name is legal in
all three places — a label value, a path segment, and an object name. Regenerate
[the CRD](room-pass/config/crd/roompass.configbutler.ai_participants.yaml) and
ship it with the chart.

**`DisplayName` is immutable** (`self == oldSelf`), so existing Participants
cannot be repaired in place. They are owned by their Room, so deleting the Room
takes them with it — do that as part of the cutover rather than discovering it
when an old Participant fails the new pattern.

**The join page must preview the change.**
[`previewScript`](room-pass/internal/server/server.go#L523) is an inline JS copy
of `participantID` that previews the *address* while the name is typed. Now the
**name** changes too, so it should show both — "You will appear as
**Ada-Lovelace**" beside the address — or someone types "Ada Lovelace" and is
surprised by what lands in Git. That means a case-preserving branch in the
script mirroring `labelName`; the existing one already skips `\u0300`–`\u036f`
and caps at 40, so it is a small edit to a function that already exists.

#### Voting requires a Room Pass session

With Room Pass folding the name, a participant's `DisplayName` is label-proof by
construction — but Room Pass is not the only identity that reaches the vote path.
[`requireParticipant`](voter/oidc_handlers.go#L137) checks that a session is
valid and CSRF-clean, not which Dex connector issued it. An operator signs in
through the **github** connector, where `claims.name` is a GitHub profile name
Room Pass never saw, which routinely contains a space.

Rather than repair that name downstream, **refuse the vote**. It is less code, it
removes the last path by which an unvalidated name reaches a label, and it is a
truer rule: voting is for people who came through the door.

Three small edits in voter:

**1. Carry the connector on the session.** The app already requests the
`federated:id` scope ([oidc.go:125](voter/oidc.go#L125)) — which is what makes
Dex emit `federated_claims` at all — but the claim is not decoded. Add it beside
the others at [oidc.go:323](voter/oidc.go#L323):

```go
var claims struct {
	Name            string   `json:"name"`
	Email           string   `json:"email"`
	Groups          []string `json:"groups"`
	FederatedClaims struct {
		ConnectorID string `json:"connector_id"`
	} `json:"federated_claims"`
}
```

Store it as `participantSession.Connector`, and **bump
`participantCookieVersion` to 4**
([participant_session.go:27](voter/participant_session.go#L27)). Sessions issued
before this change carry no connector, and the version check at
[participant_session.go:131](voter/participant_session.go#L131) is what stops one
being read as "not a room session" and silently refused — it forces a clean
re-login instead.

**2. Gate the vote, and only the vote.** In the `POST` branch of
`/public/rounds/{name}` ([participant_quiz.go:176](voter/participant_quiz.go#L176)),
beside the existing `state != "live"` check:

```go
if s.Connector != "room" {
	writeJSON(w, 403, map[string]string{
		"error": "Voting is for people who joined through Room Pass. Scan the QR to join the room.",
		"code":  "NotAParticipant",
	})
	return
}
```

Leave `/public/rounds`, `/public/rounds/{name}` (GET) and
`/public/rounds/{name}/results` open. The operator must still open and close
rounds and read results — that is the whole operator page — and none of those
write a label.

**3. Tell the SPA.** Add `"connector": s.Connector` to the `/auth/session`
payload ([oidc_handlers.go:55](voter/oidc_handlers.go#L55)) so the vote form can
render the reason up front instead of after a failed submit.

The label helper in voter then disappears entirely — `submitterLabel:
s.DisplayName` is the whole of it, because the only sessions that reach that line
came through Room Pass.

Note this is an **application** rule, not an RBAC one, and the difference is
demo-able. `github:simonkoudijs@gmail.com` is bound to `cluster-admin`
([humans-rbac.yaml:49](external/k8s/k8s.koudijs.dev/2-gitops/auth/rbac/humans-rbac.yaml#L49)),
so the API server has no objection whatsoever to that identity creating a
QuizSubmission — which is exactly what
[the next section](#demo1-b-three-votes-one-commit) does. Leave the
`voter-audience` Role alone; it already grants voting only to
`demo:voter-audience`, and that is the grant participants use.

#### One judgement call: names become filenames

Those names become **file names in a public-facing repository** — and, since
[the submission name is readable](#the-submission-name-becomes-readable), object
names in the cluster too. Placement is sticky, so a name cannot be re-filed
later. If someone in the room enrols as
something you would rather not have in `ConfigButler/k8s-audit-trail`, it is in
the tree and in the history.

The new pattern narrows this — letters, digits and `-` only, 40 characters — but
it does not eliminate it: `[A-Za-z0-9-]{1,40}` still spells plenty. Two things
make it smaller, and neither makes it zero:

- The display name is **already** in that repository. It is mapped into
  `user.extra` as `configbutler.ai/claims/display-name`
  ([authentication-config.reference.yaml](external/k8s/k8s.koudijs.dev/2-gitops/auth/authentication-config.reference.yaml))
  precisely so the reverser can author commits as a real person, so every vote
  already carries it in a commit author header. This moves it from the header
  into the filename, not from nowhere into the repo.
- Room Pass gates enrolment, and a Participant can be revoked.

To keep names out of paths entirely, file by round instead —
`"results/{label:voter.configbutler.ai/round|_unsorted}.yaml"` — and keep the
submitter label for `.LabelValue` in the commit subject. You lose the named file,
which is the nicest part, so this is your call rather than mine.

**Placement is sticky**: where a document lands is decided once, when its file is
first created. Ship all of this **before** either round takes a vote, or those
submissions keep whatever path they were first given.

---

## demo1-b: three votes, one commit

[`voter/config/demo1-b.yaml`](voter/config/demo1-b.yaml) is the follow-up to
being locked out of the vote form: three QuizSubmissions created straight
against the API server, plus a `CommitRequest` that puts all three in **one**
commit under a message written by hand.

It is the payoff of the rule above. The app refuses the operator; the API server
does not, because that identity is `cluster-admin`. Saying that out loud — "the
application has a rule, Kubernetes has a different one, and here is the gap" — is
a better story than either half alone.

```console
kubectl create -f voter/config/demo1-b.yaml
```

That one line is the whole of it — which it would not be if submissions were
still keyed by the round's UID. Three things in the file are load-bearing:

- **The round label must match the live round's name.** It appears twice per
  document, in `spec.sessionRef.name` and in
  `voter.configbutler.ai/round`, and a mismatch is silent: the votes are created
  and simply never counted.
- **`create`, not `apply`.** The `CommitRequest` uses `generateName`, which
  `apply` cannot handle, and a vote that already exists *should* fail rather than
  be overwritten.
- **`closeDelaySeconds: 15`.** The request is created in the same breath as the
  writes, so it can arrive before their window is open. Immediate finalization
  would then resolve as `NoWindowInGrace` and the hand-written message would be
  quietly lost while the writes commit on the normal 5s timer. The delay is spent
  *waiting for a matching window*, which is exactly this case.
- **`gitTargetRef: demo1`.** QuizSubmissions are watched by **both** demo
  targets, and a `CommitRequest` finalizes one target's window. So demo1 commits
  immediately and demo2 follows on its own timer a second or two later. Add a
  second `CommitRequest` naming `demo2` if you want the two folders to move
  together.

The `message` is used **verbatim** — template syntax stays literal, so it says
what it means in plain words rather than borrowing `{{.Count}}`.

One honest asymmetry worth naming on stage: the `submitter` label decides the
**filename**, while the commit **author** is whoever ran `kubectl`. The mirror
never claims Ada Lovelace pushed anything — it files her ballot under her name
and records who actually submitted it. That is the distinction the whole demo is
about, visible in one commit.

To re-run in rehearsal, clear the three votes first — with `prune: Always` this
also writes their removal, which is worth seeing once but not on stage:

```console
kubectl -n voter delete quizsubmission \
  vote-ada-lovelace vote-grace-hopper vote-simon-koudijs --ignore-not-found
```

To clear a whole round's votes, including the ones the room cast, the label is
now something you can type without looking anything up:

```console
kubectl -n voter delete quizsubmissions -l voter.configbutler.ai/round=demo1-round-2026-09-15
```

---

## Part 3: the three targets

All three live in the `voter` namespace and share the existing `GitProvider`.
That is forced, not a preference: `GitTarget.spec.gitProviderRef` is a
namespace-local reference, so a target elsewhere needs its own copy of the deploy
key and the signing key.

Keep the three paths **siblings, never nested** — all three run `prune: Always`,
which includes the resync mark-and-sweep, and a folder nested inside another
target's `spec.path` asks a converging mirror to reason about documents it does
not own. `spec.path`, `spec.branch` and `spec.gitProviderRef` are all
**immutable**: getting one wrong means delete-and-recreate.

### `demo1` — the quiz, flat

```yaml
apiVersion: configbutler.ai/v1alpha3
kind: GitTarget
metadata:
  name: demo1
  namespace: voter
spec:
  gitProviderRef:
    name: k8s-audit-trail
  branch: main
  path: clusters/k8s.koudijs.dev/demo1
  placement:
    byType:
      # Both rounds in ONE file, so opening round two in demo2 shows up as a
      # diff in a file the room has already seen.
      examples.configbutler.ai/v1alpha1/quizsessions: "quiz-configs.yaml"
      # Every vote in ONE file, deliberately. A folder of 200 files named
      # vote-<64 hex> says nothing from the back of a room; one growing
      # multi-document file is the shape of the thing.
      examples.configbutler.ai/v1alpha1/quizsubmissions: "submissions.yaml"
      # No Secret can reach this target -- the WatchRule below covers two CRD
      # kinds and nothing else. It is here because the operator refuses a
      # non-identity-complete layout outright unless every sensitive type has a
      # route where collision is impossible. {namespace} and {name} make it
      # impossible; {sensitiveSuffix} renders .sops.yaml.
      v1/secrets: "secrets/{namespace}/{name}{sensitiveSuffix}"
    default: "{name}.yaml"
  serializeNamespace: false
  prune:
    mode: Always
  commit:
    window: 5s
    message:
      liveTemplate: |-
        chore(demo1): {{.Count}} change{{if ne .Count 1}}s{{end}} from {{if .Author}}{{.Author}}{{else}}an unnamed actor{{end}}

        {{range .Resources -}}
        - [{{.Operation}}] {{.Resource}}/{{.Name}}
        {{end -}}
      reconcileTemplate: "chore(demo1): reconcile {{.Count}} {{if .Resource}}{{.Resource}}{{else}}resources{{end}}"
---
apiVersion: configbutler.ai/v1alpha3
kind: WatchRule
metadata:
  name: demo1
  namespace: voter
spec:
  gitTargetRef:
    name: demo1
  rules:
    - apiGroups: [examples.configbutler.ai]
      apiVersions: [v1alpha1]
      resources:
        - quizsessions
        - quizsubmissions
      operations: [CREATE, UPDATE, DELETE]
```

### `demo2` — the coffee menu, and a file per voter

```yaml
apiVersion: configbutler.ai/v1alpha3
kind: GitTarget
metadata:
  name: demo2
  namespace: voter
spec:
  gitProviderRef:
    name: k8s-audit-trail
  branch: main
  path: clusters/k8s.koudijs.dev/demo2
  placement:
    byType:
      # At the root, where a price change is one diff on one obvious file.
      examples.configbutler.ai/v1alpha1/coffeeconfigs: "coffee-config.yaml"
      #
      # THE CONTRAST. demo1 files every vote into one submissions.yaml. These
      # are the same objects read through a label instead: one file per person,
      # named after them, which every round they vote in appends to. The only
      # difference between the two folders is this template string.
      #
      # Bundling (no {name}) is fine here -- a submission is not sensitive. It
      # is also why the v1/secrets route below has to exist and be
      # identity-complete.
      #
      # |_anonymous names the bucket for a submission carrying no submitter
      # label. Without a fallback the operator uses its built-in `_unlabeled`;
      # naming it keeps the word on screen one we chose. A leading underscore is
      # allowed only in a fallback, and that is the point: no real label value
      # may start with one, so nothing genuinely labelled can land here and be
      # mistaken for an unlabelled vote.
      examples.configbutler.ai/v1alpha1/quizsubmissions: "results/{label:voter.configbutler.ai/submitter|_anonymous}.yaml"
      v1/secrets: "secrets/{namespace}/{name}{sensitiveSuffix}"
    default: "{name}.yaml"
  serializeNamespace: false
  prune:
    mode: Always
  commit:
    window: 5s
    message:
      # 0.46.0 lets a commit message read the same label the path did.
      # .LabelValue renders only when EVERY resource in the window agrees on the
      # value, so a commit spanning two rounds stays unnamed rather than being
      # attributed to one of them.
      liveTemplate: |-
        chore(demo2): {{.Count}} change{{if ne .Count 1}}s{{end}}{{with .LabelValue "voter.configbutler.ai/round"}} in {{.}}{{end}} from {{if .Author}}{{.Author}}{{else}}an unnamed actor{{end}}

        {{range .Resources -}}
        - [{{.Operation}}] {{.Kind}}/{{.Name}}{{with .Label "voter.configbutler.ai/submitter"}} ({{.}}){{end}}
        {{end -}}
      reconcileTemplate: "chore(demo2): reconcile {{.Count}} {{if .Resource}}{{.Resource}}{{else}}resources{{end}}"
---
apiVersion: configbutler.ai/v1alpha3
kind: WatchRule
metadata:
  name: demo2
  namespace: voter
spec:
  gitTargetRef:
    name: demo2
  rules:
    - apiGroups: [examples.configbutler.ai]
      apiVersions: [v1alpha1]
      resources:
        - coffeeconfigs
        - quizsubmissions
      operations: [CREATE, UPDATE, DELETE]
```

Two targets watching `quizsubmissions` is supported and costs one extra watch
stream per type. Both write the same branch; the operator serializes per branch.

Note `.Label` in the body line, not `.Labels.x`: these templates render with
`missingkey=error`, so indexing a label a resource does not carry fails the
render — and a failed render fails the commit. A `DELETE` carries no object, so
its labels are empty and `.Label` renders nothing rather than exploding.

Want the rounds separated as well? `"results/{label:voter.configbutler.ai/round|_unsorted}/{label:voter.configbutler.ai/submitter|_anonymous}.yaml"`
files a folder per round with a named file inside — two labels in one path is
supported, since the `/` inside `{label:…}` is part of the key rather than a
separator. The flat form above is the better beat: a person's file *grows* when
round two lands, where a new folder only appears.

#### The app must point its CommitRequest at `demo2`

The coffee save path creates a `CommitRequest` to close the window early — "save
now" instead of waiting out the 5s
([participant_coffee.go:158](voter/participant_coffee.go#L158)) — and it names a
target through `CONFIGBUTLER_GIT_TARGET_NAME`
([config.go:47](voter/config.go#L47)), currently `voter-demo` in
[app.yaml:185](external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/app.yaml#L185).

CoffeeConfig now lives in `demo2`, so that env var must become `demo2`. Miss
this and the coffee save still works — the write lands on the 5s window — but the
"save now" button silently stops doing anything, which is exactly the beat it
exists for.

### `gitops-reverser-config` — the config mirroring itself

```yaml
apiVersion: configbutler.ai/v1alpha3
kind: GitTarget
metadata:
  name: gitops-reverser-config
  namespace: voter
spec:
  gitProviderRef:
    name: k8s-audit-trail
  branch: main
  path: clusters/k8s.koudijs.dev/gitops-reverser-config
  encryption:
    provider: sops
    # Created by the operator on first reconcile; it does not have to exist
    # first. Required whenever extractFromSecret is set.
    secretRef:
      name: gitops-reverser-age
    age:
      enabled: true
      recipients:
        # ConfigButler owns the key -- see trap 4 below. The two flags are a
        # pair: generateWhenMissing writes the identity, extractFromSecret is
        # what reads a recipient back out of it.
        extractFromSecret: true
        generateWhenMissing: true
  placement:
    byType:
      # A sensitive route must be identity-complete -- {namespace} and {name} --
      # so two Secrets can never collide onto one file. A {label:...} would NOT
      # satisfy this: a label is not identity, two resources can share one.
      v1/secrets: "{namespace}/secrets/{name}{sensitiveSuffix}"
    # voter/gittargets/demo1.yaml, voter/watchrules/demo2.yaml, ... and for the
    # cluster-scoped ClusterProvider, _cluster/clusterproviders/default.yaml --
    # that bucket is exactly the 0.46.0 change from Part 1.
    default: "{namespace}/{resource}/{name}.yaml"
  # TRUE here, unlike the two demo targets. This folder is a real backup of the
  # configuration and should be re-appliable as it stands; and it holds both
  # namespaced and cluster-scoped objects, so the documents are the only honest
  # place for the namespace to live.
  serializeNamespace: true
  prune:
    mode: Always
  commit:
    window: 5s
    message:
      liveTemplate: |-
        chore(config): {{.Count}} ConfigButler change{{if ne .Count 1}}s{{end}} from {{if .Author}}{{.Author}}{{else}}an unnamed actor{{end}}

        {{range .Resources -}}
        - [{{.Operation}}] {{.Kind}} {{.Namespace}}/{{.Name}}
        {{end -}}
---
apiVersion: configbutler.ai/v1alpha3
kind: WatchRule
metadata:
  name: gitops-reverser-config
  namespace: voter
spec:
  gitTargetRef:
    name: gitops-reverser-config
  rules:
    # NOTE what is absent: commitrequests. See trap 2.
    - apiGroups: [configbutler.ai]
      apiVersions: [v1alpha3]
      resources:
        - gitproviders
        - gittargets
        - watchrules
      operations: [CREATE, UPDATE, DELETE]
    # The credentials the config refers to. Type-scoped only -- see trap 1.
    - apiGroups: [""]
      apiVersions: [v1]
      resources: [secrets]
      operations: [CREATE, UPDATE, DELETE]
---
# ClusterProvider is cluster-scoped, so it needs the cluster-scoped rule kind:
# a WatchRule never selects cluster-scoped types and vice versa.
apiVersion: configbutler.ai/v1alpha3
kind: ClusterWatchRule
metadata:
  name: gitops-reverser-config
spec:
  gitTargetRef:
    # Cluster-scoped, so the reference must name the target's namespace. That
    # namespace is already admitted by the default ClusterProvider's accessFrom
    # (release.yaml lists "voter"), so no chart change is needed.
    name: gitops-reverser-config
    namespace: voter
  rules:
    - apiGroups: [configbutler.ai]
      apiVersions: [v1alpha3]
      resources: [clusterproviders]
      operations: [CREATE, UPDATE, DELETE]
```

#### Trap 1 — a WatchRule cannot filter Secrets by name

`spec.rules[]` selects by operation, group, version and resource. There is **no**
name selector and no label selector, so `resources: [secrets]` in `voter` mirrors
every Secret in the namespace, not only ConfigButler's two:

| Secret | What it is |
|---|---|
| `k8s-audit-trail-git` | the deploy key |
| `k8s-audit-trail-signing` | the SSH signing key |
| `room-pass-cookie` | app |
| `voter-app-cookie` | app |
| `voter-oidc-client` | app — a real OIDC client secret |
| `demo-koudijs-dev-tls` | cert-manager, and it **rotates** |

You have said this is fine as long as they are encrypted, and it is: every one of
them takes the `{sensitiveSuffix}` route above and lands as `.sops.yaml`,
encrypted to a recipient whose private half is never in the cluster (trap 4).
"Every secret this namespace holds, mirrored and encrypted" is the stronger claim
anyway. Two things to know rather than to fix:

- **`demo-koudijs-dev-tls` rotates on cert-manager's schedule**, so that folder
  gains a commit you did not make every renewal. Not during a demo, and honest
  when it happens.
- **Encryption is the only boundary here.** Confirm the visibility of
  `ConfigButler/k8s-audit-trail` before the first push, and treat the age key
  (trap 4) as the credential that it now is.

#### Trap 2 — never watch `commitrequests`

The backend creates a `CommitRequest` on **every** coffee "save now"
([participant_coffee.go:158](voter/participant_coffee.go#L158)). Add
`commitrequests` to that rule and every save writes a second document into the
config mirror; a room of fifty buries the thing you wanted to show. The list
above omits it on purpose.

#### Trap 3 — the target mirrors itself, and that is fine

`gitops-reverser-config` watches `gittargets` in `voter`, which includes itself.
That is the good part — edit the target and it commits its own new definition —
and it does not loop, for two reasons:

- **`status` is stripped before write.** Only `apiVersion`, `kind`, `spec` and a
  trimmed `metadata` (name, namespace, labels, annotations) are serialized.
- **An unchanged tree skips the commit.** Status churn — stream counts, the 5m
  reconcile — fires watch events whose sanitized document is byte-identical, so
  no commit is planned.

Without both, the every-few-minutes status write would commit forever.

#### Trap 4 — ConfigButler owns the age key

The target sets `generateWhenMissing: true` with `extractFromSecret: true`, so on
first reconcile the operator generates an X25519 identity, creates
`voter/gitops-reverser-age` with a date-named `<YYYYMMDD>.agekey` entry, and
derives the public recipient from it. Nothing has to exist beforehand, and the
two flags are a pair — the operator refuses `generateWhenMissing` without
`extractFromSecret`.

The trade this accepts is **disaster recovery, not secrecy.** That Secret lives
in the namespace this very target mirrors, so a copy of it lands in Git —
encrypted to its own public half, which means it cannot be decrypted without the
key that is inside it. That is not a leak; it is a file nobody can read. It does
mean Git holds no usable copy, so if the cluster goes away, every `.sops.yaml`
in this folder becomes unreadable with it.

The operator asks you to close that gap itself: it stamps a backup-required
annotation on the Secret and logs `ENCRYPTION KEY BACKUP REQUIRED` on every
reconcile until the annotation is removed. Back it up once, after the first
reconcile:

```console
kubectl -n voter get secret gitops-reverser-age -o jsonpath='{.data}' \
  | jq -r 'to_entries[] | select(.key|endswith(".agekey")) | .value' | base64 -d
```

Store it where your other demo credentials live, then quiet the log:

```console
kubectl -n voter annotate secret gitops-reverser-age \
  configbutler.ai/encryption-backup-required-
```

With the private half on your laptop, decrypting on stage is a real
demonstration rather than a formality:

```console
SOPS_AGE_KEY_FILE=voter-demo.agekey sops --decrypt \
  clusters/k8s.koudijs.dev/gitops-reverser-config/voter/secrets/k8s-audit-trail-git.sops.yaml
```

The alternative — generating the pair off-cluster and listing only
`recipients.publicKeys` — keeps the private half out of the cluster entirely and
needs no backup step, at the cost of a manual `age-keygen` before the target can
ever write. Either is defensible; this one was chosen so the demo bootstraps
itself.

## Cutting over

`spec.path` is immutable, so `voter-demo` cannot become `demo1`; it is replaced.

**Done in the working tree** (`external/k8s/k8s.koudijs.dev/2-gitops/`),
uncommitted:

1. `gitops-reverser/release.yaml` — chart pinned to 0.46.0,
   `sha256:1eb3cc64…`, with the tag alongside for legibility.
2. `voter-demo/git-sink.yaml` reduced to the one `GitProvider`; the three
   targets split into `git-sink-demo1.yaml`, `git-sink-demo2.yaml` and
   `git-sink-config.yaml`, all registered in `kustomization.yaml`.
3. `voter-demo/demo-round.yaml` — the two rounds.
4. `voter-demo/app.yaml` — `CONFIGBUTLER_GIT_TARGET_NAME: demo2`.
5. `voter-demo/crds/roompass.configbutler.ai_participants.yaml` — resynced from
   `room-pass/config/crd`, carrying the narrowed `displayName` pattern. This copy
   is what the cluster actually applies; leaving it stale would let a
   Participant be created with a name the voter app then cannot label.

Still to do, in order:
6. **Ship the Room Pass change** — `labelName`, the `validName` fold, the CRD
   pattern and the join-page preview from Part 2 — and roll it out. Then
   **delete and recreate the Room**: `DisplayName` is immutable, so Participants
   enrolled under the old rules cannot be repaired, and they are owned by the
   Room so deleting it takes them with it.
7. **Ship the voter change** — the round label moving from UID to name (backend
   **and** SPA together, or `DisallowUnknownFields` turns it into a 400),
   `submitterLabel`, the connector on the session, the cookie-version bump and
   the Room Pass gate — and roll the deployment. Confirm an operator session
   gets the 403 and can still open a round and read results.
8. **Clear the trail repo**: delete `clusters/k8s.koudijs.dev/voter/` in a commit
   to `ConfigButler/k8s-audit-trail`. Deleting a GitTarget stops its writes; it
   does not remove its folder, so the old one would sit there orphaned.

Order matters in two places. Room Pass (6) goes before voter (7), so no
Participant is enrolled under the old rules after the label starts being written.
Both go before **any** round takes a vote, because placement is sticky. They are
also the steps that can break voting outright — an illegal label value makes the
API server refuse the `Create` — so test both folds against a table before they
meet a room.

### Verifying

```console
kubectl -n voter get gittargets -o wide
kubectl -n voter get watchrules -o wide
kubectl get clusterwatchrules -o wide
```

All three `Validated=True`, `Ready=True`, streams converged. `demo1` should show
2 streams, `demo2` 2, `gitops-reverser-config` 4 (3 namespaced + 1 cluster).

Then, in the trail repo, the tree at the top of this document. Spot-check one
submission before trusting the rest:

```console
kubectl -n voter get quizsubmissions --show-labels | head
```

Every row should carry both labels, and `demo2/results/_anonymous.yaml`
should not exist at all: every vote now comes either from a Room Pass session or
from `demo1-b.yaml`, and both carry a submitter label. If it appears, something
created a QuizSubmission without one.

Check the gate itself while you are there — sign in as the operator, confirm the
vote form refuses with `NotAParticipant`, and confirm the same session can still
open a round and read results.

Expect a steady trickle of `WatchError` Warning events — roughly one per watched
type every forty minutes, self-healing in about two seconds. That is the
apiserver's normal randomized watch timeout, not a fault; it is
[entry 1 of the feedback doc](docs/gitops-reverser-feedback.md). This plan goes
from 3 watched streams to 8, so expect roughly the same increase in the rate.
Worth knowing before an event feed goes on a projector.

## The beats this buys

**demo1 — the quiz**

1. Room votes. `demo1/submissions.yaml` grows a document per vote, each commit
   authored by the participant and committed by the robot — `git show
   --format=fuller` names both.
2. Open `demo1/quiz-configs.yaml`: both rounds, one file, including the one not
   open yet.

**demo2 — the coffee bar**

3. Change a price in the browser. `demo2/coffee-config.yaml` moves, and "save
   now" closes the window on the spot instead of waiting 5s.
4. Click **Open** on round two. Two things move at once: `demo1/quiz-configs.yaml`
   flips `closed` → `live`, and the room's page updates off the watch stream.
5. Room votes again. `demo1/submissions.yaml` takes every vote in one file,
   while in `demo2/results/` **the files that are already there grow** — each
   person's round-two vote appended to the file their round-one vote created.
   Same objects as the file next door; the only difference is one template
   string reading one label.
6. Try to vote as the operator. The form refuses: you did not come through Room
   Pass. Then run `demo1-b.yaml` — three ballots and one `CommitRequest` — and
   **one** commit appears carrying all three, under a message you wrote. The app
   has a rule; Kubernetes has a different one; `cluster-admin` sits on the far
   side of the gap and the mirror records exactly who walked through it.
7. Put `demo2/results/Ada-Lovelace.yaml` on screen and run `git log -p` on it:
   one person's voting history across both rounds. The filename is her label;
   the author of the last commit is you.

**the config**

8. Open `gitops-reverser-config/voter/gittargets/demo2.yaml` — the rule that
   produced beats 5 to 7, exported by the thing it configures.
9. Open a `*.sops.yaml`, then decrypt it from your laptop with a key that was
   never in the cluster.
