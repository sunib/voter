# A proper status for CoffeeConfig

> Status: built, in `voter/coffee_status_reconciler.go`. The checks still owed on the live cluster are
> under "Proving it".

Voter says on the menu's status which version of the menu it has looked at, whether the coffee bar can
deliver what the menu promises, and when the first order came in. There are no counters.

## What it looks like

```yaml
status:
  observedGeneration: 116
  firstOrderTime: "2026-11-06T09:14:03Z"
  keys:                                 # exists today; the editor reads it
    mail: {set: true}
    payments: {set: true}
  conditions:
    - type: Ready
      status: "False"
      reason: VoucherDepleted
      message: "Voucher TESTNET has reached its maximumUsage of 1."
      lastTransitionTime: "2026-11-06T09:14:04Z"
    - type: MenuAvailable
      status: "True"
      reason: ProductsEnabled
      message: "1 of 2 products can be ordered."
      lastTransitionTime: "2026-11-05T20:00:00Z"
    - type: KeysAvailable
      status: "True"
      reason: AllKeysSet
      message: "Both keys the menu names have a value."
      lastTransitionTime: "2026-11-05T20:00:00Z"
    - type: VouchersAvailable
      status: "False"
      reason: VoucherDepleted
      message: "Voucher TESTNET has reached its maximumUsage of 1."
      lastTransitionTime: "2026-11-06T09:14:04Z"
```

## The fields

| Field | Means | Why it is there |
| --- | --- | --- |
| `observedGeneration` | The `metadata.generation` all of this was worked out from | Without it nobody can tell "Ready" from "was Ready before your edit". Behind `metadata.generation` for more than a moment means Voter is not looking |
| `conditions` | The judgements, each with a reason and a sentence | `kubectl wait --for=condition=Ready` works, and generic tooling can read them |
| `keys` | The one bit per key. Unchanged | The editor's "Set" / "Not set" pills read it. The conditions judge it; they do not replace it |
| `firstOrderTime` | When the first order since this Voter started was placed | The answer to "has anyone ordered yet?", without a count |

Only one `observedGeneration`, at the top. All conditions come from a single reconcile, so a copy
on each one could only ever repeat it.

## The conditions

All are positive: True is the good state. That is the repo's own convention
([conditions-and-status.md](../skills/k8s-crd-design-review/references/conditions-and-status.md)).

| Type | True means | True reasons | False reasons |
| --- | --- | --- | --- |
| `MenuAvailable` | At least one product can be ordered | `ProductsEnabled` | `NoEnabledProducts` |
| `KeysAvailable` | Every key the menu names has a value | `AllKeysSet`, `NoKeysRequired` | `MailKeyMissing`, `PaymentsKeyMissing`, `KeysMissing` |
| `VouchersAvailable` | Every enabled voucher can be honoured | `VouchersRedeemable`, `NoVouchers` | `VoucherDepleted`, `VoucherNotApplicable` |
| `Ready` | All three above are True | `ShopReady` | The first failing condition's reason, in the order above |

How each one is decided:

- **The CRD already refuses a malformed menu**, so no condition repeats that. Each one reports
  something the schema cannot see: every product switched off, an empty Secret, a voucher that has
  run out.
- **A key counts only if the menu names it.** A menu without `spec.mail.provider` needs no mail key,
  so an empty `coffee-mail` is not a fault.
- **`KeysAvailable` means "a value is there", never "the key works".** It is computed from the bits
  in `status.keys`, so it reveals nothing new about a key. In cluster mode Voter does not use the keys
  at all, so a missing key does not stop an order, and `Ready` can be False while orders still work.
  `Ready` means "this bar is as its menu says", not "this request will succeed".
- **`VoucherDepleted` uses the same comparison as the till.** Add `depleted(code, limit)` to
  `voucherLedger` and call it from both `redeem` and the reconciler, so the two cannot disagree. Keep
  the existing rule that a limit of zero or less means unlimited.
- **`VoucherNotApplicable`** reuses the rule the storefront has: an enabled voucher that applies to no
  enabled product. Both voucher reasons are already the names of order failures.
- **`Unknown` is never written.** The reconciler only runs once it has read both Secrets, so it knows
  or it says nothing. A missing status means no Voter is looking, and a stale `observedGeneration`
  means one has stopped.

## Why no counters

The status changes only when an answer changes, so a room's worth of orders costs a handful of writes:

| | Status writes for 200 orders | Events reaching 200 phones |
| --- | --- | --- |
| `used: 137` on each order | about 200 | about 40,000 |
| This design | one for the first order, one per voucher that runs out | a few hundred |

The message may name the limit, because that is in the spec and every reader of the menu can see it.
It never names a person, or how many have been used. The real count stays where it is today:
`/public/vouchers`, in process memory, labelled as such.

### Why `firstOrderTime` is not a condition

A condition is a state that can go either way, and `OrderReceived=False` reads like a fault on a bar
that is fine and has just opened. The first order is an event that happened once, so it is a timestamp,
as `Job.status.startTime` is. It does not feed `Ready`.

- Only a **placed** order sets it. A refusal does not.
- **A later order never moves it.**
- **Voter clears it when it starts.** The first pass after a start removes what the last run left, so
  it means "the first order since this Voter started". Redemptions and the order feed are in memory and
  reset on a restart too, so all three always tell one story, and a restart is the clean slate between a
  rehearsal and a talk. An order placed before that first pass is kept: it belongs to this run.
- The cost: after a restart the status can say no order has come in when earlier runs took some. Nothing
  here records orders across restarts, so that is the honest answer for this run.

### What a restart does

A restart forgives every redemption, so `VouchersAvailable` goes back to True, and it clears
`firstOrderTime`. That is correct and not a loss of information: the condition says what Voter will do
now, and it will honour the voucher again. The cost of keeping counts in memory stays where
[coffee_vouchers.go](../voter/coffee_vouchers.go) states it.

## How Voter keeps it

[coffee_status_reconciler.go](../voter/coffee_status_reconciler.go), which grew out of the keys
reconciler: **one reconciler owns all of `status`**, so two writers can never overwrite each other's
merge patch.

| What wakes it | So that |
| --- | --- |
| A watch on `demo-coffee` (new) | `observedGeneration` follows a save in about a second, not at the next one-minute resync |
| The two Secret informers (today) | A key's bit moves |
| A placed order | `firstOrderTime` and voucher depletion. A non-blocking poke: an order never waits for status, and a failed status write cannot fail an order |
| The one-minute resync (today) | A failed write heals, and a recreated object gets its status back |

Each pass:

1. **Wait a second** and collapse whatever else arrives, as the tally does (`coalesce` in
   [quiz_reconciler.go](../voter/quiz_reconciler.go)). A room saving together produces one write.
2. **`GET` the object.** The new watch is only a doorbell. Deciding from a fresh read means a late
   echo cannot make it write twice, or replace `firstOrderTime` with a later order's time.
3. **Compute the status** with `meta.SetStatusCondition` from apimachinery, which keeps
   `lastTransitionTime` unless the status really changed.
4. **Patch only if the result differs.** The status write's own watch event then finds nothing to do,
   which is what ends the loop.

The compute step is a pure function of (object, key bits, ledger, pending first order), like
`placeCoffeeOrder`, so it can be tested without a cluster.

## What a status write touches

| What | Effect | Checked |
| --- | --- | --- |
| The editor's draft | None: the store only tracks `spec` (`regionPolicy([['spec']])`) | Read in `liveEditableResource.ts` |
| A save in progress | A status write moves `resourceVersion`, so a save holding the old one gets a 409. It re-reads and retries, up to three attempts | Read in the code. **Every save now causes one more write, so this happens more often; test it** |
| `/public/vouchers` | Each open editor refetches it on any `resourceVersion` change. One extra GET per write | Read in `AdminScreen.vue` |
| Git (k8s-trail, coffee-settings, settings image) | gitops-reverser strips status, so no diff and no commit | Stated in the code comments. **See below** |
| Commit authorship | A write by Voter's ServiceAccount now lands inside the commit window of every person's save. Today that happens only when a key goes from empty to set. A window opened by the wrong author has bitten before ([the bug report](gitops-reverser-bug-delete-poisons-author.md)) | **Not proven. Test before it goes live** |
| Admission policies | `coffee-price-policy` matches `coffeeconfigs`, not `coffeeconfigs/status`, so status writes skip it | Read in the policy |
| Flux `wait: true` on `voter-demo` | From what I know of kstatus: `observedGeneration` behind `generation` reads as in progress, and `Ready=False` is ignored for a custom kind. So a depleted voucher does not fail the Kustomization, but a menu edit while Voter is down could hold it until Voter is back | **From memory, not tested. Confirm with `flux get`** |
| `SETTINGS_DIR` and other shop-only deployments | No status at all: no reconciler is made, so an order has nothing to tell, and a menu from an image needs no client. An absent status truthfully says nobody is watching | A Go test through the order handler, and a real process placing orders with no cluster |

## On screen

Printer columns are the stage view. `Currency` moved to `-o wide` to make room. The editor shows the
same thing in its Status panel, live, from the object it already streams, and it says "Checking" while
the menu is ahead of what Voter has looked at:

```text
$ kubectl get coffeeconfig demo-coffee -n voter -w
NAME          SHOP        READY   REASON            GEN   OBSERVED   FIRST ORDER   CREATED
demo-coffee   Koffiebar   True    ShopReady         115   115                      30d
demo-coffee   Koffiebar   True    ShopReady         115   115        2s            30d
demo-coffee   Koffiebar   False   VoucherDepleted   115   115        40s           30d
demo-coffee   Koffiebar   False   VoucherDepleted   116   115        90s           30d
demo-coffee   Koffiebar   True    ShopReady         116   116        91s           30d
```

Those rows are illustrative, but they are the story, in order:

1. The bar is open, and nobody has ordered.
2. The first order comes in.
3. The last allowance goes. **The status knows before anyone is refused**, while the storefront still
   advertises the discount and the next person in the room walks into the refusal.
4. Someone raises `maximumUsage`. The spec moved and Voter has not looked yet: this is the row where
   `observedGeneration` explains itself.
5. Voter has looked, and the bar is ready again.

For the columns: `.status.conditions[?(@.type=="Ready")].status` and `.reason`,
`.metadata.generation`, `.status.observedGeneration`, and `.status.firstOrderTime` as a `date`, which
kubectl prints as an age. The message goes in a `priority: 1` column.

## Rollout

This counts as a compatible change in
[runbook-crd-updates.md](runbook-crd-updates.md), so `external/k8s` goes first. Here the order really
matters, because the new app fails quietly against the old platform:

- **Old CRD:** the API server prunes the new fields. The reconciler then always sees a difference and
  patches on every pass, forever.
- **Old Role:** the new informer never syncs. If the reconciler waits for it as it waits for the
  Secrets today, `status.keys` stops being updated too.

A `feat:` commit reaches the cluster on its own, so the `external/k8s` commit must be live before the
PR merges.

## Proving it

- **Unit:** the compute function, table-driven over each reason. A second pass over its own output
  writes nothing. `lastTransitionTime` survives a pass where nothing changed. A limit of zero never
  depletes. A start clears `firstOrderTime`. An order through the handler in folder mode tells nothing.
- **Browser, on the fixture:** the existing save and conflict specs still pass with the extra write.
  [coffee-status.spec.js](../test/browser/coffee-status.spec.js) walks the sequence in "On screen",
  starting from a restart that clears an old `firstOrderTime`, with an unsaved edit open throughout.
- **On the live cluster,** because the fixture (Kubernetes 1.31) has no WatchList and live 1.37 does,
  so an informer can behave differently there.
    - A named person saves the menu, and the commit carries their name with a `Ready` CommitRequest.
      This is the authorship check above.
    - `flux get kustomization voter-demo` stays Ready while a voucher is depleted.
    - Two people save in the same second, and neither sees "lost the race".

## Decisions

1. **A depleted voucher makes `Ready` False.** In real life a limited voucher running out can be normal,
   but here the menu is still advertising a discount it will refuse, and this is the beat the talk needs.
   To keep `Ready` for real faults, leave `VouchersAvailable` out of the loop in `computeCoffeeStatus`.
2. **A missing key makes `Ready` False, though orders still work,** because the menu names a provider
   and the CRD's own message says a provider without a key cannot send.
3. **A start clears `firstOrderTime`.** See "Why `firstOrderTime` is not a condition".
