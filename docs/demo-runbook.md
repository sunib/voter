# Demo runbook — "GitOps Needs an API"

Swiss Cloud Native Day 2026, 17 September. A 45-minute slot: **40 minutes of
talk, 5 of questions.** Two live demos and a closing round, a room full of phones.

This is the operator's copy: what to press, what the room sees, what breaks and
what to do about it. It is also the answer to "what are we building for" — if a
feature is not in here, it is not in the talk.

Read [ARCHITECTURE.md](../ARCHITECTURE.md) for how it works and
[authorization.md](authorization.md) for who may do what. This file is the
choreography.

---

## The shape of the 40 minutes

The slot is 45. **The talk is 40 and the last five are questions** — so every
number below is five minutes tighter than the version of this runbook you read
last week. [`presentation.md`](presentation.md) carries the same clock in its
presenter notes, slide by slide. If the two ever disagree, the deck is right,
because the deck is what is in front of you on stage.

| Clock | Beat | Room's phones |
|---|---|---|
| 0:00–3:15 | Why we like GitOps: the four W's, and the fact that Git is not easy | idle |
| 3:15–8:30 | The problem arc, eleven diagram builds | idle |
| 8:30–10:30 | The big picture, today's setup, and what is out of scope | idle |
| 10:30–18:00 | **Demo 1** — identity, authorization, and a Kubernetes object you did not expect | **in use** |
| 18:00–24:00 | The mechanism: how you observe a cluster, and what a commit can carry | idle |
| 24:00–36:00 | **Demo 2** — Git, conflict, and the one place the room works together | **in use** |
| 36:00–38:45 | The honest checklist: where this fits, where it does not | idle |
| 38:45–40:00 | **The evaluation round, opened live** | **in use** |
| 40:00–45:00 | Questions, with the results still on screen | in use |

**Three phone segments now, not two.** The third one is cheap because it is the
close: you need their attention anyway, and the button press that opens it is
itself the last artifact of the talk. See "The closing round" below.

Five minutes came out of the 45-minute version, and here is exactly where from,
so you can put any of it back if a rehearsal runs short:

| Cut | Saves | Was |
|---|---|---|
| Demo 1 step 4, the interloper | 2:00 | optional already; this is the third time it has been spent |
| Demo 1 step 3 tightened | 0:30 | four minutes of refusals; three is enough |
| Demo 2 step 2 told, not walked | 0:30 | this runbook already conceded it survives 90 seconds |
| Demo 2 step 3 moved to the close | 2:00 | it costs nothing there, and it makes the close live |
| The bio merged into one slide | 1:00 | two slides; nobody is here for it |

**The first eight minutes talk about Git, and that is fine.** What is being held
back is not the word. It is the evidence that *this cluster has been writing to a
repository since they scanned the code*. Naming the problem — intent ends up in
a repo that the people who own the intent cannot reach — is the setup for both
demos and gives nothing away. "The secret" below draws the line.

**What to sacrifice if you are over on stage**, in the order you should reach for
it: the krm-stream slide after demo 1 (30s, pure bonus), demo 2 step 2 told
rather than walked (30s), the coffee menu dropped from demo 1 step 2 (30s — it
comes back in demo 2 anyway). Do not cut demo 2 step 4; it is the beat the talk
is named for. Do not cut the closing round; it is where the last commit comes
from.

---

## What you are asking of the room

They need a phone with a camera and a browser. No app, no account, no typing a
URL. They scan a projected QR code and type a display name.

**Do not say what the name becomes.** This runbook used to open with the best
sentence in the talk — *"the name you type becomes a filename in a Git
repository, choose accordingly"* — three sections above a rule saying not to
mention Git in demo 1. It is now the last sentence rather than the first. Say
this instead:

> Type a name you are happy for a room full of strangers to read.

Be honest with yourself about the trade. The old line bought careful names; this
one buys fewer, and you will get more `asdf`. That is the price of the reveal
landing on a room that did not see it coming, and it is worth paying — the
invented voters in "Casting your own answers" are partly there to keep the file
readable when the room's own contributions are not.

The deployment no longer helps you spoil it either. `Room.spec.attributionNote`
read "It labels your changes in Git." and now says the name and address are used
for demo purposes: true, enough for an informed choice, silent on the part you
are saving. The join page is titled "Room Pass" rather than after this demo, for
the same reason — it says what the page is, not what the talk is about.

Expect 30–60% of the room to join. Design every beat so a non-participant still
sees the point on the projector: the room screen and the results screen are the
demo, the phone is the participation.

The room is `demo`, enrolment `Open`, capped at **300 participants**. The join
code is 6 characters, **rotates every 30s and stays valid for 120s** — so a
photographed code dies quickly, and a slow scanner still gets in.

---

## Two repositories, and only one of them is a source

Worth re-reading before you walk on, because the whole containment argument
rests on it and it is the one thing an audience question can catch you on.

```mermaid
flowchart LR
    G1["ConfigButler/k8s<br/><i>the cluster's repo</i><br/>Flux reads this"]

    subgraph K["namespace voter, on k8s.koudijs.dev"]
        REC["reconciled forever<br/>Deployments, RBAC, Room,<br/>GitProvider, GitTargets"]
        SEED["seeded once, then let go<br/>CoffeeConfig<br/>both QuizSessions"]
    end

    R(["gitops-reverser"])

    G2["ConfigButler/k8s-audit-trail<br/><i>the demo's repo</i><br/>written, never read"]

    DEAD>"Nothing reconciles<br/>from here. An attendee's<br/>commit cannot reach<br/>the cluster."]

    G1 ==>|"Flux, every 10 min"| REC
    G1 -.->|"on create only"| SEED
    REC --> R
    SEED --> R
    R ==>|"one commit per action,<br/>authored by whoever did it"| G2
    G2 --- DEAD
```
**`ConfigButler/k8s`** is the ordinary one: the cluster's GitOps repo, Flux's
only source, everything under `k8s.koudijs.dev/2-gitops/voter-demo/`. You change
it by pull request and Flux reconciles it, like any cluster.

**`ConfigButler/k8s-audit-trail`** is the one the talk is about. It holds
`clusters/k8s.koudijs.dev/demo1/`, `demo2/` and `gitops-reverser-config/`, and
**only `gitops-reverser` ever writes to it.** No Flux source points at it, no
controller reads it, and that is the answer to the security question demo 2
always gets — see the containment line in demo 2 step 1.

Say the two-repo split out loud if anyone asks how the commits get authored. The
cluster is not committing to its own source of truth; it is keeping a record in a
place that cannot feed back.

### The three objects Git seeds and then lets go

`CoffeeConfig/demo-coffee` and both `QuizSession`s carry
`kustomize.toolkit.fluxcd.io/ssa: IfNotPresent`, which means Flux creates them
and never applies them again.

They have to. Flux applies server-side and owns every field it declares,
including `maximumUsage` and `state`. Without the annotation the demo is quietly
bi-directional and loses both ways: the room raises the voucher limit and the
next reconcile reverts it, or you press Open on `evaluation` at the close and
Flux closes it again while the room is still voting. The reconcile interval is
10 minutes — comfortably inside a 45-minute slot.

Two consequences, both of which bite at pre-flight rather than on stage:

- **Editing those specs in Git no longer changes a cluster where the object
  exists.** Changing a price in `app.yaml` and pushing does nothing. To re-seed,
  delete the object and let Flux recreate it:
  `kubectl -n voter delete coffeeconfig demo-coffee`
- **The rounds are now called `demo1` and `evaluation`, and those names are
  stable.** That reads better everywhere — the demo2 commit template
  interpolates the round name into the subject line, so a commit says
  `chore(demo2): 1 change in evaluation` instead of carrying a date nobody
  needs. (No author on that line any more — see "What the commits say".) But it removes the escape hatch this runbook used to lean on.

> ⚠️ **The rename trick is gone, and it was doing three jobs.** A dated round
> name used to reset `state`, drop the previous room's ballots, and let a
> returning audience vote again, all for free. With fixed names, **every one of
> those is now a manual step**, and the one that bites is the rehearsal: open
> `evaluation` this afternoon to check it works and it is still `live` tomorrow
> morning, because Flux will not apply over an object that already exists. See
> T-24h and "Reset between runs" — both now carry the commands.

**Do not verify this by looking for the annotation on the live object.** Flux
reads it from the manifest it is about to apply, sees the object already exists,
and skips — so it never writes it. `kubectl get coffeeconfig demo-coffee -o yaml`
shows no annotation, and that absence is the proof it is working. Verify by
behaviour instead: patch a field, `flux reconcile kustomization voter-demo`, and
check the value survived.

### What the commits say

Every commit message lost its author line. They used to end `from
demo:CgZzaW1vbjcSCXJvb20tcGFzcw` — the raw Kubernetes username, which is the
identity RBAC authorized and reads on a projector like a base64 accident — and
demo2's body line repeated the display name in brackets on top of that. The same
fact, three times, one of them unreadable.

It now appears exactly once, where Git already keeps it:

```
commit 4aaa861…
Author:     Ada-Lovelace <ada-lovelace@koudijs.dev.test>
Commit:     ConfigButler Bot <bot@configbutler.ai>

    chore(demo2): 1 change in demo1

    - [CREATE] QuizSubmission/demo1-ada-lovelace
```

That is a better demo, not just a tidier one. The subject says **what changed**
and the header says **who** — so `git show --format=fuller` in demo 2 step 1 is
the moment the name appears, rather than a repetition of something already on
screen. Say it that way: *"the message never mentions her. Git's own author
field does, and the cluster filled it in."*

**What is not in there is a resourceVersion**, and it cannot be. A live commit
template's per-resource context exposes Operation, Group, Version, Resource,
Kind, Namespace, Name and Labels — and no resourceVersion — while the mirrored
YAML has it stripped by the reverser's sanitizer, deliberately, or every write
would churn the file. A reconcile commit is the one place a version is
available, and those now say `at rv 184213` when the reverser pins one. See
[gitops-reverser-feedback.md](gitops-reverser-feedback.md).

---

## Pre-flight

### T-24h

- [ ] **Deploy the questions** from
      [`demo-questions.yaml`](demo-questions.yaml) — two sessions, `demo1` with
      four questions and `evaluation` with four. The ids are referenced further
      down this file; if you change one, change it here too.
- [ ] **Delete the sessions and let Flux re-seed them.** Pushing is not enough:
      they are `ssa: IfNotPresent`, so Flux will not apply over an object that
      exists. This is the *only* way to change the questions now that the names
      are stable:

      ```bash
      kubectl -n voter delete quizsession demo1 evaluation
      flux reconcile kustomization voter-demo
      kubectl -n voter get quizsessions
      ```

      The old dated sessions (`demo1-round-2026-09-15` and friends) are not
      removed by that delete — they go when Flux prunes them, because they left
      the kustomization when `demo-round.yaml` became `demo-questions.yaml`.
      Confirm nothing dated is left in the same `get`.
- [ ] **`demo1` is `live`, `evaluation` is `closed`.** Not `draft` — a draft is
      filtered out of the room page entirely and there is no button to press.
      **Check it, do not assume it**, and check it again after any rehearsal:
      `kubectl -n voter get quizsessions` prints the state of both. If a
      rehearsal opened it, a push will not close it again:

      ```bash
      kubectl -n voter patch quizsession evaluation --type=merge -p '{"spec":{"state":"closed"}}'
      ```

- [ ] **Delete every ballot from both rounds.** Submissions are not owned by
      their round and the names no longer carry a date, so nothing expires on
      its own. Skip this and tomorrow's room sees today's answers and cannot
      vote:

      ```bash
      kubectl -n voter delete quizsubmissions -l voter.configbutler.ai/round=demo1
      kubectl -n voter delete quizsubmissions -l voter.configbutler.ai/round=evaluation
      ```

- [ ] **If you changed the coffee menu**, pushing is not enough either — same
      reason. `kubectl -n voter delete coffeeconfig demo-coffee`, then reconcile.
- [ ] Confirm the audit-trail repo is reachable and the previous talk's folders
      are either cleaned or clearly dated. `prune: Always` means the deletions
      above reach Git, so expect commits from this step.

### T-30m

- [ ] `kubectl -n voter get pods` — voter and room-pass ready, one replica each.
      **One replica is load-bearing**: voucher counts and the order feed are
      per-process.
- [ ] Open `https://demo.koudijs.dev/auth/login?connector=github&return=%2Froom`
      on the presenter machine. That is the operator door; it puts you at
      `/room` with the rotating QR.
- [ ] Join from your own phone as a participant. Vote in `demo1`. This proves
      the whole chain and gives the room's first file a neighbour.
- [ ] `git -C <audit-trail> pull` and confirm your test vote arrived in **both**
      places — every submission is mirrored twice, because both WatchRules claim
      `quizsubmissions`:
      `clusters/k8s.koudijs.dev/demo1/submissions.yaml` and
      `clusters/k8s.koudijs.dev/demo2/results/<your-name>.yaml`.
- [ ] Delete the test vote so the room starts clean:
      `kubectl -n voter delete quizsubmissions -l voter.configbutler.ai/round=demo1`
- [ ] **Confirm the audience grant is off.** On `/room`, *Let the room edit the
      coffee menu* must be unticked — demo 1's refusal depends on it. If a
      previous run left it on:
      `kubectl -n voter delete rolebinding voter-audience-coffee-admin`
- [ ] Open `/me` on your own phone and confirm the permission grid renders. It
      is the first thing you point the room at in demo 1 step 3.
- [ ] **Nothing the audience can read may mention Git.** Two values, one
      command — the CoffeeConfig is seeded and not reconciled, so a fix in the
      platform repo does not reach the live object on its own:

      ```bash
      kubectl -n voter get coffeeconfig demo-coffee -o jsonpath='{.spec.bannerText}{"\n"}'
      kubectl -n voter get room demo -o jsonpath='{.spec.title}: {.spec.attributionNote}{"\n"}'
      ```

      The banner must not say "Git"; if it does, patch it back:
      `kubectl -n voter patch coffeeconfig demo-coffee --type=merge -p '{"spec":{"bannerText":"Edit this menu. Every phone in the room sees your change."}}'`.
      The Room is reconciled by Flux, so its two values look after themselves.
- [ ] Have the audit-trail repo open in a browser tab, on the `demo1` folder,
      **not yet shown**.

### T-2m

- [ ] `/room` projected, QR visible, code rotating.
- [ ] Terminal ready with `kubectl` context on the demo cluster, font size up.
- [ ] Phone on the lectern, signed in, so you can show a participant's view
      without asking a volunteer to hand you theirs.
- [ ] **Last look at the audience grant.** `/room` → *Let the room edit the
      coffee menu* unticked. This is checked twice on purpose: when it is
      wrongly on, nothing breaks and nothing warns you — demo 1's coffee refusal
      just quietly does not happen, and demo 2's best beat has nothing to give
      away. One command settles it:
      `kubectl -n voter get rolebinding voter-audience-coffee-admin` should say
      `NotFound`.
- [ ] **Last look at the two round states**, for the same reason — a wrongly
      `live` `evaluation` breaks the close silently and the room will have voted
      in it during demo 1. One command settles it:

      ```bash
      kubectl -n voter get quizsessions
      # demo1       live
      # evaluation  closed
      ```

---

## Demo 1 — who you are, who decides what you may do, and what a vote actually is

**Clock 10:30 → 18:00, seven and a half minutes. Git is not mentioned once.**
See "The secret" below.

Demo 1 has two jobs and only two. The first is identity and authorization: who
the room is, and who decided what they may do. The second is quieter and lands
better for being unannounced — **the things this application keeps in Kubernetes
are not the things anyone expects to find there.** A vote is an object. A coffee
menu is an object. You show that and then let it sit, because the surprise here
is the API, not the repository, and the repository is demo 2's to reveal.

### 1. Get them in (2 min)

Project `/room`. The QR is on screen and the code rotates. They scan, type a
name, and land on the quizzes page.

While they join, say what just happened, because it is the substance:

- The QR carries a **room code**, not a credential.
- Room Pass validates the code, writes a `Participant` object, and is the *only*
  thing that may call Dex's header-trusting connector.
- Dex mints an ID token. The **API server** — not the app — derives the username
  from `federated_claims.connector_id`, which Dex sets and no caller can forge.
- Everyone in the room is now `demo:<opaque-subject>` in group
  `demo:voter-audience`.

### 2. Let them vote, then show them what they just wrote (3 min)

`demo1` is live. Four questions — Argo/Flux, Helm/Kustomize, how they change
configuration today (multi-select), and one free-text line. Project the results
screen and let the bars fill. **Press "Refresh results" to make them fill** —
the results screen loads once and has a button, not a stream. The room page
updates itself; this one does not. Give it a press every few answers so the
bars visibly grow rather than sitting still while you talk over them.

That is the warm-up, and it earns the room's attention for what follows.

**Take the two free callbacks off those bars.** They cost nothing now and they
buy you two moments later:

- **Argo vs Flux** — "whichever you picked, nothing in this talk changes it.
  They both live downstream of everything I am about to show you." Use it at the
  big-picture slide.
- **Helm's share** — *remember the number*. At the honest-limits slide you get to
  say "sixty-odd percent of this room said Helm, and a chart is the one thing I
  cannot reverse into readable intent." Your own audience proving your own
  limitation is worth more than the question cost.

And say what the multi-select bars are showing, because they are the argument
from the "But Git is not easy" slide made out of their data: GitOps and
`kubectl apply` are both lit up. *"You are all doing both, and only one of them
leaves a story."*

Then go to the terminal while the bars are still fresh:

```bash
kubectl -n voter get quizsubmissions
kubectl -n voter get quizsubmission demo1-<somebody> -o yaml
kubectl -n voter get coffeeconfig demo-coffee
```

**This is demo 1's second job.** Every answer the room just gave is a Kubernetes
object, named after the person who gave it, validated by a CRD whose CEL rules
refuse two answers to one question. The coffee menu next to it is another one.
Nobody reached for a ConfigMap and nobody put a REST API in front of a database.

Let the oddness sit there unresolved. Do not justify it — the middle segment is
where the pattern gets a name — and do not mention Git. "Your vote is a
Kubernetes object" is a big enough thing to have said, and it is the sentence
demo 2 collects on.

### 3. Show what they *cannot* do (2.5 min) — the core of the authorization half

This is the part worth rehearsing, because every refusal below is a **real API
server 403**, rendered verbatim. There is no client-side permission model to
give the game away.

| Try this | What happens | Why |
|---|---|---|
| Vote twice in `demo1` | "You have already voted in this round." | One `QuizSubmission` per identity per round, named `demo1-<name>`. The create is atomic, so two tabs still make one vote. |
| A participant opens or closes a round | **403 from Kubernetes**, shown on the page | The audience Role has `get, list, watch` on `quizsessions` and no `patch`. |
| A participant edits the coffee menu | **403 on save**, with the page saying so before they try | `coffeeconfigs: get, list, watch` — no `patch`. They may read the menu and watch it change. Editing lives in a second Role that is not bound yet; you hand it out in demo 2. |
| A participant edits their own vote | no path to it, and **403** if attempted directly | `quizsubmissions: get, list, watch, create`. Create-only, on purpose. |

Then answer the question the room is now asking — *who checked that?*

**Tell them to open their own name in the top bar.** `/me` now has a permission
grid under the identity fields: the API server's answer, about their token, on
their phone. The missing `patch` on `coffeeconfigs` is visible as a gap in a
column before anyone presses anything, which is a better order than being
refused and then told why.

Then show it from the other side, in the terminal:

```bash
A="--as=demo:some-subject --as-group=demo:voter-audience --as-group=system:authenticated"

kubectl -n voter auth can-i create quizsubmissions $A   # yes  — they may vote
kubectl -n voter auth can-i patch  quizsessions   $A    # no   — not open a round
kubectl -n voter auth can-i patch  coffeeconfigs  $A    # no   — not yet
kubectl -n voter auth can-i delete coffeeconfigs  $A    # no   — not ever
```

**The third line says `no` and that is the setup for demo 2.** It used to say
`yes`; the audience Role lost `patch` on `coffeeconfigs` when the live grant went
in. Run this block before the talk — if that line answers `yes`, a previous run
left the grant bound and demo 1's best refusal is silently gone.

The point to land: **the application never decided any of this.** The
participant's own token went to the API server, RBAC answered, and the app
rendered the answer. That is what makes the audit trail honest — and it is the
setup for demo 2, though you do not say so yet.

### 4. The interloper — CUT, and this is where it went

Log in with GitHub instead of the room code. You are now `github:<email>` — a
different namespace of identity, which **cannot** hold demo grants, because the
API server derives the prefix from the connector. Try to vote: refused.

**Be careful how you say this one, because it is the one refusal in demo 1 that
is NOT Kubernetes'.** The vote handler checks the Dex connector and returns its
own 403; your operator identity is bound to `cluster-admin`, so the API server
has no objection at all to the same write made with `kubectl`. It is entry 4 of
[deliberate-simplifications.md](deliberate-simplifications.md), and the reason
is mundane: an operator's GitHub profile name has never been folded by Room
Pass, usually contains a space, and would be refused as a label value with a 422
nobody in the room could read.

Say that out loud rather than letting the room assume RBAC did it — in a segment
whose whole claim is "the application never decided any of this", one refusal
that *is* the application's needs naming. It is a better beat honestly told:

> The app has a rule here and Kubernetes has a different one. Watch — the app
> says no in the browser, and then I do exactly the same thing with `kubectl`
> and it works, because I am cluster-admin. Which is worth knowing about RBAC:
> it only ever *adds*. There is no rule you can write that takes something away
> from an administrator. If you want that, you need a different layer.

**This beat is cut from the 40-minute version.** It is the two minutes that pay
for the questions at the end, and it is the third time this runbook has spent
them. It stays written down because it is genuinely strong and because you may
walk on early — but the deck has no slide for it, so putting it back means
talking over the demo-1 slide for two extra minutes. Decide before you go on, not
on stage.

---

## The secret: do not SHOW Git in demo 1

Your instinct is right, and the deployment is now built for it.

`demo1` has been mirroring the whole time. Every vote the room cast in the last
nine minutes is already in `clusters/k8s.koudijs.dev/demo1/submissions.yaml`,
each commit authored by the person who cast it. You have said nothing about it.

So demo 2 opens on a file that is *already full of the room's answers*. The
reveal is "this has been happening since you scanned the code", which is a much
better beat than "watch, I will now demonstrate a commit". Keep it.

**The line is between talking and showing.** The problem statement in the first
eight minutes is allowed to say that business intent ends up in Git and that
stakeholders do not open pull requests; a room that has not heard that does not
know why any of this matters. What may not appear before minute 25 is evidence
that this cluster is doing it right now.

Four things can spoil it. Three are now fixed in the deployment; the fourth is
you.

- **The audit-trail repo in a browser tab, or a `git log` left on screen.**
  Still entirely on you. Check your tabs before you walk on.
- The join page's attribution note, which read "It labels your changes in Git."
  Now says demo purposes only.
- The display-name line this runbook told you to say out loud. Gone — see "What
  you are asking of the room".
- The storefront banner, which read "Edit this menu -- your change becomes a Git
  commit in your name". The audience may read `coffeeconfigs` from the moment
  they join, so that was legible on a phone during demo 1, on the very page
  demo 1 invites them to open and be refused by. **The seed in Git is fixed;
  the live object is seeded, not reconciled, so confirm it at T-30m.**

---

## Demo 2 — the same actions in Git, and the one place the room works together

**Clock 24:00 → 36:00, twelve minutes.** The room is still signed in from demo 1;
do not make them join again.

Step 3 used to be "open round two, live". It now happens at 38:45, during the
close — see "The closing round" below. Everything else keeps its shape, and the
steps are renumbered.

Two arguments, in this order. The first is the reveal: everything they have
already done is in a repository, attributed to them by name, and not one of them
has a GitHub account. The second answers the question the first one provokes in
any engineer in the room — *if Git is the record, what happens when two people
change the same thing?* — and its answer is the thesis of the talk. **Git is the
record. The Kubernetes API is where you work together.** Step 4 is where that
stops being a slogan and becomes a red dot on a screen.

### 1. The reveal (2 min)

Open `clusters/k8s.koudijs.dev/demo1/submissions.yaml`. It is one growing
multi-document file holding every vote the room cast. Scroll it. Let the room
find their own name.

Then `git log` it, and open one commit with:

```bash
git show --format=fuller <sha>
```

Two names, and the split is the whole argument:

- **Author** — the participant. Recovered from the Kubernetes audit event.
- **Committer** — `ConfigButler Bot <bot@configbutler.ai>`, signed.

The cluster made this commit *on a named human's behalf*. Nobody in this room
has a deploy key, a GitHub account on that repo, or any idea what a pull request
is.

Say the containment line here, because a security-minded room is already
composing the question: **`k8s-audit-trail` is not a Flux source.** Nothing
reconciles from it. A commit written by an attendee can never travel back into
the cluster. The picture is in "Two repositories" above if you want it fresh
before you walk on.

### 2. The same objects, filed two ways (90 seconds — tell it, do not walk it)

Now open `clusters/k8s.koudijs.dev/demo2/results/`. One file per person, named
after them, and every round they vote in appends a document to their file. That
is not a second copy made for the demo: **both WatchRules claim
`quizsubmissions`**, so every vote was mirrored into both folders at the moment
it was cast.

These are **the same `QuizSubmission` objects**. The only difference between the
two folders is one template string:

```yaml
# demo1
examples.configbutler.ai/v1alpha1/quizsubmissions: "submissions.yaml"

# demo2
examples.configbutler.ai/v1alpha1/quizsubmissions: "results/{label:voter.configbutler.ai/submitter|_anonymous}.yaml"
```

This is the moment the pattern stops being a diagram. The API is typed, the
objects are the intent, and *how they are laid out in Git is configuration* —
not something the caller knows or cares about.

### 3. Grant the room the admin page (2 min)

The room's admin page has been refusing them since demo 1. On `/room`, under
**What the audience may do**, tick *Let the room edit the coffee menu*.

What happens, in this order:

1. A `RoleBinding` is created — by **your** token, not the application's.
2. Every phone's `/me` grid grows a `patch` column on `coffeeconfigs`, within a
   few seconds, with no reload and no new sign-in.
3. Their admin page stops refusing. The banner disappears and Save starts
   working, on the page they already had open.

Say what did *not* happen: no feature flag, no application permission, no
deploy. The application never learned anything — each phone asked Kubernetes
what it may do and got a different answer than it did in demo 1.

The binding is not a Flux resource, deliberately, or Flux would recreate it
after you switched it off.

**Show the commit. This is the strongest artifact in the talk**, because it is
the only place where "who may do what" is a reviewable file with a human's name
on it. `demo1/authorization.yaml` gains eighteen lines the moment you tick the
box:

```bash
git -C <audit-trail> pull
git -C <audit-trail> log --format=fuller -1 \
  -- clusters/k8s.koudijs.dev/demo1/authorization.yaml
git -C <audit-trail> show --format=fuller HEAD
```

The commit message is
`chore(demo1): 1 change from github:<you>` with the body
`- [CREATE] rolebindings/voter-audience-coffee-admin`, **Author** is you and
**Committer** is the bot — the same split as every vote in demo 1, now applied
to a permission rather than an answer.

The diff lands directly under the `Role` it references, which is worth pointing
at: the rules were already in the file and reconciled by Flux, and what you just
added is the one object that connects them to a group of people.

Switch it back off at the end. Watching a permission be taken away is worth
fifteen seconds, and it proves the switch was real in both directions — the
revoke is a clean `+0/-18`, with the document gone from Git rather than
tombstoned. Verified on 2026-09-15; both directions produce a commit.

### 4. Two people, one menu (5 min)

Now let *them* do it — and then collide with them on purpose. This is the beat
the talk is named for.

**The bug.** Someone orders with the `TESTNET` voucher and it fails: *"This
voucher has been used the maximum number of times."* The storefront showed the
discount right up to the submit. That is the bug the demo is about, and it is a
configuration bug.

**The edit.** Open the menu editor and raise `maximumUsage`. **Do not save.**
Yellow dots mark the fields you changed and the summary offers to save one
change.

**The collision.** With that draft still open and unsaved, have a second person
change the same field — a volunteer, now that step 3 gave them the permission,
or you from the terminal if nobody bites:

```bash
kubectl -n voter patch coffeeconfig demo-coffee --type=json \
  -p '[{"op":"replace","path":"/spec/vouchers/0/maximumUsage","value":99}]'
```

**Use `--type=json`, not a merge patch.** `spec.vouchers` is a list, and a JSON
merge patch replaces a list wholesale — `-p '{"spec":{"vouchers":[...]}}'` would
delete every voucher you did not retype, live, in front of the room. Check the
index first with `kubectl -n voter get coffeeconfig demo-coffee -o jsonpath='{range .spec.vouchers[*]}{.code}{"\n"}{end}'`;
`TESTNET` is at `0` today and that is not guaranteed after an edit.

Your screen changes with no reload. The field turns **red**, the notice reads
*"Another editor changed the same fields. Review the highlighted conflicts."*,
and the summary lists both values with **Take Theirs** and **Keep Mine** beside
them. **Save is disabled** until every conflict is resolved — not discouraged,
disabled; the footer counts "N edit(s) and M missed incoming change(s) in this
save" so nobody resolves one by accident.

Three things to say while that is on screen, in this order:

1. **Be precise about who refuses, because a sharp room will ask.** The red dot
   and the disabled button are the browser: the editor saw the watch event and
   will not let you write over it. Underneath, the save carries the
   `resourceVersion` the draft was built on, so if you *did* get past the
   browser the **API server** would refuse it with a 409, exactly as it refuses
   a stale `kubectl apply`. The UI is a courtesy; the guarantee is Kubernetes'.
   Say it that way round — this is the same shape as the interloper beat in
   demo 1, and it costs nothing to be accurate.
2. **Only the collision is a conflict.** Change a *different* field from the
   terminal and watch it arrive as an ordinary update while your draft survives
   untouched. That is what an API can do and a three-way text merge cannot: it
   knows what a field is.
3. **So this is why the API is the source of truth and the repository is the
   record.** Two people cannot both be right about `maximumUsage`, and the place
   that settles it has to be the one they are both already talking to. Git finds
   out afterwards. If the room takes one sentence home, take that one.

**The save.** Resolve it — Take Theirs or Keep Mine, and say out loud which you
chose and why. Then fill in the reason box. It is not decoration: it becomes
`CommitRequest.spec.message`, which becomes the commit message on a commit
authored by whoever pressed the button. Press Save, then **save now**, which
creates the `CommitRequest`.

Hand the whole thing to the room if the grant from step 3 is on — it is their
permission now, and a commit authored by an attendee fixing the bug beats one
authored by you. The conflict half works either way, because the second editor
can always be your terminal.

Two separate facts the UI is careful to distinguish, and you should be too:

1. **Kubernetes saved it.** Immediately true, and every phone's price updates.
2. **ConfigButler accepted a commit request.** The receipt says
   `commitRequested`, not `committed`. An *observed* Git commit is still a
   release gate — see [PLAN.md](../PLAN.md). Do not claim more on stage than the
   UI claims.

Then show `demo2/coffee-config.yaml` with the new value, the reason you typed as
the commit message, and whoever made the edit as the author. Re-order the
coffee; it succeeds.

If you want one more, this is the place for it: set the banner to *"Edit this
menu — your change becomes a Git commit in your name"* from the editor. It used
to be the seeded value and it gave demo 2 away during demo 1. Set live, at this
moment, it is a banner announcing the commit that is itself a commit, and every
phone in the room reads it at once.

### 5. The boundary — what is deliberately *not* in Git (2 min)

Switch to the **Orders** tab, next to Config. It is a live feed of what the room
actually ordered, placed and refused.

None of it is in Git. None of it is a Kubernetes object. It is an array in the
pod's memory, the last 100, gone on restart — and the page says so.

This is your closing checklist made concrete, one tab away from its opposite:

> The menu is configuration: low-churn, high-impact, wants review and audit.
> The orders are events: high-frequency, no reviewer, no reconciler. A commit
> per coffee is a history no human will ever read.

Knowing which is which is the skill. That is the honest half of the talk, and
you can point at both halves on one screen.

---

## The closing round — the last commit of the talk

**Clock 38:45.** This is the old demo 2 step 3, moved. It costs nothing here,
because you need the room's attention for the close anyway, and it turns a
bullet list into a live demo.

On the "One last round" slide, go to `/room` and press **Open** on `evaluation`.
Two things happen at once:

- Every phone in the room updates without a refresh, and the round appears.
- `demo1/quiz-configs.yaml` flips `state: closed` → `state: live` **in place**,
  authored by you.

**Show that diff.** A one-line change in a file the room has already seen, made
by pressing a button, attributed to the person who pressed it. It is the last
artifact of the talk and it is the four W's slide proved one more time, in five
seconds, without a slide.

Note which folder it lands in: **`demo1/quiz-configs.yaml`, not `demo2/`.** The
demo2 GitTarget does not claim `quizsessions` — only `coffeeconfigs` and
`quizsubmissions`. If you go looking in the wrong folder on stage you will find
nothing and lose thirty seconds.

Then leave the results screen projected and take questions. Four questions:
`trust`, `first`, `adopt` (0–10, renders as an average to one decimal) and
`missing` (free text).

**It does not move on its own.** `/answer/evaluation/results` fetches once and
offers a "Refresh results" button; there is no stream behind it. Across a
five-minute Q&A that matters more than anywhere else in the talk, because the
screen behind you is the last thing the room looks at. Press Refresh between
questions — it is one click and it is worth the beat. If you would rather not
touch the laptop, say so out loud ("this is a snapshot, let me pull it again")
rather than letting a frozen count look like nobody voted.

**Read two or three of the `missing` answers out loud.** It is a question
channel for everyone in the room who would never raise a hand, and the answers
are your roadmap written by the people who would use it.

**There is no contact question, on purpose.** Real e-mail addresses would be the
only genuine personal data in that repository, they are awkward to remove from a
history, and right now you get to say on stage that every identity in there is
synthetic (`@koudijs.dev.test` — RFC 2606 reserves `.test`, so nobody can ever
register it; Room Pass chose it over `.invalid` because a participant reads the
address on the join page and "invalid" looks like a bug). Keep that property.
Point people at
reversegitops.dev, or just ask them to come and find you.

If a rehearsal left `evaluation` already `live`, there is no button to press:
skip the diff, let them vote, and say the sentence instead. See T-2m — this is
exactly what that check is for.

**Before you leave the stage**, revoke the coffee grant, and let the room watch
you do it. The revoke is a clean `+0/-18` commit, so it proves the switch was
real in both directions:

```bash
kubectl -n voter delete rolebinding voter-audience-coffee-admin
```

---

## The kubectl interludes

### Casting your own answers

You wanted to add a few obviously invented voters. The object is plain:

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: QuizSubmission
metadata:
  # demo1-<display name, lowercased>
  name: demo1-ada-lovelace
  namespace: voter
  labels:
    voter.configbutler.ai/round: demo1
    # Original casing. This becomes results/Ada-Lovelace.yaml in demo2.
    voter.configbutler.ai/submitter: Ada-Lovelace
spec:
  sessionRef:
    group: examples.configbutler.ai
    kind: QuizSession
    name: demo1
  submittedAt: "2026-09-17T10:00:00Z"
  answers:
    # Exactly one answer field per question — the CRD enforces it with CEL.
    - questionId: stack
      singleChoice: Flux
    - questionId: packaging
      singleChoice: Plain YAML
    # approach is multiChoice now — a LIST, not a string. A bare
    # `singleChoice: GitOps` here is rejected by the CRD, live, in front of
    # the room.
    - questionId: approach
      multiChoice:
        - A pull request to a GitOps repo
        - kubectl apply
    - questionId: wish
      freeText: I would like the cluster to write this down for me.
```

**The ids and types must match [`demo-questions.yaml`](demo-questions.yaml).**
Three of them moved when the questions were finalised: the round is `demo1` and
not a dated name, `feedback` became `wish`, and `approach` became `multiChoice`.
Paste the old version and the CEL validation refuses it on stage.

Keep the made-up names visibly made up — Ada Lovelace, Grace Hopper, Alan
Turing. The room enjoys being in on it, and it makes the "one file per person"
layout legible at a glance.

**Do this with your own operator identity**, and say so. You are `github:<you>`,
not `demo:<subject>`, and the Git author on those commits will be you. That is
not a flaw in the demo, it is the demo: the trail records who *actually* did it,
including when that is you cheating.

### Watching it land

```bash
kubectl -n voter get quizsubmissions \
  -l voter.configbutler.ai/round=demo1 \
  --sort-by=.metadata.creationTimestamp
```

---

## One correction to the plan

You wrote: *"I can show the same 3 votes, and they ended up in one commit
because of the CommitRequest."*

The bundling is real, but the mechanism is not the `CommitRequest`. Two
different things:

- **The 5s commit window.** `gitops-reverser` batches changes per target. Three
  votes applied within five seconds of each other land in **one commit** with a
  message like `chore(demo1): 3 changes from github:<you>`. That is what will
  happen when you `kubectl apply -f` three ballots at once.
- **`CommitRequest`.** Created by the voter backend on the coffee **save now**,
  and only there — `CONFIGBUTLER_GIT_TARGET_NAME` is `demo2`. It *closes the
  open window early* so the change lands while you are still standing on stage,
  rather than making you wait it out.

Voting never creates a `CommitRequest`. Saying it does invites exactly the
question you would rather not field.

The good news is that you have two demonstrable behaviours instead of one, and
distinguishing them is more impressive than conflating them:

> Three votes, one commit — that is the window batching. And when I need it
> *now*, on stage, I ask for it — that is the CommitRequest.

Worth noting for the same reason: `commitrequests` are **deliberately not
watched** by any target. The backend creates one per coffee save, so mirroring
them would bury the folder under a document per save.

---

## Failure modes

| Symptom | Likely cause | Do this |
|---|---|---|
| Nobody can join | Room ended, or enrolment closed | `kubectl -n voter get room demo -o yaml` — check `endsAt` and `enrollment` |
| Join works, voting 403s | RoleBinding missing | `kubectl -n voter get rolebinding voter-audience` |
| Phones show stale prices | Stream dropped | The page says so and offers Retry. Prices are server-authoritative; orders are still priced correctly |
| `evaluation` will not open | You are signed in as a participant, not an operator | Re-enter through the GitHub door |
| `evaluation` is already open when the close arrives | A rehearsal opened it and a push did not close it — seeded, not reconciled | Nothing to fix on stage: skip the diff, let them vote. Prevent it at T-2m |
| Nothing reaching Git | Reverser down, or deploy key | `kubectl -n voter logs deploy/gitops-reverser`; check the GitProvider's secret |
| Commit lands with no author | Audit event not matched | Message renders "from an unnamed actor". Mention it and move on — do not debug on stage |
| Voucher still depleted after raising the limit | Order placed before the patch reconciled | Re-order. The limit is read fresh on every order, never cached |
| Order feed empty after a restart | Working as designed | This is the point of step 5 — say so |
| A menu or round edit pushed to Git changed nothing | That object is seeded, not reconciled | Delete it and let Flux recreate it. Not a Flux fault — see "Two repositories" |
| The grant switch is missing from `/room` | You are signed in as a participant | Reading it needs `rolebindings`, which the audience does not have. Re-enter through the GitHub door |
| Switch reports the Role does not exist | `voter-audience-coffee-admin` was not reconciled | `kubectl -n voter get role voter-audience-coffee-admin`. The switch refuses rather than create a binding that grants nobody anything |
| Grant is on but a phone still refuses | That phone has not polled yet | Wait ~5s. The table refreshes itself; there is nothing to press |
| Permission grid is empty or says "incomplete" | An authorizer could not enumerate | The page says so itself. Mention it and move on — the refusals are still real |
| The results bars do not move | Working as designed — the results screen fetches once | Press **Refresh results**. There is no stream behind that page; only the room page updates itself |
| A commit message names nobody | Working as designed since the templates changed | The name is in the commit's Author header. `git show --format=fuller` — see "What the commits say" |
| Save is greyed out in the menu editor | An unresolved conflict — this is the design | Take Theirs or Keep Mine on every red field. `canSave` is false while any conflict is open |
| The conflict never appears in demo 2 step 4 | The second editor changed a different field, or the stream dropped | Different field is not a conflict; that is the point of beat 2. If the banner says reconnecting, press Refresh from cluster and redo the collision |
| "Live updates overtook the read. Refresh again" | A watch arrived mid-refresh | Press Refresh from cluster again. Your draft is retained throughout |
| A voucher vanished after a terminal patch | A merge patch replaced the whole list | `--type=json` with an index, never `--type=merge`, on `spec.vouchers` or `spec.products`. Re-seed if you have lost them: see "Two repositories" |

**The one rule, if anything looks odd with authentication:** nothing may reach
Dex's `room` connector callback except Room Pass. If you find yourself
port-forwarding to Dex on a shared conference network, stop.

---

## Reset between runs

**The rounds are called `demo1` and `evaluation` and they keep those names.** The
old advice — give each run a dated name and let the rename do the cleanup — no
longer applies, and neither does anything downstream of it. Run all four
commands below between every run, **including between a rehearsal and the real
talk**, because nothing resets itself any more:

```bash
# 1. Drop every ballot from both rounds. Submissions are not owned by their
#    round, so nothing goes away on its own. prune: Always means this reaches
#    Git — expect delete commits.
kubectl -n voter delete quizsubmissions -l voter.configbutler.ai/round=demo1
kubectl -n voter delete quizsubmissions -l voter.configbutler.ai/round=evaluation

# 2. Re-seed both sessions. A push does NOT do this: they are ssa: IfNotPresent,
#    so Flux will not apply over an object that exists. This is also how you
#    reset `evaluation` to closed after a rehearsal opened it.
kubectl -n voter delete quizsession demo1 evaluation
flux reconcile kustomization voter-demo

# 3. Confirm what you actually got.
kubectl -n voter get quizsessions   # demo1 live, evaluation closed

# 4. Revoke the coffee grant.
kubectl -n voter delete rolebinding voter-audience-coffee-admin
```

If you would rather not delete the sessions, step 2 has a smaller version that
resets the one field that matters — but it leaves any question edits unapplied:

```bash
kubectl -n voter patch quizsession evaluation --type=merge -p '{"spec":{"state":"closed"}}'
```

On the coffee grant specifically — the next room starts with the permission
demo 1 is supposed to refuse if you skip it:

```bash
kubectl -n voter delete rolebinding voter-audience-coffee-admin
```

The switch on `/room` does the same thing and is the one you will actually
reach from the stage.

The order feed needs no reset — restarting the pod empties it, which is the
only guarantee it makes.

---

## What not to promise

The talk's credibility rests on the last four minutes being honest, so do not
spend it in the first thirty-six:

- **Not** "the commit is observed end to end." The receipt says accepted. Observed
  commit completion is still open work.
- **Not** "this scales to your production write path." One replica, in-memory
  voucher counts, a 5s window. It is a pattern demo on purpose.
- **Not** "the audit trail is tamper-proof." It is signed, and nothing
  reconciles from it. Those are the two claims it can back.

---

## Open questions for the next iteration

- The Git side is only visible on your screen, never on the room's phones.
  Worth a read-only "your commit" link on the thanks screen? It would make the
  attribution personal for 300 people instead of for one projector. **This one
  got sharper, not weaker**: `k8s-audit-trail` is private, so "go and look at
  your own commit afterwards" is not something you can offer today even if
  somebody asks for it.
- The old `confidence` question ("How readable was the mirror on screen?") was
  replaced by `adopt` ("How likely are you to try this in the next six months?"),
  on the reasoning that at the close you want the number that matters and
  readability is something you can ask three people at the coffee break. Both are
  `scale0to10`; swapping back is a one-line change in
  [`demo-questions.yaml`](demo-questions.yaml).
- **A third layer: admission.** Demo 1 shows authentication and RBAC, and the
  interloper beat runs straight into RBAC's one structural limit — it is
  additive, so nothing can be subtracted from `cluster-admin`. A
  `ValidatingAdmissionPolicy` is the layer that *can* say no to an
  administrator, because admission runs after authorization and inspects the
  object as well as the caller. The cluster has none today
  (`kubectl get validatingadmissionpolicies` → none), which is why the vote gate
  lives in the application. Making it real would turn a documented simplification
  into a third demonstrable mechanism — and "here are three different ways to
  authorize someone, and here is what each one cannot do" is a better closing
  than two. Not built; see the trade-offs discussed before building it, because
  the obvious version breaks "Casting your own answers" above.
