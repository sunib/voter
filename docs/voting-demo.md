# Run a voting round

Use the same room code and login as the coffee demo. Open `/vote` on the Voter
host, choose **Answer questions**, answer, and submit. The confirmation opens
results. The presenter can open `/answer/demo-round-1/results` and select
**Refresh results** as votes arrive. Attendees can switch between Coffee and Vote
using the top navigation. Results do not refresh automatically.

## Prepare, close and repeat

The sample is [demo-round.yaml](../voter/config/demo-round.yaml). Production round
resources belong in `external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/` and must be
listed in that directory's `kustomization.yaml`. Commit and push there; Flux applies
them. The Voter repository sample is a template, not a second reconciler.

1. Before sharing a round, choose its title, questions and a new `metadata.name`.
   Set `spec.state: live` to accept votes. `draft` blocks voting and is hidden from
   the round list; `closed` remains visible for results.
2. Let Flux reconcile and verify the questions at `/vote`.
3. Share the round link, collect votes, and refresh the presenter results page.
4. To close, change `spec.state` to `closed` in Git and push. A request that read
   the live state just before closure can still finish; this is not an atomic cutoff.
5. For another round, add a resource with a new name. Reopening a closed round
   keeps its existing ballots and does not let existing voters vote again.

Do not change questions after voting begins. A browser with an older resource
version is asked to reload, and answers invalid under a changed question set are
excluded from results. Use a new round for a changed question set instead.

## Behavior and limits

- Ballots survive application restarts because Kubernetes stores them.
- An enrolled identity can create one ballot per round UID through Voter. Duplicate
  submits never replace the recorded answer. A lost success response can be retried;
  the app then reports the existing vote and shows results.
- Required answers, choices, types, numeric bounds and text length are checked on
  the server. Drafts survive reloads, scoped to the participant and round UID.
- Answers are shared with the room, including free text. This is not a secret ballot;
  participant RBAC allows reading submissions. Avoid collecting sensitive answers.
- Application voting rules are not Kubernetes admission rules. Direct API clients
  with the granted create permission can bypass them. Re-enrollment can also create
  another identity. This is an audience demo, not an election system.
- Results use explicit reads, with paginated ballot retrieval. No polling loops or
  additional watches are opened. This has browser coverage with two participants,
  not a measured 200-person capacity guarantee. The shared CoffeeConfig streaming
  and load rehearsal in PLAN.md remain outstanding.

Quiz CRD definitions are under `voter/config/crd/` for reproducible local fixtures;
keep platform CRD copies aligned when changing their schema. The browser voting test
creates and deletes its own round using only the disposable fixture kubeconfig.
