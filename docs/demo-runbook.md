# Demo runbook — "GitOps Needs an API"

Swiss Cloud Native Day 2026. 45 minutes, two live demos, a room full of phones.

This is the operator's copy: what to press, what the room sees, what breaks and
what to do about it. It is also the answer to "what are we building for" — if a
feature is not in here, it is not in the talk.

Read [ARCHITECTURE.md](../ARCHITECTURE.md) for how it works and
[authorization.md](authorization.md) for who may do what. This file is the
choreography.

---

## The shape of the 45 minutes

| Minutes | Beat | Room's phones |
|---|---|---|
| 0–8 | The problem: business intent ends up in Git, stakeholders do not do pull requests | idle |
| 8–17 | **Demo 1** — identity, authorization, and a Kubernetes object you did not expect | **in use** |
| 17–25 | The pattern: a typed API in front of the repo | idle |
| 25–40 | **Demo 2** — Git, conflict, and the one place the room works together | **in use** |
| 40–45 | The honest checklist: where this fits, where it does not | idle |

Two phone segments, eight minutes apart, is about right. A room will pick a phone
up twice; it will not pick it up four times.

**The first eight minutes talk about Git, and that is fine.** What is being held
back is not the word. It is the evidence that *this cluster has been writing to a
repository since they scanned the code*. Naming the problem — intent ends up in
a repo that the people who own the intent cannot reach — is the setup for both
demos and gives nothing away. "The secret" below draws the line.

The arithmetic, honestly: demo 1's steps sum to nine minutes with the interloper
dropped, demo 2's to fifteen. That is the allocation above, and it leaves the
closing its full five. **The interloper (demo 1 step 4) is the slack** — it is
marked optional and this is the second time this runbook has spent it. Put it
back only if you walk on early. If you are still over on stage, demo 2 step 2
survives being told in ninety seconds.

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
next reconcile reverts it, or you press Open on round two and Flux closes it
again while the room is still voting. The reconcile interval is 10 minutes —
comfortably inside a 45-minute talk.

Two consequences, both of which bite at pre-flight rather than on stage:

- **Editing those specs in Git no longer changes a cluster where the object
  exists.** Changing a price in `app.yaml` and pushing does nothing. To re-seed,
  delete the object and let Flux recreate it:
  `kubectl -n voter delete coffeeconfig demo-coffee`
- **Renaming a round is unaffected.** A new name is a new object, so it is
  created from Git as normal and `prune` removes the old one. This is a second
  reason to rename rather than reuse — a re-run under the *same* name will not
  have its `state` reset, because Flux no longer touches it.

**Do not verify this by looking for the annotation on the live object.** Flux
reads it from the manifest it is about to apply, sees the object already exists,
and skips — so it never writes it. `kubectl get coffeeconfig demo-coffee -o yaml`
shows no annotation, and that absence is the proof it is working. Verify by
behaviour instead: patch a field, `flux reconcile kustomization voter-demo`, and
check the value survived.

---

## Pre-flight

### T-24h

- [ ] **Rename both rounds.** `demo-round.yaml` ships
      `demo1-round-<date>` / `demo2-round-<date>`. Choose a new name for *this
      talk*, not this topic. A reused name means the room that voted last time
      cannot vote again, this talk's votes land in the previous talk's file, and
      — because ballots are selected by round *name*, not UID — a recreated
      round **inherits the old round's votes**. The file's own header explains
      all three; do not skip it. To write new questions rather than rename old
      ones, [quizsession-authoring.md](quizsession-authoring.md) is the field
      reference — every constraint, the five question types, and what each one
      looks like on the projector.
- [ ] Round one `state: live`, round two `state: closed` (not `draft` — a draft
      is filtered out of the room page entirely and there is no button to press).
      **Check this, do not assume it.** A rehearsal leaves round two `live`, and
      then demo 2 step 3 has nothing to open and the room has already voted in
      it: `kubectl -n voter get quizsessions` prints the state of both.
- [ ] Push and let Flux reconcile. Confirm both rounds exist:
      `kubectl -n voter get quizsessions`
- [ ] **If you changed the coffee menu**, pushing is not enough — the
      CoffeeConfig is seeded, not reconciled. Delete it and let Flux recreate
      it: `kubectl -n voter delete coffeeconfig demo-coffee`. Same for a round
      whose `state` you edited without renaming it. See "Two repositories".
- [ ] Confirm the audit-trail repo is reachable and the previous talk's folders
      are either cleaned or clearly dated.

### T-30m

- [ ] `kubectl -n voter get pods` — voter and room-pass ready, one replica each.
      **One replica is load-bearing**: voucher counts and the order feed are
      per-process.
- [ ] Open `https://demo.koudijs.dev/auth/login?connector=github&return=%2Froom`
      on the presenter machine. That is the operator door; it puts you at
      `/room` with the rotating QR.
- [ ] Join from your own phone as a participant. Vote in round one. This proves
      the whole chain and gives the room's first file a neighbour.
- [ ] `git -C <audit-trail> pull` and confirm your test vote arrived in
      `clusters/k8s.koudijs.dev/demo1/submissions.yaml`.
- [ ] Delete the test vote so the room starts clean:
      `kubectl -n voter delete quizsubmissions -l voter.configbutler.ai/round=<round-1-name>`
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

---

## Demo 1 — who you are, who decides what you may do, and what a vote actually is

**Nine minutes. Git is not mentioned once.** See "The secret" below.

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

Round one is live. They answer "How do you change Kubernetes configuration
today?" Project the results screen and let the bars fill. That is the warm-up,
and it earns the room's attention for what follows.

Then go to the terminal while the bars are still fresh:

```bash
kubectl -n voter get quizsubmissions
kubectl -n voter get quizsubmission <round>-<somebody> -o yaml
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

### 3. Show what they *cannot* do (4 min) — the core of the authorization half

This is the part worth rehearsing, because every refusal below is a **real API
server 403**, rendered verbatim. There is no client-side permission model to
give the game away.

| Try this | What happens | Why |
|---|---|---|
| Vote twice in round one | "You have already voted in this round." | One `QuizSubmission` per identity per round, named `<round>-<name>`. The create is atomic, so two tabs still make one vote. |
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

### 4. The interloper (2 min, optional but strong)

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

If you would rather not open that door mid-demo, skip this beat entirely. It is
optional, and it costs you the two minutes the closing checklist wants back.

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

**Fifteen minutes.** The room is still signed in from demo 1; do not make them
join again.

Two arguments, in this order. The first is the reveal: everything they have
already done is in a repository, attributed to them by name, and not one of them
has a GitHub account. The second answers the question the first one provokes in
any engineer in the room — *if Git is the record, what happens when two people
change the same thing?* — and its answer is the thesis of the talk. **Git is the
record. The Kubernetes API is where you work together.** Step 5 is where that
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

### 2. The same objects, filed two ways (2 min)

Now open `clusters/k8s.koudijs.dev/demo2/results/`. One file per person, named
after them, and every round they vote in appends a document to their file.

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

### 3. Open round two, live (2 min)

From `/room`, press Open on round two. Two things happen at once:

- Every phone in the room updates without a refresh, and the round appears.
- `demo1/quiz-configs.yaml` flips `state: closed` → `state: live` **in place**,
  authored by you.

Show that diff. A one-line change in a file the room has already seen, made by
pressing a button, attributed to the person who pressed it.

Let them vote. Round two is the "would you actually run this" round, which gives
you your closing material for free.

### 4. Grant the room the admin page (2 min)

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

### 5. Two people, one menu (5 min)

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
change the same field — a volunteer, now that step 4 gave them the permission,
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

Hand the whole thing to the room if the grant from step 4 is on — it is their
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

### 6. The boundary — what is deliberately *not* in Git (2 min)

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

## The kubectl interludes

### Casting your own answers

You wanted to add a few obviously invented voters. The object is plain — and it
has to agree with the round exactly, which
[quizsession-authoring.md](quizsession-authoring.md) spells out:

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: QuizSubmission
metadata:
  # <round>-<display name, lowercased>
  name: demo1-round-2026-09-15-ada-lovelace
  namespace: voter
  labels:
    voter.configbutler.ai/round: demo1-round-2026-09-15
    # Original casing. This becomes results/Ada-Lovelace.yaml in demo2.
    voter.configbutler.ai/submitter: Ada-Lovelace
spec:
  sessionRef:
    group: examples.configbutler.ai
    kind: QuizSession
    name: demo1-round-2026-09-15
  submittedAt: "2026-09-15T10:00:00Z"
  answers:
    # Exactly one answer field per question — the CRD enforces it with CEL.
    - questionId: approach
      singleChoice: GitOps
    - questionId: feedback
      freeText: I would like the cluster to write this down for me.
```

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
  -l voter.configbutler.ai/round=<round-name> \
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
| Round two will not open | You are signed in as a participant, not an operator | Re-enter through the GitHub door |
| Nothing reaching Git | Reverser down, or deploy key | `kubectl -n voter logs deploy/gitops-reverser`; check the GitProvider's secret |
| Commit lands with no author | Audit event not matched | Message renders "from an unnamed actor". Mention it and move on — do not debug on stage |
| Voucher still depleted after raising the limit | Order placed before the patch reconciled | Re-order. The limit is read fresh on every order, never cached |
| Order feed empty after a restart | Working as designed | This is the point of section 6 — say so |
| A menu or round edit pushed to Git changed nothing | That object is seeded, not reconciled | Delete it and let Flux recreate it. Not a Flux fault — see "Two repositories" |
| The grant switch is missing from `/room` | You are signed in as a participant | Reading it needs `rolebindings`, which the audience does not have. Re-enter through the GitHub door |
| Switch reports the Role does not exist | `voter-audience-coffee-admin` was not reconciled | `kubectl -n voter get role voter-audience-coffee-admin`. The switch refuses rather than create a binding that grants nobody anything |
| Grant is on but a phone still refuses | That phone has not polled yet | Wait ~5s. The table refreshes itself; there is nothing to press |
| Permission grid is empty or says "incomplete" | An authorizer could not enumerate | The page says so itself. Mention it and move on — the refusals are still real |
| Save is greyed out in the menu editor | An unresolved conflict — this is the design | Take Theirs or Keep Mine on every red field. `canSave` is false while any conflict is open |
| The conflict never appears in demo 2 step 5 | The second editor changed a different field, or the stream dropped | Different field is not a conflict; that is the point of beat 2. If the banner says reconnecting, press Refresh from cluster and redo the collision |
| "Live updates overtook the read. Refresh again" | A watch arrived mid-refresh | Press Refresh from cluster again. Your draft is retained throughout |
| A voucher vanished after a terminal patch | A merge patch replaced the whole list | `--type=json` with an index, never `--type=merge`, on `spec.vouchers` or `spec.products`. Re-seed if you have lost them: see "Two repositories" |

**The one rule, if anything looks odd with authentication:** nothing may reach
Dex's `room` connector callback except Room Pass. If you find yourself
port-forwarding to Dex on a shared conference network, stop.

---

## Reset between runs

If you give this talk twice in a day, use a new round name per run (`-afternoon`
suffix) rather than deleting. If you must clean:

```bash
kubectl -n voter delete quizsubmissions -l voter.configbutler.ai/round=<old-round>
```

Submissions are not owned by their round, so renaming the round alone leaves
them behind. `prune: Always` means the delete reaches Git too.

Renaming also sidesteps the seeded-object rule: a new round name is a new
object, so Flux creates it from Git with the `state` you asked for. Reusing a
name does not reset anything, because Flux no longer applies over an object that
already exists.

Revoke the coffee grant between runs, or the next room starts with the
permission demo 1 is supposed to refuse:

```bash
kubectl -n voter delete rolebinding voter-audience-coffee-admin
```

The switch on `/room` does the same thing and is the one you will actually
reach from the stage.

The order feed needs no reset — restarting the pod empties it, which is the
only guarantee it makes.

---

## What not to promise

The talk's credibility rests on the last seven minutes being honest, so do not
spend it in the first thirty-eight:

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
  attribution personal for 300 people instead of for one projector.
- Round two's `confidence` question ("How readable was the mirror on screen?")
  is scored 0–10 by a room that has just watched demo 2. That is a live audience
  metric for the talk itself — consider showing it during the closing checklist.
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
