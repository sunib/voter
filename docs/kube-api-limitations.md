# Kubernetes API limitations worth remembering

Things the API server cannot do, or does differently from what you would
assume, that have shaped (or broken) a design here. Each entry says what the
limit is, how we know, and what we did about it. Add to it whenever a design
runs into one: the point is that the next design does not have to find it again.

Versions matter: unless an entry says otherwise, it holds on Kubernetes 1.37,
the version `k8s.koudijs.dev` runs.

---

## Admission and CEL

### A Secret's `data` is base64 text in CEL, and CEL cannot decode it

In a `ValidatingAdmissionPolicy`, `object.data.apiKey` is the base64 **string**
as stored (`taart` arrives as `dGFhcnQ=`), not bytes. `string(...)` returns that
base64, and `base64.decode` is not part of the API server's CEL ("undeclared
reference to 'base64'"). So a policy **cannot check what a Secret value says**,
only what its encoding looks like: its length (a regex on the base64 shape can
pin "1 to 5 bytes") and whether it equals another value (old vs new).

_Found 2026-11-03 on k3d 1.37.1, for the coffee keys
([coffee-api-keys-design.md](coffee-api-keys-design.md), decision 5). We dropped
the rule on the value: any key is accepted, and `voter-coffee-keys` only compares
old and new._

### A CEL rule sees only the object being written

A CRD's `x-kubernetes-validations` sees `self` (and `oldSelf`); a policy sees
`object`, `oldObject`, the request and its params. Neither can look up another
object, so "the Secret this names exists" or "the round is still open" is not
something CEL can say. That takes a param, a webhook or a controller.
See [kubernetes-as-a-bff.md](kubernetes-as-a-bff.md#5-validating-admission-the-rules-that-need-the-world).

### No clock, no rate limits

CEL in admission has no `now()`, and API Priority and Fairness shares capacity
between callers rather than setting quotas. "At most once a minute" needs a
webhook or a controller. We use RBAC instead: one named person holds the grant.

### Ratcheting works, but a CEL rule warns as it ratchets

Tightening a CRD does not block edits to objects that already break the new
rule, as long as the edit leaves the offending field alone. Verified for
`enum`, `required` and an `x-kubernetes-validations` rule without `oldSelf`.
The difference: the enum and `required` ratchet silently, while the CEL rule
lets the edit through **with a warning** on every write, so an old object keeps
printing `Warning: spec.payments: Invalid value: …` until it is fixed.

_Verified 2026-11-03 on k3d 1.37.1._

### A patch that breaks two rules gets one status

A patch that only adds an annotation to a coffee key breaks two rules of
`voter-coffee-keys`: "the value must change" (`reason: Invalid`, 422), listed
first, and "only the value" (`reason: Forbidden`, 403). It comes back 422. So a
test of one rule has to make every other rule pass, or it asserts on the wrong
refusal.

_Seen 2026-11-03 on the fixture (k3s 1.31), writing coffee-keys.spec.js._

### Things that do work, checked so they need not be checked again

- A no-op patch (`{}`, or the value already stored) still goes through
  validating admission, even though the resourceVersion does not change.
- So does a `dryRun=All` patch.
- A `stringData` patch reaches admission already merged into `data`;
  `object.stringData` is not there.
- A map literal indexed by `object.metadata.name` type-checks in a policy
  variable.

_Verified 2026-11-03 on k3d 1.37.1._

## RBAC

### `create` cannot be limited by name

`resourceNames` does not apply to `create`, because the name is not known when
authorization runs. To let someone write exactly one named object, pre-create
it (from Git) and grant `patch` or `update` on that name.

### There is no write-only

The response to a `patch` or `update` is the whole stored object. Whoever may
patch a Secret sees every key in it. Keep one key per Secret if a writer should
only ever see what they just sent, and refuse no-op patches, or `patch` works as
`get`.

### `kubectl patch` needs `get`

`kubectl patch` reads the object first, client side, so a Role with only
`patch` gets `cannot get resource` from kubectl. A bare `PATCH` (curl, the
browser through krm-foyer) works with `patch` alone. Test such a Role with a
raw request, not with kubectl.

## Server-side apply

### A keyed list applied as `[]` is dropped, not emptied

When the owner applies a `x-kubernetes-list-type: map` list as `[]`, SSA removes
the field instead of storing an empty list, so a `required` list fails as
`Required value`. See [runbook-crd-updates.md](runbook-crd-updates.md#empty-keyed-lists).
