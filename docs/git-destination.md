# Where participant commits should land

Status: **design only**. Nothing in this document exists yet — no repository, no
credential, no `GitTarget`. It describes the smallest thing that makes "your
change becomes a Git commit in your name" true, and names the risks of doing it
on GitHub so the decision to accept them is explicit.

## What is missing today

gitops-reverser is installed and healthy: the chart is deployed through Flux,
the audit webhook delivers, and `ClusterProvider/default` reports
`AuditFactsReceived=True` — the cluster has already proven it can name a human
actor from an audit event.

What it does not have is a destination:

```
$ kubectl get gittargets,gitproviders,watchrules,commitrequests -A
No resources found
```

The application is already configured for one. `CONFIGBUTLER_GIT_TARGET_NAME`
defaults to `voter-demo`, and a participant who saves the menu creates a
`CommitRequest` in the `voter` namespace naming that target — a right their
RoleBinding grants. Admission always allows a `CommitRequest` (it only records
the submitter as the command author), so **today the save receipt reports
`commitRequested: true` and nothing ever reaches Git**. That is the one dishonest
claim in the demo, and this design is what retires it.

## The repository

A new, small, single-purpose repository — `sunib/coffee-demo-state` — holding
one folder:

```
clusters/k8s.koudijs.dev/voter/     <- GitTarget.spec.path
README.md                           <- what this repo is, seeded by hand
```

Properties, each for a reason:

| Property | Why |
| --- | --- |
| Separate repo, not a folder in `ConfigButler/k8s` | The platform repo is a *source* Flux reconciles. A sink that strangers write into must never be a source. |
| **No Flux `GitRepository` points at it** | This is a one-way mirror. There is no path from an attendee's commit back into the cluster, so the blast radius of a bad commit is "the repo looks silly". |
| Public | Attendees can open it on their phones during the talk. That is the payoff; see the risk section for what it costs. |
| One branch, `main` | `GitProvider.spec.allowedBranches: [main]` refuses everything else, so a compromised key cannot litter branches. |
| Issues, Wiki, Projects, Discussions, Actions all disabled | Nothing to moderate, no Actions minutes, no notification stream. |
| Disposable | Created for the conference, deleted or force-reset afterwards. Treat its history as scratch. |

## The credential

An `ed25519` **deploy key with write access on that one repository** — not a PAT.
A PAT carries the whole account; a deploy key carries one repo, which is the
entire point.

```sh
ssh-keygen -t ed25519 -C "gitops-reverser@k8s.koudijs.dev" -f ./gitops-reverser-key -N ""
gh repo deploy-key add ./gitops-reverser-key.pub --repo sunib/coffee-demo-state \
  --title gitops-reverser --allow-write
```

The private key and a pinned `known_hosts` become a SOPS-encrypted Secret in
`ConfigButler/k8s` at `2-gitops/voter-demo/`, encrypted in the repo path so the
`.sops.yaml` creation rules match. Pin `known_hosts` from `ssh-keyscan github.com`
rather than trusting on first use.

## The manifests

Four objects in `ConfigButler/k8s`, namespace `voter`, added to
`2-gitops/voter-demo/kustomization.yaml`. Everything is `configbutler.ai/v1alpha3`
— the only served version.

```yaml
apiVersion: configbutler.ai/v1alpha3
kind: GitProvider
metadata:
  name: coffee-demo-state
  namespace: voter
spec:
  url: git@github.com:sunib/coffee-demo-state.git     # immutable
  secretRef:
    name: coffee-demo-state-git                        # ssh-privatekey + known_hosts
  allowedBranches: [main]
  commit:
    # The committer is the robot and stays the robot. Only the AUTHOR is the
    # participant -- which is exactly how `git show --format=fuller` tells the
    # room that the cluster made the commit on a human's behalf.
    committer:
      name: ConfigButler
      email: gitops-reverser@k8s.koudijs.dev
---
apiVersion: configbutler.ai/v1alpha3
kind: GitTarget
metadata:
  # This name is API, not decoration: the app sends CommitRequests to
  # CONFIGBUTLER_GIT_TARGET_NAME, which defaults to voter-demo.
  name: voter-demo
  namespace: voter
spec:
  gitProviderRef:
    name: coffee-demo-state
  branch: main
  path: clusters/k8s.koudijs.dev/voter
  prune:
    mode: OnEvent
  commit:
    # 5s is the default and the right one here: a participant tapping "+5c"
    # four times produces one commit, and "save now" closes the window early.
    window: 5s
    message:
      liveTemplate: |-
        {{.Author}} changed the menu

        {{range .Resources}}- [{{.Operation}}] {{.Resource}}/{{.Name}}
        {{end}}
---
apiVersion: configbutler.ai/v1alpha3
kind: WatchRule
metadata:
  name: coffee-demo
  namespace: voter
spec:
  gitTargetRef:
    name: voter-demo
  rules:
    # The menu, and nothing else. Not Secrets, not the Room Pass Participants
    # (they carry attendee-typed names), not the quiz -- see below.
    - apiGroups: [examples.configbutler.ai]
      apiVersions: [v1alpha1]
      resources: [coffeeconfigs]
      operations: [CREATE, UPDATE, DELETE]
```

### The prerequisite that is easy to miss

`ClusterProvider/default` is deny-by-default about which namespace may *use* it:

```yaml
spec:
  accessFrom:
    names: [gitops-reverser]
```

A `GitTarget` in `voter` is refused until `voter` joins that list. That is a
values change on the HelmRelease in `2-gitops/gitops-reverser/release.yaml`:

```yaml
    clusterProvider:
      default:
        accessFrom:
          names:
            - gitops-reverser
            - voter
```

The alternative — putting the `GitTarget` in the `gitops-reverser` namespace and
pointing `CONFIGBUTLER_COMMITREQUEST_NAMESPACE` there — is worse: it would give
a room full of strangers `create` rights inside the operator's own namespace.
Widening `accessFrom` by one name grants the `voter` namespace the right to
*write to its own Git folder*, which is what it should have.

## Who the commit says you are

The chain is already built and already carrying real data; only the destination
is missing.

1. A participant joins through Room Pass and types a display name. It is
   validated to 1–64 bytes, no control characters, no angle brackets — and is
   otherwise free text.
2. Room Pass sets `X-Remote-User-Name` to that name and
   `X-Remote-User-Email` to `<participant-id>@demo.invalid`, a synthetic address
   with no mailbox behind it.
3. The apiserver's authentication config maps those into
   `configbutler.ai/claims/display-name` and `configbutler.ai/claims/email` on
   the user, alongside the username `demo:<opaque-dex-subject>`.
4. The audit event carries them to gitops-reverser, which uses the display name
   as the Git author name when it is safe to place in a signature header, and
   the username otherwise; the email claim when it is a valid address, and
   `<username>@cluster.local` otherwise.

So a commit reads `Author: Ada <p-7f3c…@demo.invalid>`, `Commit: ConfigButler
<gitops-reverser@k8s.koudijs.dev>`. If attribution ever fails to resolve, the
author is the loud `unknown (attribution unresolved)
<attribution-unresolved@gitops-reverser.invalid>` rather than a silent fallback
to the robot — a broken demo looks broken.

## The risks of doing this on GitHub

Accepted, but each one has a cheap mitigation that should actually be applied.

**Attendee-typed names become permanent public Git history.** This is the real
one. The author name is whatever a stranger typed into a join form, in a public
repo, forever-ish. Mitigations: the repo is disposable and gets deleted or
force-reset after the conference; the README says plainly what it is; and the
kill switch is to drop the `display-name` extra from the apiserver claim mapping,
which makes every author the opaque `demo:<sub>` — at the cost of the demo's
whole point, so keep it as an emergency lever rather than a default.

**A write credential lives in the cluster.** Bounded by construction: one deploy
key, one repository, one branch allowed, and the repository is a sink nothing
trusts. Rotation is `gh repo deploy-key delete` plus a new SOPS Secret.

**The demo now depends on github.com from the venue.** If the network is bad,
pushes stall and the commit shows up late or not at all. This is why the receipt
reports *saved to Kubernetes* and *commit requested* as separate facts — the
Kubernetes half still works and the room still sees the menu change live. If
that trade feels bad on the day, the fallback is a second `GitTarget` pointing at
an in-cluster Gitea, which removes the internet from the path entirely.

**Volume.** 200 attendees editing one CoffeeConfig is not a lot of commits: the
window coalesces a burst per author, and one branch worker serializes pushes.
Interleaved authors split windows, so expect roughly one commit per person per
edit burst, not per keystroke.

**GitHub push protection.** Secret scanning stays on. Since the only watched kind
is CoffeeConfig, nothing scannable should ever be written — but if push
protection ever rejects a push, the `GitTarget` stalls rather than corrupting
anything, and `Ready=False` names the reason.

## Deliberately out of scope

**The quiz is not mirrored.** `QuizSubmission` objects are one per participant
per round and would turn a readable menu diff into 200 files of vote noise. If a
"the room's answers are in Git" moment is wanted later, it belongs in its own
`GitTarget` and folder so the coffee story stays legible.

## How to know it works, before the room does

1. `kubectl get gitprovider,gittarget,watchrule -n voter` — all `Ready=True`,
   and `GitTarget` reports `GitPathAccepted` and `StreamsRunning`.
2. Patch the CoffeeConfig with the admin kubeconfig, wait for the window, and
   check the commit landed. The author will be `admin` — that proves the
   pipeline, not the attribution.
3. Log in through Room Pass as a real participant, change a price, and press
   save. `git show --format=fuller` must name the display name typed at the join
   form. This is the check that matters; the admin write cannot stand in for it.
4. Confirm the `CommitRequest` reaches `Ready=True` with a `status.sha`, and that
   its SHA is the commit you just read.

## Worth building afterwards

`CommitRequest.status` carries `branch` and `sha`. The save receipt could turn
those into a link to `https://github.com/sunib/coffee-demo-state/commit/<sha>`,
so a participant goes from "I changed a price" to their own commit on GitHub
without anyone reading a URL off a slide. That is the moment the demo is trying
to sell, and it is a small change to the receipt the app already returns.
