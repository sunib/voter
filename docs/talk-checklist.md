# Talk checklist — where authorization actually lives

The closing segment, and the answer to the two questions this talk reliably
gets: *"so the app decides?"* and *"what stops you, the admin?"*

[demo-runbook.md](demo-runbook.md) is the choreography. This file is the
argument you make once the demos are over, plus the honest limits of each
mechanism. Nothing here is aspirational — where the demo does something weaker
than the slide, it says so.

---

## The three layers

A request to the API server passes through three gates, in this order, and they
answer different questions. Most rooms have only ever thought about the middle
one.

| Layer | Answers | Sees | Cannot |
|---|---|---|---|
| **Authentication** | Who are you? | The token | Say anything about what you may do |
| **Authorization (RBAC)** | May *this identity* do *this verb* on *this kind*? | User, groups, verb, resource, namespace, object *name* | Look inside the object. **Subtract anything** — RBAC is additive and has no deny rule |
| **Admission** | Is *this object*, from *this caller*, acceptable? | The whole object, old and new, plus `request.userInfo` | Touch reads. Admission runs on writes only. Be enumerated in advance — nothing can list it for you |

The ordering matters and is worth saying out loud: **admission runs after
authorization**, on a request RBAC has already allowed. It is not a second
opinion on the same question; it is a different question about an object that
now exists in the request body.

---

## Which layer produced each refusal in the demo

Have this straight before you walk on. Since 2.0.0 (2026-10-06) every one of
these is Kubernetes: the page writes through krm-foyer's `/k8s` with the
person's own token, and Voter has no vote handler left to hold a rule. Being able
to say which *part* of the API server refused is the point.

| The refusal the room sees | Produced by | Mechanism |
|---|---|---|
| A participant cannot open or close a round | **kube-apiserver** | No `patch` on `quizsessions` in the audience Role |
| A participant cannot edit the coffee menu | **kube-apiserver** | No `patch` on `coffeeconfigs` until you grant it |
| A participant cannot delete the menu | **kube-apiserver** | No `delete`, ever |
| A participant cannot vote twice | **kube-apiserver** (admission + name) | `voter-ballot` makes the ballot's name `<round>-<display name>`; the second create is a 409, so two tabs still make one vote |
| A participant cannot vote in somebody else's name | **kube-apiserver** (admission) | `voter-ballot` checks the name and labels against the token's display name, which no request can set |
| A participant cannot vote after the round closes | **kube-apiserver** (admission) | `voter-ballot`: "This round is not open for voting." |
| A participant cannot relabel or annotate the menu, even with the grant | **kube-apiserver** (admission) | `voter-editable-spec`: a person may change only `spec` |
| The grant switch is absent from a participant's `/room` | **kube-apiserver** | No `get` on `rolebindings`, so the page has nothing to render |
| **An operator signed in with GitHub cannot vote quietly** | **kube-apiserver** (admission) | `voter-ballot`: a ballot from anyone but a `demo:` user must carry `voter.configbutler.ai/cast-by: operator` |

That last row is the one the room will poke at. The operator is bound to
`cluster-admin`, and RBAC has no objection to their ballot. Admission does: the
operator may cast one, but only by labelling it as theirs, and the label lands
in Git with the commit. The page does not offer the operator a vote at all; that
is courtesy, the policy is the control.

---

## The structural point: RBAC only adds

This is the sentence to land, and it surprises people who have used Kubernetes
for years:

> There is no rule you can write that takes something away from an
> administrator. RBAC has no `deny`. Every Role and every binding is additive,
> and permissions are the union of everything that matches you.

So the question *"what stops you?"* has no RBAC answer. If the identity holds
`*` on `*`, that is the end of the conversation at this layer, and nothing about
ordering, specificity or a more precise Role changes it.

The corollary is the useful part: **if you want a rule that binds the
administrator too, it cannot be a permission.** It has to be a statement about
the object, enforced somewhere the administrator does not sit above — and that
is admission.

---

## The admission layer, and what this demo uses it for

Two shapes, same position in the request path:

- **`ValidatingAdmissionPolicy`** — CEL expressions evaluated in-process by the
  API server. No server to run, no certificate to rotate, no availability risk
  you own. This is the one to reach for first.
- **`ValidatingWebhookConfiguration`** — your own HTTPS service, called per
  request. Arbitrary logic, at the cost of running something on the write path
  of your cluster. `failurePolicy: Fail` means an outage of your webhook is an
  outage of the resource it guards; `Ignore` means your rule is advisory.

**This cluster runs two policies, no webhooks**: `kubectl get
validatingadmissionpolicies` lists `voter-ballot` and `voter-editable-spec`
(sources in [`voter/config/admission/`](../voter/config/admission/)). Both match
people only — `system:` users and service accounts are not voters and not
editors.

`voter-ballot` is the declared-operator version, not the flat one. A flat *"only
`demo:` identities may create ballots"* would also refuse your own seeded Ada
Lovelace and Grace Hopper, the "Casting your own answers" interlude in the
runbook. Instead an operator may cast a ballot, but it must carry
`voter.configbutler.ai/cast-by: operator`, and the interlude ballots in
[`demo1-b.yaml`](../voter/config/demo1-b.yaml) do. The trail is *more* honest
for it:

> I can still stuff the ballot box, I just cannot do it quietly.

What it costs, said plainly:

- Both policies are `failurePolicy: Fail`. A policy that errors takes voting, or
  menu saves, out for the whole room — closed, not open. That was the price of
  moving the rules off Voter's handlers, where anyone with a terminal could
  always walk around them.
- A ballot that names a round which does not exist passes `voter-ballot`
  vacuously. Nothing counts it — the tally only counts rounds it tallies — but
  it is a gap, not a rule.
- The admission policies have no envtest suite in CI yet; the evidence is the
  browser fixture's `boundaries.spec.js`.

---

## The blind spot nobody asks about, so you should raise it

The permission table on `/me` is a `SelfSubjectRulesReview`: the API server
enumerating what a token may do. It is complete for RBAC and **silent about
admission**.

That is not a bug in the page, it is a property of the layer. RBAC is a set of
grants and can be listed; admission is code that runs against an object that
does not exist yet, and there is no API that answers *"would this be admitted?"*
without submitting it. `kubectl auth can-i` has the same limit.

So: a permission table can promise you *may*, and admission can still say no.
Worth thirty seconds, because it is the honest edge of the nicest screen in the
talk — and this demo has a live example: with the menu grant on, `/me` says the
room may `patch` the CoffeeConfig, and `voter-editable-spec` still refuses a
patch that touches a label.

---

## Questions you will get

**"Doesn't the app still decide, since it holds the token?"**
The app does not hold the token. krm-foyer keeps the session and passes the
person's own token to the API server on `/k8s`; the page builds the request and
renders the answer. Every refusal in the table above is an API server response
the app could not have produced. The page can choose *not to ask* — it hides the
vote from the operator — and that is the honest limit of the claim.

**"What stops a participant calling the API directly with their token?"**
Nothing, and that is the design. Their RBAC is the same whether the request
comes from the page or from `curl`. The page is a convenience over a credential
the person genuinely holds.

**"What stops you?"**
At the RBAC layer, nothing — see above. At admission, nothing stops me either,
but `voter-ballot` makes me say so: my ballot carries `cast-by: operator`, and so
does its commit. And as cluster-admin I could delete the policy — through Git,
where that too is a commit.

**"Could an attendee's commit get reconciled back into the cluster?"**
No. `k8s-audit-trail` is not a Flux source. See "Two repositories" in the
runbook.

---

## What not to claim

- **Not** "the app cannot refuse you." The page hides what you may not do
  (the operator's vote, the grant switch); the refusals that count are the API
  server's.
- **Not** "RBAC prevents the admin from voting." It cannot. Nothing in RBAC
  constrains an administrator.
- **Not** "the permission table shows everything that could refuse you." It
  shows RBAC. Admission is invisible to it, here and everywhere.
- **Not** "admission stops the admin." It makes the admin's ballot declare
  itself; cluster-admin can still change the policy, through Git.
