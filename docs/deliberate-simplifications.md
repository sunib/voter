# Deliberate simplifications

Voter is a conference demo that has to be **read from the back of a room**. That
goal competes with robustness in a specific way: every defence costs a concept,
and a concept nobody can hold while watching a live demo is worse than the fault
it prevents.

This file is where we write those trades down. Each entry says what we do, what
we gave up, what keeps the gap closed in practice, and how to recover if it bites
anyway. Nothing here is an accident or a TODO — a bug is a bug and gets fixed.
These are the places we looked at a real failure mode and chose not to defend
against it.

The rule for adding an entry: **if you deleted a guard, or declined to write one,
and a reader would otherwise assume it exists, it belongs here.**

---

## 1. A round is identified by its name, not its UID

**What we do.** A `QuizSubmission` carries `voter.configbutler.ai/round` holding
the round's `metadata.name`, and the results page selects on it.

**What we gave up.** A `QuizSession` deleted and recreated under the *same name*
inherits the old round's ballots. They are counted into the new round, and the
people who cast them cannot vote again, because the submission name they would
get is already taken.

Keying on `metadata.uid` prevented exactly that, and nothing else.

**What keeps it closed.** The convention stated in the first line of the round
manifest: **a new `metadata.name` for every session, one per talk.** A round name
carries its date for this reason. The failure needs someone to delete a round and
recreate it with a name they have already used.

**Why we took the trade.** A UID cannot be written down in advance, so everything
that touched a round had to look one up first: the hand-applied
[demo1-b.yaml](../voter/config/demo1-b.yaml) needed an `envsubst` pipeline, the
cleanup command needed a `jsonpath` query, and the round label was unreadable in
a mirror meant to be projected. Keying on the name is also the shape every other
Kubernetes reference has — `spec.sessionRef` was already a name — so there is one
notion of "which round" instead of two.

**Recovery.** Delete the stale ballots. The selector is now typeable:

```console
kubectl -n voter delete quizsubmissions -l voter.configbutler.ai/round=<round-name>
```

**Staleness is not part of this trade.** A browser voting into a round that
changed under it is still refused, by the `resourceVersion` comparison in the
vote handler. A recreated object gets a fresh `resourceVersion` just as an edited
one does, so that check catches both; the UID comparison beside it was redundant.

---

## 2. A submission is named after the voter, not a hash

**What we do.** `submissionName` returns `<round>-<lowercased display name>` —
`demo1-round-2026-09-15-ada-lovelace`. It was a SHA-256 of the round UID and the
opaque Kubernetes subject.

**What we gave up.** Nothing, *as long as one display name is one person*. That
is not a property of this repository.

**What keeps it closed.** Room Pass, deliberately. A `Participant`'s object name
is `p-<folded display name>`, and enrolling under a name that is already taken is
**refused** rather than shared — `internal/server/server.go` says why: *"two
browsers sharing one Participant would be one voter with two ballots."* One
display name is therefore one Participant is one Dex subject, and API-server name
uniqueness still enforces one ballot per person per round.

**What would break it.** Any change that makes Room Pass names non-unique: a
random suffix on the object name, per-room rather than per-namespace uniqueness,
or an admin creating `Participant` objects that share a `displayName`. If you
change enrolment, re-read this entry.

**Why we took the trade.** The submission list is the demo. With Lens open on
`quizsubmissions`, the room watches `demo1-round-2026-09-15-ada-lovelace` arrive
as Ada votes, and everyone can pick out their own ballot. `vote-9f2a…` is a wall
of hex.

**What it makes visible.** Who voted is legible to anyone who can list
submissions, which is every participant — they already hold `get, list, watch`,
so they could always read every ballot; the hash only obscured whose it was. The
mirrored audit trail files each vote under the same name, so this reveals nothing
Git would not. It is still a choice, now made in two places.

---

## 3. Typed names are folded, not rejected

**What we do.** Room Pass stores the *folded* form of what a participant types:
NFKD, diacritics folded, everything outside `[A-Za-z0-9]` collapsed to a single
`-`, capped at 40. "Ada Lovelace" is stored as `Ada-Lovelace`; "Renée O'Hara"
becomes `Renee-O-Hara`.

**What we gave up.** A name in a script with no ASCII fold — `日本語`, an
emoji-only name — cannot be used at all. It is refused at the join form with
*"Choose a name with at least one letter or number."*

**Why we took the trade.** That one value has to be legal in three places at
once: a Kubernetes label value on every ballot, one path segment in the mirrored
audit trail, and the tail of a submission's object name. The intersection of
those rules is roughly `[A-Za-z0-9-]`. The alternative — rejecting every name
with a space — would have a room of fifty people fighting a form, and an illegal
value does not fail gently: the API server refuses the ballot's `Create` and the
participant simply cannot vote.

Folding at the boundary means there is **one** name, legal everywhere, and no
caller downstream has to know the rules. `Participant.spec.displayName` carries a
CRD pattern that is exactly what the fold emits, so a hand-written object cannot
reintroduce the problem.

**The invariant to preserve.** `strings.ToLower(labelName(x)) == participantID(x)`
— asserted in `TestLabelNameLowercasesToParticipantID`. The submission name is
built by lowercasing the stored display name, so if those two folds ever diverge
a ballot silently points at a participant who does not exist.

---

## 4. Voting is refused by the application, not by RBAC

**What we do.** The vote handler refuses any session whose Dex connector is not
`room`. An operator signed in through the `github` connector gets a `403` with
code `NotAParticipant`, while the same session can still open and close rounds
and read results.

**What we gave up.** This is **not a security boundary.** The demo operator is
bound to `cluster-admin`, so the API server has no objection whatsoever to that
identity creating a `QuizSubmission` directly.

**Why that is fine, and in fact the point.** The gap is a demo beat rather than a
leak: the application has a rule, Kubernetes has a different one, and
[demo1-b.yaml](../voter/config/demo1-b.yaml) walks through it on purpose with
`kubectl`. Saying that out loud is a better story than either half alone. The
participant-facing grant is the real boundary and is unchanged: the
`voter-audience` Role grants `create` on `quizsubmissions` to
`demo:voter-audience` and to nobody else.

**Why the rule exists at all.** An operator's `claims.name` is a GitHub profile
name that Room Pass never folded, and it routinely contains a space. Without the
gate it would be written as a label value and the API server would refuse the
ballot with a `422` nobody in the room could read. Refusing the vote is less code
than repairing the name, and it is a truer rule.

---

## 5. The config mirror commits every Secret in its namespace

**What we do.** The `gitops-reverser-config` target mirrors `v1/secrets` from the
`voter` namespace, which is every Secret there — not only ConfigButler's deploy
key and signing key, but the app's cookie keys, the OIDC client secret, and the
cert-manager TLS secret.

**What we gave up.** Narrow scope. A `WatchRule` selects by operation, group,
version and resource; there is **no** name selector and no label selector, so
"these two Secrets" is not expressible. The alternative was moving the
credentials to their own namespace, which — because `gitProviderRef` is a
namespace-local reference — means a second `GitProvider` and a duplicated deploy
key.

**What keeps it closed.** Encryption, and nothing else. Every one of them takes
the `{sensitiveSuffix}` route and lands as `.sops.yaml`.

**What to keep in mind.** The TLS secret rotates on cert-manager's schedule, so
that folder gains a commit nobody made at renewal. And because encryption is the
only boundary here, the age key is a credential worth protecting like one — see
the next entry.

---

## 6. ConfigButler generates its own age key, in the namespace it mirrors

**What we do.** The config target sets `generateWhenMissing: true`, so the
operator mints the age identity itself on first reconcile and stores it in
`voter/gitops-reverser-age`. There is no manual `age-keygen` step before the
demo can write anything.

**What we gave up.** That Secret lives in the namespace the same target mirrors,
so a copy of it is committed — encrypted to its own public half. It is not
readable without the key that is inside it, so this is **not a disclosure**; it
is a file nobody can decrypt. What it costs is **disaster recovery**: Git holds
no usable copy of the key, so losing the cluster makes every `.sops.yaml` in
that folder permanently unreadable.

The alternative is generating the pair off-cluster and listing only
`recipients.publicKeys`, which keeps the private half out of the cluster
entirely and needs no backup step — at the cost of a manual bootstrap before the
target can write at all.

**What keeps it closed.** A one-time backup, which the operator asks for rather
than leaving to memory: it stamps a backup-required annotation on the Secret and
logs `ENCRYPTION KEY BACKUP REQUIRED` on every reconcile until the annotation is
removed. Do it once, after the first reconcile, and the gap is gone:

```console
kubectl -n voter get secret gitops-reverser-age -o jsonpath='{.data}' \
  | jq -r 'to_entries[] | select(.key|endswith(".agekey")) | .value' | base64 -d
```

**Recovery.** There is none if the key is lost and the cluster is gone. That is
the whole reason the annotation exists, and why this entry does.

See [reverse-gitops-plan.md](./reverse-gitops-plan.md) for the target
configuration itself.

---

## Things that are *not* simplifications

Worth stating, because they look like candidates and are not:

- **The participant's own token does every write.** No impersonation, no shared
  ServiceAccount, no application-side permission model. That is what makes the
  audit event — and therefore the Git commit author — honest, and it is load
  bearing rather than convenient.
- **`prune: Always` on the demo targets.** A deleted object leaves Git. Chosen so
  the mirror cannot become a liar on a projector, accepting that a bad watch
  scope could delete manifests.
- **The session cookie is versioned.** Adding a field bumps the version so stale
  cookies become a clean re-login rather than a subtly wrong session.
