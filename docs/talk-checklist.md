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

Have this straight before you walk on. Three of these are Kubernetes; one is
not, and being caught claiming otherwise costs you the room.

| The refusal the room sees | Produced by | Mechanism |
|---|---|---|
| A participant cannot open or close a round | **kube-apiserver** | No `patch` on `quizsessions` in the audience Role |
| A participant cannot edit the coffee menu | **kube-apiserver** | No `patch` on `coffeeconfigs` until you grant it |
| A participant cannot delete the menu | **kube-apiserver** | No `delete`, ever |
| A participant cannot vote twice | **kube-apiserver** | One `QuizSubmission` per identity per round; the create is atomic, so two tabs still make one vote |
| The grant switch is absent from a participant's `/room` | **kube-apiserver** | No `get` on `rolebindings`, so the page has nothing to render |
| **An operator signed in with GitHub cannot vote** | **the application** | The vote handler checks the Dex connector and returns its own 403 |

That last row is [deliberate simplification #4](deliberate-simplifications.md).
The operator is bound to `cluster-admin`, so the API server has no objection to
the same write made with `kubectl` — and the reason for the rule is mundane
rather than security: a GitHub profile name has never been folded by Room Pass,
usually contains a space, and would be refused as a label value with a 422 that
means nothing to anyone watching.

Say it rather than let it be discovered. The honest version is a better beat
than the overclaim, because it sets up the structural point below.

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

## The admission layer, and why this demo does not use it

Two shapes, same position in the request path:

- **`ValidatingAdmissionPolicy`** — CEL expressions evaluated in-process by the
  API server. No server to run, no certificate to rotate, no availability risk
  you own. This is the one to reach for first.
- **`ValidatingWebhookConfiguration`** — your own HTTPS service, called per
  request. Arbitrary logic, at the cost of running something on the write path
  of your cluster. `failurePolicy: Fail` means an outage of your webhook is an
  outage of the resource it guards; `Ignore` means your rule is advisory.

**This cluster has neither** — `kubectl get validatingadmissionpolicies` returns
nothing. That is why the vote gate is in the application, and the checklist
above has one row that says "the application".

What it would buy, in one line each:

- The operator's ballot could be refused, or required to *declare itself*, by
  Kubernetes rather than by the app.
- The demo would show three mechanisms instead of two, and *"here is what each
  one cannot do"* is a better closing than *"here is what we used."*

What it costs, and why it is not built:

- A policy scoped wrong, or a CEL expression that errors, takes voting out for
  the whole room. `failurePolicy` decides whether that fails open or closed, and
  neither is a comfortable thing to discover on stage.
- **The obvious version breaks the demo.** A flat *"only `demo:` identities may
  create ballots"* also refuses your own seeded Ada Lovelace and Grace Hopper,
  which is the "Casting your own answers" interlude in the runbook.
- The more interesting version — an operator may cast a ballot, but it must
  carry a label saying so — keeps the interlude and makes the trail *more*
  honest. `I can still stuff the ballot box, I just cannot do it quietly.` That
  is the one worth building, and it is worth building calmly.

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
talk.

---

## Questions you will get

**"Doesn't the app still decide, since it holds the token?"**
It holds the token and spends it; it does not evaluate anything. Every refusal
in the table above except one is rendered from an API server response the app
could not have produced. The app can choose *not to ask* — which is exactly what
the vote gate does — and that is the honest limit of the claim.

**"What stops a participant calling the API directly with their token?"**
Nothing, and that is the design. Their RBAC is the same whether the request
comes from the page or from `curl`. The page is a convenience over a credential
the person genuinely holds.

**"What stops you?"**
At the RBAC layer, nothing — see above. Name admission as the answer and say
this cluster does not use it yet.

**"Could an attendee's commit get reconciled back into the cluster?"**
No. `k8s-audit-trail` is not a Flux source. See "Two repositories" in the
runbook.

---

## What not to claim

- **Not** "authorization is entirely Kubernetes'." One refusal is the
  application's, and it is documented.
- **Not** "RBAC prevents the admin from voting." It cannot. Nothing in RBAC
  constrains an administrator.
- **Not** "the permission table shows everything that could refuse you." It
  shows RBAC. Admission is invisible to it, here and everywhere.
- **Not** "we use admission control." This cluster has no policy and no webhook.
