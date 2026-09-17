# Databases: the platform team's front door

The third tab, beside Quizzes and Coffee. It shows every `Database` object in
the demo namespace — what each team has asked the platform team for — and lets
anybody with the grant file a new request or change an existing one.

It is the same application as the coffee menu editor, deliberately. Same stream,
same draft-and-conflict machinery, same "why are you making this change?" note,
same single save. What differs is that a `CoffeeConfig` is one shared object
everybody amends, while a `Database` is one object per request, so this half has
a list and a create where coffee has neither.

**There is no delete.** The pages do not offer it and the Role does not grant it.

## What it is built on

`databases.platform.configbutler.ai`, a plain `CustomResourceDefinition` with no
controller behind it. Nothing reconciles a `Database`; the declared intent is
the whole of it, which is the point of the demo. `status.phase` is therefore
empty on every object and the list says "Not provisioned" rather than inventing
a green pill nobody wrote.

The CRD is at [`voter/config/crd/databases.yaml`](../voter/config/crd/databases.yaml).

## What has to exist before it works

Three things, in this order. None of them are done by building the image.

### 1. The CRD, applied to the cluster — done

```sh
kubectl apply --server-side -f voter/config/crd/databases.yaml
kubectl get crd databases.platform.configbutler.ai
```

Cluster-scoped, so this is one apply for the whole cluster. Until it exists
every page here answers with the API server's "the server could not find the
requested resource", which the screens render verbatim.

Applied to `k8s.koudijs.dev` on 2026-09-17 and Established. It is **not** in the
Flux checkout, so it is the one piece of this demo a cluster rebuild would
silently drop; [demo-c-plan.md](demo-c-plan.md) carries that as a loose end.

### 2. The participant Role

The `voter-audience` Role in the platform checkout
(`external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/participant-rbac.yaml`) needs
one more rule. It is not in this repository — deployment lives in the platform
checkout, and adding it there is what puts it in front of the room.

```yaml
  # What teams may do with their own database requests. `create` and `patch`
  # but never `delete`: filing a request and amending one are the demo; removing
  # somebody else's is not.
  - apiGroups: [platform.configbutler.ai]
    resources: [databases]
    verbs: [get, list, watch, create, patch, update]
```

Without it the pages still render — they explain the gap from the API server's
own answer and leave every button live, so pressing one produces the real 403.
That is the same behaviour the coffee editor has before the operator pulls the
switch, and it is worth showing.

### 3. Some requests to look at

The list is empty until somebody files one. Either press **New database
request** on the page, or:

```sh
kubectl apply -n voter -f - <<'YAML'
apiVersion: platform.configbutler.ai/v1alpha1
kind: Database
metadata:
  name: checkout-postgresql
  annotations:
    platform.configbutler.ai/intent: >-
      Checkout is outgrowing the shared cluster and needs its own store before
      the Black Friday freeze.
spec:
  engine: postgresql
  engineVersion: "16"
  tier: business-critical
  size: medium
  region: eu-central-2
  service: checkout
  dataClassification: confidential
  purpose: Orders, payments and the idempotency ledger for the checkout flow.
  owner:
    team: payments-core
    costCentre: CC-finance-07
    contact: payments-core@example.com
    onCallRotation: PD-SCHED-1234
YAML
```

An object written with `kubectl` appears on the page without a reload, exactly
as one filed from the page does — the list is a watch, not a poll.

## Where the intent goes

The note a person types is sent as `X-Change-Reason`, the same header the coffee
editor sends. What happens to it afterwards is where the two differ.

For coffee, the note becomes a Git commit message: the backend creates a
ConfigButler `CommitRequest` that finalizes the open window on the `voter-demo`
GitTarget, and the commit carries the message and the participant's name.

For databases there is no GitTarget yet, so there is no window to close. The
note is written onto the object as an annotation instead:

```
platform.configbutler.ai/intent
```

The create and the patch each carry it in the same write as the spec it
explains, so there is never a moment where the object records a change nobody
gave a reason for. The list page reads it straight back off the annotation, and
the editor shows the last one recorded above the form.

Crucially the backend does **not** reuse the coffee GitTarget. Doing so would
close the commit window demo 1 is waiting on, from a page that never touched the
menu — committing a half-finished menu edit under somebody else's message, live.
`TestDatabaseSaveMakesNoCommitRequestByDefault` pins that.

### Turning the Git half on later

When a GitTarget does start watching `platform.configbutler.ai/databases`, one
environment variable on the Deployment is the whole change:

```
CONFIGBUTLER_DATABASE_GIT_TARGET_NAME=<that target's name>
```

The receipt shape and what the screen does with it already match the coffee
editor's, so nothing else moves. The annotation stays useful either way: it
travels into Git with the spec it explains — the reverser strips only
operational annotations, and this is not one.

That target has a name and a plan now: **demo-c**, in
[demo-c-plan.md](demo-c-plan.md).

## The pages

| Route | What it is |
| --- | --- |
| `/databases` | The list, grouped by cost centre. One action: **New database request**. |
| `/databases/new` | The same form, blank. Creates the object, then opens the editor on it. |
| `/databases/:name` | The live editor: dirty and conflict markers, change summary, one Save. |

`/databases/new` is declared before `/databases/:name` in the router, so a
request may not be named `new` and quietly shadow the create page.

## The form is data

`frontend/src/api/databaseForm.ts` describes the fields — path, label, kind,
options, help text — and one component renders them. Thirty hand-written inputs
is where a wrong path string becomes invisible; the description is checked by
`databaseForm.test.ts`, which pins that the required fields are exactly the
CRD's and that every option list matches.

The `help` lines are the CRD's own `description` text, shortened. The platform
team wrote them for this reader, and a page that paraphrases them starts
disagreeing with the API it edits.

The enum lists in `databaseTypes.ts` are copies of the CRD's, so a dropdown can
render before the API server has been asked anything. They are not the
validation. Everything real — the enums, the patterns, the email format, the
lengths, the required fields — is still Kubernetes' answer, and the create page
renders its refusal verbatim rather than restating the rules.

## Shared with the coffee editor

The refactor that came with this page pulled the editing engine out of
`liveCoffeeConfig.ts` into `liveEditableResource.ts`: draft, conflicts, save
capture, reconciliation, recovery. `liveCoffeeConfig.ts` and `liveDatabase.ts`
are now three things each — what a valid object of that kind looks like, how to
read one, how to write one.

The same happened on the backend: `participant_save.go` holds the body shape,
the size bound and the "only `spec` is editable" rule that both `PATCH`
endpoints run, and `http.ts` holds the one fetch wrapper that used to live in
`coffee.ts`.

If the Database editor and the menu editor ever behave differently under a
concurrent edit, that is a bug in one of them and not a design decision.
`liveDatabase.test.ts` and `liveCoffeeConfig.test.ts` both exercise the shared
engine for that reason.
