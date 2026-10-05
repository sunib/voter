# The Voter demo at Swiss Cloud Native Day, in numbers

**Thursday 17 September 2026.** What the room did, counted from the records the
demo left behind. For what went wrong and why, see the post-mortem,
[post-demo-2026-09-17.md](post-demo-2026-09-17.md).

All times are local (CEST, UTC+2). The underlying records are in UTC, two hours
earlier.

## The headline numbers

| | |
|---|---|
| People who signed in | **66** (plus the presenter) |
| Time for the whole room to get in | **75 seconds**: 11:43:47 to 11:45:02 |
| Commits in the demo hour (11:43 to 12:43) | **110** |
| of which authored by audience members | **105**, by **43** different people |
| Ballots in the opening round | **38** |
| Saves to the shared coffee menu | **26** in six minutes: 25 from the audience, by 17 people, plus 1 from the presenter |
| Coffee orders | about **60** |

Every audience commit carries the name of the person who clicked, taken from
the identity the API server authenticated, not from anything the app wrote down.

## Where the numbers come from

- **Sign-ins**: the `Participant` objects that Room Pass creates on enrolment,
  one per person, still in the `voter` namespace. The room's own
  `status.participantCount` is 70. That count includes the presenter's two
  enrolments and two test enrolments made after the talk.
- **Commits**: the history of
  [ConfigButler/k8s-audit-trail](https://github.com/ConfigButler/k8s-audit-trail),
  which gitops-reverser wrote while the talk ran.
- **Ballots and answers**: the `QuizSession` status tally, cross-checked
  against the audit trail.
- **Coffee orders**: the voter pod's log, as read for the post-mortem on 21
  September. Orders are not mirrored to Git, and that pod has since been
  replaced, so this is the one number that can no longer be recounted.

## Timeline

| Local time | What happened | Commits |
|---|---|---|
| 11:43:47 to 11:45:02 | 66 people enrol: 16 in the first 13 seconds, 49 in the next minute | none (enrolment is not mirrored) |
| 11:44:55 | Presenter opens round `demo1` | 1 |
| 11:45:30 to 11:48:39 | **Voting.** 38 ballots | 76 |
| 11:58 to 11:59 | About 60 coffee orders | none (orders are not mirrored) |
| 12:00:17 | Presenter makes the first save to the coffee menu | 1 |
| 12:02:04 | Presenter gives the whole room edit rights on the menu | 1 |
| 12:02:13 to 12:06:25 | **The room edits the menu.** 25 saves | 25 |
| 12:07:03 | Presenter files two database requests | 1 |
| 12:12:44 | Presenter opens round `evaluation` | 1 |
| 12:13:37 to 12:13:51 | 2 ballots | 4 |

**Total in the demo hour: 110 commits.** For the whole day, it was 115. The
other 5 are setup and rehearsal from 11:06 to 11:35.

Each ballot produces two commits, because gitops-reverser mirrors a
`QuizSubmission` into two places: the round's `submissions.yaml`, and a
per-person file under `demo2/results/`. So 38 ballots account for 76 commits.
Counted as distinct changes made by the audience, the hour contains 40 ballots
and 25 menu saves: **65 audience actions**.

### How fast it happened

Ballots per minute in the opening round:

| 11:45 | 11:46 | 11:47 | 11:48 |
|---|---|---|---|
| 7 | 19 | 10 | 2 |

Menu saves per minute:

| 12:02 | 12:03 | 12:04 | 12:05 | 12:06 |
|---|---|---|---|---|
| 11 | 10 | 3 | 0 | 1 |

In the busiest minutes the menu took a save every five or six seconds, and
three pairs of saves landed in the same second. All of them went to one object.

## Who took part, and how far they got

| Stage | People | Share of those who signed in |
|---|---|---|
| Signed in | 66 | 100% |
| Wrote at least one commit | 43 | 65% |
| Voted in the opening round | 38 | 58% |
| Saved the coffee menu | 17 | 26% |
| Did both | 12 | 18% |

**23 people signed in and never got a single change into Git.** Some will simply
not have tried. But the gap between 66 sign-ins and 38 ballots is where
[defect 1](post-demo-2026-09-17.md#defect-1-a-subset-of-the-room-could-not-vote)
landed: phones that sat on the question screen were refused, and an unknown
number of their owners gave up rather than reloading. The app logged no
refusals at the time, so there is no way to split the 28 missing ballots into
"did not try" and "tried and was refused". That logging now exists.

One of the free-text answers that did get through was *"not responsive 😭😭"*.

The most active editor saved the menu four times, each with the commit message
`localizations`. Five of the 25 audience saves carried a message the person
wrote themselves, and the other 20 used the default. The audience names ranged
from `Verstappen` and `cthulu` to `drop-table-where-1-1`, and that one was
stored as a plain display name and committed like every other.

## What the room answered

### Round 1: "How do you change Kubernetes configuration today?" (38 ballots)

**Argo CD or Flux?**

| Argo CD | Flux | Both | Neither yet |
|---|---|---|---|
| 28 (74%) | 5 (13%) | 2 (5%) | 3 (8%) |

**Helm or Kustomize?**

| Helm | Both | Plain YAML | Kustomize |
|---|---|---|---|
| 17 (45%) | 14 (37%) | 5 (13%) | 2 (5%) |

**How do you change configuration today?** (pick all that apply)

| Answer | People | Share |
|---|---|---|
| A pull request to a GitOps repo | 32 | 84% |
| `kubectl apply` | 8 | 21% |
| A web UI or portal | 7 | 18% |
| I ask someone in Slack or a ticket | 5 | 13% |
| Someone else does it entirely | 5 | 13% |

**"In one line: what do you wish people could self-service?"** (15 answers)

"Everything" came up five times, in various capitalisations. The rest:
*Databases and persistent storage*, *cluster bootstrapping*, *GitHub Apps*,
*Coffee*, *Happiness*, *New C-Level*, *Gitops is a lie*, and *not responsive
😭😭*, plus two test entries.

So this was a GitOps-literate room. Five out of six already change configuration
through a pull request, and three quarters of them run Argo CD. The talk was
arguing for the reverse direction, cluster to Git, in front of people who
already do Git to cluster every day.

### Round 2: "Now that you have seen it — would you run this?" (2 ballots)

Only two people voted, both in the same fifteen seconds. Both picked *"Yes, with
signed commits and a repo nothing reconciles from"*. Two ballots is not a
result. Most likely the talk ran out of time before the room got to this round.

**This round is still open.** Its `spec.state` is `live`, and its tally was
still being rewritten this morning. Close it before the next demo, or its
ballots will mix with the next room's.

## What the numbers say about the demo itself

- **Enrolment works at conference scale.** 66 people got a real Kubernetes
  identity in 75 seconds, well inside the room's cap of 300.
- **The audit trail held up under a crowd.** 105 audience commits, each with the
  right human as author, including three same-second collisions on a single
  object. gitops-reverser was not the bottleneck anywhere.
- **Voting lost about four in ten of the room.** Of the 66 who signed in, 38
  voted (58%). A demo that turns the audience into participants should get most
  of them to cast the first ballot. The `resourceVersion` check refused anyone
  who took their time. That fix shipped on 21 September.
- **The editing segment was the busiest writing the demo has seen.** Seventeen
  people edited one object inside four minutes, and that is when the room saw
  the "latest version" error. Since 21 September the editor re-sends a save
  that lost a race when nothing overlaps.
- **The closing question got almost no answers.** If the evaluation round
  matters for the next talk, open it earlier or give it a fixed slot.
