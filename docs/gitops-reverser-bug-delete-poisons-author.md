# Bug: a `kubectl delete` names the author of the next writes to that name

**2026-10-06 · bug · gitops-reverser 0.49.1 (deployed) · matcher weakness still on `main`**

For ten minutes after someone deletes an object with `kubectl delete`, gitops-reverser credits
every create and update of an object **with the same name** to the person who deleted it. The
API server records the real writer correctly; gitops-reverser picks the wrong audit fact.

When the write is a person's save followed by their `CommitRequest`, the damage doubles: the
commit window opens under the deleter's name, the person's `CommitRequest` no longer matches it,
and it resolves `WindowMismatch`. The commit then ships under the wrong author **and** with a
generated message in place of the sentence the person typed.

We hit it on the demo cluster the day before a talk whose whole point is "your change becomes a
Git commit in your name". A routine reset step from our own runbook triggered it.

---

## Impact

- **Wrong author on Git commits.** Not a missing author (which would fall back to the configured
  committer, visibly), but a different, real human. Nothing in the commit says it was a guess.
- **The person's commit message is dropped.** Their `CommitRequest` resolves `WindowMismatch`
  and nothing is committed for it; the edit is committed by the window timer under
  `liveTemplate`.
- **Silent.** The `CommitRequest` reports `AuthorAttributed=True / AttributedFromAdmission`,
  which is true for the request and says nothing about the window. The only trace is one info
  log line ("CommitRequest is waiting on a window that belongs to someone else") and the
  terminal reason.
- **Scope.** Every exact-capable watch event (ADDED, MODIFIED) on an object whose
  `(namespace, name)` was deleted within `--author-attribution-ttl` (default 10 min), whenever
  the event reaches the index before its own audit fact. With audit webhook batching that is the
  common case, not a race that rarely loses: we saw it 2 out of 2 times.

## Environment

| | |
|---|---|
| gitops-reverser | `ghcr.io/configbutler/gitops-reverser:0.49.1`, one replica |
| Attribution | `--author-attribution=true`, `--author-attribution-transport=redis`, `--author-attribution-ttl=10m`, `--author-attribution-grace=3s` |
| Admission webhook | on (command author capture to Valkey) |
| Audit policy | `RequestResponse` for the watched types, delivered by the API server's batching audit webhook |
| Object | `coffeeconfigs.examples.configbutler.ai/v1alpha1`, `voter/demo-coffee` (a CRD, not aggregated) |
| GitTarget | `voter/demo2`, provider `k8s-trail`, branch `main` |
| Writer | a person signed in through krm-foyer, which forwards the person's own bearer token (no impersonation) |

## Timeline (all times 2026-10-06, UTC)

All of this comes from the cluster: the kube-apiserver audit log on the Talos nodes,
gitops-reverser's logs, the `CommitRequest`'s status, and the commits in `ConfigButler/k8s-trail`.

**14:46:35.527: an admin deletes the object.** Our runbook resets the coffee menu this way.

```json
{"level":"RequestResponse","verb":"delete",
 "user":{"username":"admin"},"userAgent":"kubectl/v1.37.0 (linux/amd64) kubernetes",
 "objectRef":{"resource":"coffeeconfigs","namespace":"voter","name":"demo-coffee",
              "apiGroup":"examples.configbutler.ai","apiVersion":"v1alpha1"},
 "responseStatus":{"metadata":{},"status":"Success","code":200,
   "details":{"name":"demo-coffee","group":"examples.configbutler.ai","kind":"coffeeconfigs",
              "uid":"3c679d5a-0faa-410e-ab26-dc3fa5fcdab8"}}}
```

`objectRef` has no `uid` and no `resourceVersion`. The uid exists only in the `Status` body's
`details`.

**14:46:35.946: Flux recreates it.** `system:serviceaccount:flux-system:kustomize-controller`,
server-side apply, `201`. gitops-reverser:

```
Opening commit window  author=admin  gitTarget=voter/demo2
  resource=examples.configbutler.ai/v1alpha1/coffeeconfigs/demo-coffee
git commit created     message="chore(demo2): 1 change\n\n- [CREATE] CoffeeConfig/demo-coffee@12769209"
```

**First misattribution.** The CREATE was kustomize-controller's, and it was credited to `admin`.

**14:47:41.537: a participant saves.** It is a conditional merge PATCH through krm-foyer with
`fieldManager=voter`. The API server records the right person:

```json
{"verb":"patch","user":{"username":"demo:CgZzaW1vbjISCXJvb20tcGFzcw"},
 "userAgent":"Mozilla/5.0 (X11; Linux x86_64; rv:157.0) ...",
 "objectRef":{"resource":"coffeeconfigs","namespace":"voter","name":"demo-coffee"},
 "responseStatus":{"code":200}}
```

`managedFields` agree (`voter  Update  2026-10-06T14:47:41Z`). gitops-reverser:

```
Opening commit window  author=admin  gitTarget=voter/demo2
  resource=examples.configbutler.ai/v1alpha1/coffeeconfigs/demo-coffee
```

**Second misattribution.**

**14:47:41: the participant's `CommitRequest` is attributed correctly.**

```
recorded command author at admission  name=coffee-save-jmw26  author=demo:CgZzaW1vbjISCXJvb20tcGFzcw
CommitRequest attach enqueued  author=demo:CgZzaW1vbjISCXJvb20tcGFzcw  target=voter/demo2
  closeDelaySeconds=1  messageOverride=true
CommitRequest is waiting on a window that belongs to someone else
  requestAuthor=demo:CgZzaW1vbjISCXJvb20tcGFzcw  windowAuthor=admin  windowTarget=voter/demo2
CommitRequest resolved  outcome=WindowMismatch  sha=""
```

**14:47:46: the timer commits the edit as `admin`.**

```
Finalizing open commit window  reason=timer  windowAuthor=admin  messageOverride=false  attachedCR=false
git commit created  message="chore(demo2): 1 change\n\n- [UPDATE] CoffeeConfig/demo-coffee@12769662"
```

The `CommitRequest` as it stands:

```yaml
metadata: {name: coffee-save-jmw26, namespace: voter, uid: aec83973-2277-4562-a755-f7d939c9216c}
spec:
  closeDelaySeconds: 1
  gitTargetRef: {name: demo2}
  message: 'chore: would this still work? Perhaps make it a bit more flat in the folder?'
status:
  conditions:
  - {type: AuthorAttributed, status: "True",  reason: AttributedFromAdmission}
  - {type: Ready,            status: "True",  reason: WindowMismatch,
     message: the open commit window belongs to a different author or GitTarget; nothing
       was committed for this request}
  - {type: Pushed,           status: "False", reason: WindowMismatch}
```

What landed in Git:

| Commit | Author | Committer | Subject |
|---|---|---|---|
| `d1ea4836` (CREATE) | `admin <admin@noreply.cluster.local>` | ConfigButler Bot | `chore(demo2): 1 change` |
| `2ccebefd` (UPDATE) | `admin <admin@noreply.cluster.local>` | ConfigButler Bot | `chore(demo2): 1 change` |

Should have been kustomize-controller's service account, and
`demo:CgZzaW1vbjISCXJvb20tcGFzcw` with the typed message.

## Root cause

Two things combine. The first is fixed in 0.50.0; the second is still on `main`, and it is the
real bug.

### 1. In 0.49.1 the delete fact carries no uid, so it is filed under the name floor

`IdentityFromAuditEvent` (`internal/auditutil/identity.go` at `v0.49.1`) takes the uid from
`objectRef`, then backfills from a body's `metadata.uid`. A `kubectl delete` has neither:
`objectRef.uid` is empty, the request body is `DeleteOptions`, and the response is a `Status`
whose uid sits under `details`, not `metadata`. The fact ends up with a name and no uid or
resourceVersion, and `FactIndex.file` (`internal/queue/fact_index.go`) sends exactly that shape
to the last case:

```go
case fact.Name != "":
    // The floor: no uid and no resourceVersion, so only the name can reach it.
    facts.putName(fact.Namespace, fact.Name, ...)
```

`70847ee2` (#399, first released in **0.50.0**) reads `details.name` and `details.uid` from a
`Status` body. From 0.50.0 on, this delete is filed by uid (`putLatest` and `putRemoval` under
the *old* uid `3c679d5a…`), and a new object with a new uid can never reach it. **On 0.50.0+
the trigger above no longer reproduces for an ordinary CRD.**

### 2. An ADDED/MODIFIED event takes the first answer the ladder gives, including the name floor

This part is unchanged on `main`. `FactIndex.Await`:

```go
if resolution := i.Lookup(query); resolution.Result != AttributionAbsent {
    if !query.awaitsBetterEvidence(resolution) {
        return resolution           // ← returns here, without waiting
    }
    fallback = resolution
}
```

and `awaitsBetterEvidence` begins:

```go
if q.ExactCapable || q.FilteredRemoval || resolution.Result == AttributionAbsent {
    return false
}
```

For an exact-capable event, *any* hit ends the wait at once, whatever tier it came from. When the
watch event arrives before its own audit fact, which is normal because audit events arrive in
batches, `lookupTiers` misses the exact `(uid, rv)` tier, misses the rv tier, and falls to:

```go
if query.Name != "" {
    if fact, found := facts.lookupName(query.Namespace, query.Name, cutoff); found {
        return AuthorResolution{Fact: fact, Result: AttributionName}
    }
}
```

That returns the delete fact by `admin`. The 3 s grace, which exists precisely so the exact fact
can arrive, is never used.

The name tier's own comment names the hazard:

> The name tier is last because a name is reused after a delete and recreate, so it can name the
> author of a previous object that held it, where a uid cannot.

Ranking it last does not help when it is also accepted *immediately*: last place still wins if
nothing above it has arrived yet.

Two separate errors are hiding in that one return:

- **The wrong verb.** A fact whose verb is `delete` or `deletecollection` answers "who removed
  this", never "who wrote this". It cannot be the author of an ADDED or MODIFIED event. The
  removal path already applies this rule (`lookupRemoval` holds a *write* fact back when the
  question is a removal); the write path lacks its mirror image.
- **No wait for the strong answer.** For an exact-capable event, a weak-tier hit (`name`,
  `resourceVersion`) should be held as the fallback while the grace runs, as removals already do,
  not returned.

### Why it still matters on `main`

On `main` the delete above no longer reaches the name floor, but other facts still do. The code
lists the main source: an aggregated API's write or delete, whose `objectRef` holds only the
name from the URL path. Any type served through an aggregated API server (metrics, custom
apiservers, and anything behind `APIService`) still has this problem: delete-and-recreate within
the TTL credits the deleter. It also returns whenever any other path yields a fact with only a
name, and nothing stops such a path from being added.

## Reproduction

On **0.49.1** with attribution on, against any CRD-backed object `X` in a watched namespace:

1. As user A: `kubectl -n ns delete <kind> X`. Recreate it (as B, or via Flux).
2. Within 10 minutes, as user C: patch `X`'s spec, then create a `CommitRequest` for its
   GitTarget with a `message`.
3. Expect the window author to be C and the `CommitRequest` `Committed` with C's message.
   Observe `Opening commit window author=A` (for both the recreate and the patch), the
   `CommitRequest` `WindowMismatch`, and both commits authored by A.

On **`main`**, the same through an aggregated API type. As a unit test against `FactIndex`
alone, which needs no cluster:

1. File a fact `{verb: delete, user: A, namespace: ns, name: X}` (no uid, no rv).
2. `Await` a query `{ExactCapable: true, UID: u2, ResourceVersion: r2, Namespace: ns, Name: X}`
   with grace 3 s.
3. 500 ms later, file `{verb: patch, user: C, uid: u2, rv: r2}`.
4. Expect C / `AttributionExact`. Today the result is A / `AttributionName`, returned before
   step 3.

## Proposed fix

Both changes are in `internal/queue/fact_index.go`. They are independent; the first is the
minimal one.

**1. A removal fact never answers a write.** In `lookupTiers`, skip the name floor (and, for
symmetry, the rv tier) when the query is exact-capable and the fact's verb is a removal:

```go
if query.Name != "" {
    if fact, found := facts.lookupName(query.Namespace, query.Name, cutoff); found &&
        !(query.ExactCapable && isRemovalVerb(fact.Verb)) {
        return AuthorResolution{Fact: fact, Result: AttributionName}
    }
}
```

With this alone, our timeline resolves correctly: the patch's exact fact arrives inside the grace
and wins. If it does not arrive, the result is absent and the commit goes to the configured
committer, which is honest, rather than to a stranger.

**2. An exact-capable event waits for the exact tier.** Hold any weaker hit as the fallback
instead of returning it:

```go
func (q FactQuery) awaitsBetterEvidence(resolution AuthorResolution) bool {
    if q.FilteredRemoval || resolution.Result == AttributionAbsent {
        return false
    }
    if q.ExactCapable {
        return resolution.Result != AttributionExact
    }
    // ... removal logic unchanged
}
```

`settle` already turns a held fallback into the answer once the grace runs out, so an
aggregated-API write whose only fact is name-keyed is still attributed, just up to `grace` later.
That costs at most 3 s per such event on a serial shard goroutine. The exact waiter key is
already registered for these queries, so the common case returns as soon as the exact fact
lands, with no added latency.

Optional hardening: when a name-floor fact carries a uid (from a `Status` body or elsewhere) and
the query's uid differs, treat it as a different object and skip it. On `main` such facts are
filed by uid anyway, so this guards a future regression rather than a present bug.

**Tests to add**

- The unit test from *Reproduction*, with the exact fact arriving 500 ms late → `AttributionExact`, C.
- Same, exact fact never arriving → `AttributionAbsent` (fix 1), never A.
- A name-only *write* fact (aggregated update by A), then a MODIFIED for the same name whose
  exact fact by C arrives late → C (fix 2).
- A name-only write fact and no exact fact → A after the grace via `settle` (fix 2 keeps today's
  aggregated-API attribution).
- An e2e in the CommitRequest suite: delete, recreate, save, `CommitRequest` → `Committed`,
  author = saver, message = request's.

**Possibly worth surfacing.** A `CommitRequest` that ends `WindowMismatch` could carry the
window's author in its message (*"the open window belongs to admin"*). Here that one line
would have pointed straight at the attribution, not at the request.

## What is not the cause

- **Voter.** The browser creates the `CommitRequest` as the person through krm-foyer
  (`frontend/src/api/kube.ts`, `requestCommit`), and admission captured the right author. This
  is the correct design and should not change.
- **krm-foyer.** It forwards the person's own bearer token (`internal/proxy/proxy.go`); the
  API server's audit log has the person on the PATCH.
- **Audit delivery.** Facts arrive (`AuditFactsReceived=True` on the ClusterProvider since
  2026-09-11), and the PATCH's fact names the right user. Only the join is wrong.

## Workaround in use

Upgrading the cluster to 0.50+ removes the trigger. We are not doing that the day before a
talk: 0.50.0 also carries breaking WatchRule API changes (#398). Instead
[demo-runbook.md](demo-runbook.md) now says:

- run every reset `kubectl delete` at least **15 minutes** before anyone saves, which is longer
  than the 10-minute fact TTL;
- then make one test save and check that its `CommitRequest` reads `Committed`, not
  `WindowMismatch`.

The same applies to `quizsession demo1 evaluation`: an operator's round switch within ten
minutes of reseeding would be credited to `admin`. The ballot reset (`delete quizsubmissions
--all`) is a `deletecollection`, which is filed as a collection fact and only consulted for
removals, so it does not trigger this.
