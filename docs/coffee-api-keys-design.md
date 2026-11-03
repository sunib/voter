# Mail and payments keys the room can set

_2026-11-03. P1 is built (the CRD, the policies, the fixture and
`test/browser/coffee-keys.spec.js`); P2 to P4 are design. There is no rule on the
key's value: admission cannot read it (decision 5), and any length is fine.
Answers the open question in
`external/k8s/k8s.koudijs.dev/docs/settings-image-publishing-plan.md` §2 ("Which
Secret goes with the menu, who writes it, and who reads it?") for demo beat 2.
Items marked **to verify** are believed true from docs or source but have not
been run._

## What we want

The CoffeeConfig already has two references to an API key, one for mail and
one for payments. Someone in the room can push a new (fictional) value into
either: a SendGrid key for order confirmations, and a payment provider's key
for checkout. The menu keeps referring to them the way it does now:

```yaml
spec:
  mail:
    provider: sendgrid
    apiKeySecretRef:
      name: coffee-mail
      key: apiKey
  payments:
    provider: stripe
    apiKeySecretRef:
      name: coffee-payments
      key: apiKey
```

Two things make this harder than adding a field:

1. **Setting a key should be a deliberate step**, not something 200 phones can
   do every two seconds.
2. **A reference must not point at the wrong Secret.** Today either one can
   point at anything, including at the other's key.

## What is true today

`spec.mail.apiKeySecretRef` and `spec.payments.apiKeySecretRef` are free text:
two inputs each in [AdminScreen.vue](../frontend/src/screens/AdminScreen.vue#L1243-L1342),
`{name, key}` in [coffee_types.go](../voter/coffee_types.go#L11-L14) and in the
CRD, with no pattern and no required fields. `provider` is free text too, and
nothing in the repository picks one. **Nothing reads the references.** No
controller, no backend code and no app resolves them, so a wrong one is
harmless *today*.

It stops being harmless as soon as anything resolves them. Namespace `voter`
holds the demo's real keys:

| Secret | What reading it gives you |
| --- | --- |
| `coffee-settings-age` | the private age key that decrypts **every** Secret in the public `ConfigButler/coffee-settings` repository |
| `coffee-settings-git`, `k8s-trail-git` | write deploy keys to those two repositories |
| `k8s-audit-trail-signing` | the bot's SSH signing key: commits that show Verified |
| `voter-oidc-client`, `room-pass-cookie`, `krm-foyer-session-keys`, `voter-app-cookie` | login and session keys |

Whatever resolves `apiKeySecretRef` reads with **its own** rights, not with the
rights of whoever wrote the reference. If that reader can get every Secret in
the namespace, then `apiKeySecretRef: {name: coffee-settings-age}` turns a
menu edit into a key read. This is the confused-deputy problem. Notice also that
`coffee-settings-age` and `coffee-settings-git` start with `coffee-`, so a
`coffee-*` name rule would let exactly the worst ones through.

## The design in one picture

```text
/room: operator gives "Keys" to one person              (RoleBinding, like the menu's -person grant)
  person writes a new mail key, a new payments key,     PATCH secret coffee-mail / coffee-payments,
  or both                                               own token, via /k8s
    admission: one entry, value actually changes, no dry run, no metadata
    gitops-reverser -> coffee-settings: coffee/resources/secrets/voter/coffee-mail.sops.yaml
                                        coffee/resources/secrets/voter/coffee-payments.sops.yaml
                                        (ciphertext, authored by them)              <- demo beat 2
  coffee-settings CI -> a new settings image, a new digest
  Flux -> the third coffee app rolls onto it            the previous digest stays in zot, unchanged
each reference is fixed: mail -> coffee-mail/apiKey, payments -> coffee-payments/apiKey, or absent
the consumer resolves them INSIDE the bundle, never against the API
```

There are two Secrets and two references, and the references never move. The
person with the grant can push a new value into either one, or neither. There
are no extra slots and no "switch to it" step. Versions and rollback come from
the loop this repository already has (decision 3).

## Decisions

### 1. Two fixed Secrets, one per reference

| Reference | Secret | Key |
| --- | --- | --- |
| `spec.mail.apiKeySecretRef` | `coffee-mail` | `apiKey` |
| `spec.payments.apiKeySecretRef` | `coffee-payments` | `apiKey` |

**The value is whatever the person types**, of any length: a word like `taart`,
one character, or something the size of a real key. It stays secret. Nothing
checks it (decision 5), so the demo relies on people typing a made-up word,
not on a rule.

Both are **pre-created by Flux** from `external/k8s` (SOPS, like every Secret
there), with:

- an **empty** value, which consumers read as "not configured". A person can
  empty a key again too;
- the label `voter.configbutler.ai/coffee-settings: "true"`, so the existing
  coffee-settings WatchRule picks them up (git-sink-coffee-settings.yaml);
- `kustomize.toolkit.fluxcd.io/ssa: IfNotPresent`, so the room's writes are not
  reverted. This is the same choice as the seeded CoffeeConfig in `app.yaml`.

One Secret per reference, not one Secret with two keys: each key is written,
committed and refused on its own. And the rule in decision 5 that a patch
response may only carry what this person wrote needs exactly one key per
Secret.

The CRD keeps `apiKeySecretRef: {name, key}` on both, even though the values are
fixed. Each reference still states its dependency where a reader of the menu
can see it, the CRD keeps its shape, and the reference can later name a typed
key (see the side notes). The CRD itself pins the values (decision 4).

### 2. Writing keys is its own grant, given to one person

A new Role in `participant-rbac.yaml`. As with the other live grants, the Role
is in Git and its binding is not:

```yaml
kind: Role
metadata:
  name: voter-audience-coffee-keys
rules:
  - apiGroups: [""]
    resources: [secrets]
    resourceNames: [coffee-mail, coffee-payments]
    verbs: [patch]
```

- **`patch` only, on two names.** No `create`, because RBAC cannot restrict
  `create` by name. That is also why the Secrets are pre-created. No `get`,
  `list`, `watch` or `delete`.
- **Bound from `/room` to one person at a time** (`…-coffee-keys-person`), the
  same picker and code path as `voter-audience-coffee-admin-person`
  (`grantPerson` in [roomGrants.ts](../frontend/src/api/roomGrants.ts)). This is
  the real answer to "not every two seconds": one named person holds the grant
  at a time, the operator can see who it is, and it can be taken back.
- **One grant for both keys**, so `/room` gets one row, not two. To hand mail
  and payments to different people instead, split this into two Roles of one
  name each. Nothing else in the design changes.
- The write goes through krm-foyer's `/k8s` with **the person's own token**, as
  every other write does. So the audit event, and therefore the commit in
  coffee-settings, names them. `secrets` must never be added to krm-foyer's
  `sharedWatches`.

### 3. Versions come from the loop, not from the Secrets

Blue/green is already in the system, for the whole settings bundle rather than
for one Secret:

- each write is a commit in coffee-settings, authored by whoever made it. Two
  key writes within the 5 s window land in one commit;
- each commit becomes a settings image with its own **immutable digest**, tagged
  `main-<commit time>-<sha7>`; settings-image never deletes, so every earlier
  digest stays pullable;
- the third coffee app runs exactly one digest, written into `external/k8s` by
  Flux image automation, and a rolling update keeps the old pod serving until
  the new one is ready.

A second slot per key would rebuild this for one Secret only, and add a "switch
to it" step for the room to get wrong. With one Secret per reference, a new key
goes live the way a price change does.

**What rolling back actually means here.** In this loop the **cluster is the
source**, and Git and the image are copies of it. So rolling back means pinning
the third app to an older digest. Reverting a commit in coffee-settings does not
work: the reverser writes the cluster's value back on its next resync. Three
consequences:

- the pin has to stop the image automation too (suspend it, or drop the
  `$imagepolicy` marker for the duration), or it rewrites the line to the
  newest tag. `kubectl rollout undo` lasts only until Flux corrects the drift,
  and has not been tried yet (`docs/settings-image-loop.md` in external/k8s,
  "Open");
- a rollback restores the **whole bundle**: the menu and **both** keys as they
  were, even when only one of them was bad;
- the cluster Secrets keep the new keys, so the **next menu edit publishes them
  again**. Nobody can type an old key back, because nobody can read it. Only
  someone holding an age key can decrypt it from Git. A rollback is a way to
  hold the line while somebody pushes a good key, not a way back.

### 4. The CRD pins each reference to its one value

The schema allows exactly one value per field, in
`voter/config/crd/coffeeconfigs.yaml` and its copy in `external/k8s`
([runbook-crd-updates.md](runbook-crd-updates.md)):

```yaml
mail:
  properties:
    apiKeySecretRef:
      type: object
      required: [name, key]
      properties:
        name: {type: string, enum: [coffee-mail]}
        key:  {type: string, enum: [apiKey]}
payments:
  properties:
    apiKeySecretRef:
      type: object
      required: [name, key]
      properties:
        name: {type: string, enum: [coffee-payments]}
        key:  {type: string, enum: [apiKey]}
```

- Each reference is absent, or exactly its own Secret. Neither can name the
  other's key: mail cannot send with the payments key.
- **The schema holds for everyone**: a person, Flux, gitops-reverser, every
  namespace that has a CoffeeConfig. A reference to `k8s-trail-git` is no more
  sensible for coming from Git. demo-production gets the same rule from the same
  CRD, so its own Secrets, once they exist, carry the same names.
- **It is the API, not a side rule.** `kubectl explain`, the OpenAPI document
  and any editor read the one allowed value straight from the schema. The menu
  editor can fill the reference in from it instead of keeping its own copy. A
  refusal is the API server's own `Unsupported value: "k8s-trail-git":
  supported values: "coffee-mail"`.
- **"The change, not the menu" comes free.** CRD validation ratcheting (GA since
  Kubernetes 1.33; the nodes run 1.37) lets an object whose stored reference
  already breaks the enum be updated, as long as that field is left alone. This
  is what the price policy does by hand. Verified on k3d 1.37 for the enum,
  `required` and the CEL rule below. The CEL rule differs in one way: it lets
  the edit through **with a warning**, on every write, until the menu is fixed.

**A CEL rule for what the enum cannot say.** An enum judges one field on its
own. Whether a reference belongs there at all depends on its neighbour,
`provider`, and that is a rule for `x-kubernetes-validations` on `mail` and on
`payments`:

```yaml
mail:
  type: object
  x-kubernetes-validations:
    - rule: >-
        (has(self.provider) && self.provider != '') == has(self.apiKeySecretRef)
      message: >-
        Mail needs a provider and its key reference together, or neither: a
        provider without a key cannot send, and a key without a provider is never
        used.
```

The same rule goes on `payments`, with its own message. This is the rule behind
"the editor fills a reference in when its provider is set" (What the screens
do): the editor makes it easy, and the schema makes it true for `kubectl` and
Flux too. Until P3 the editor still has free-text fields for both. A save sends
every changed field in one patch, so filling in a provider and its reference
together works; filling in one of them gets this message in the "Admin request
failed" panel.

Why the enum stays rather than becoming CEL as well (`self.name ==
'coffee-mail'`): an enum is part of the OpenAPI document, so `kubectl explain`,
editors and the menu editor can read the one allowed value. A CEL rule is
opaque to all of them. CEL gives a kinder message than the enum's `Unsupported
value`, but not enough to be worth losing that. If the message matters more,
replace the enum with CEL; do not keep both, or one mistake comes back as two
errors.

**What CEL cannot do here** is look at the Secret a reference names: whether it
exists, or what it holds. A rule in the API server sees only the object
being written, never another one. That is why the checks on a key sit on the
Secret itself (decision 5), and why the Secrets are pre-created (decision 1).

Pinning a value in the schema mixes a deployment choice into the API: another
name means a CRD change, not a policy edit. Here that costs nothing. The CRD is
this demo's own (`examples.configbutler.ai`), it has one deployment, and a
typed key (side notes) would change this field anyway.

The schema cannot check that a Secret holds a real key, and it does not need
to: both Secrets always exist (decision 1), and an unset one reads as unset
(decision 6). The checks on the Secrets themselves (decision 5) stay a policy,
because a Secret is a built-in type and has no schema of ours to put them in.

### 5. Admission on the Secrets: the value changes, and nothing else does

**The value itself is not checked.** Any length is fine. The first plan was a
rule of 1 to 5 letters or digits, as CEL:
`string(object.data.apiKey).matches('^[A-Za-z0-9]{1,5}$')`. On k3d it never
matches: in admission a Secret's value is its base64 text, and CEL cannot decode
it ([kube-api-limitations.md](kube-api-limitations.md#a-secrets-data-is-base64-text-in-cel-and-cel-cannot-decode-it)).
A regex on the base64 shape could still pin the length, but a short word is a
habit for the demo, not something to enforce. Whoever pastes a real key is
allowed to: it lands in the audit log and, encrypted, in coffee-settings, and
that is on them.

Policy `voter-coffee-keys`
([coffee-keys-policy.yaml](../voter/config/admission/coffee-keys-policy.yaml)),
matched on updates with `matchConstraints.resourceRules[].resourceNames:
[coffee-mail, coffee-payments]`. A ValidatingAdmissionPolicy, not a webhook:
every rule is a line of CEL in the API server, so there is no server to run.

For people (not `system:`), it requires:

| Rule | Why |
| --- | --- |
| `data` has exactly one key, `apiKey` | the response to a patch carries the whole object; one key means it carries only what this person wrote |
| not a dry run | a dry run would let anyone test a guess against the stored key without changing it: refused means the guess was right |
| `apiKey` differs from the stored value (comparing the base64 text) | a no-op patch would return the previous person's key, so `patch` would work as `get`; refusing the no-op closes that. Verified: a no-op patch still goes through admission |
| labels, annotations, finalizers, owner references, `type`, `immutable` unchanged | the reverser encrypts only `data`/`stringData` (`encrypted_regex`). An annotation such as `kubectl apply`'s `last-applied-configuration` contains the value **in plain text**, and coffee-settings is public. `immutable: true` would freeze the key for everyone, Flux included |

And a second policy, `voter-coffee-settings-label`, for **everyone**, on every
Secret in the cluster that carries the label `voter.configbutler.ai/coffee-settings`:
only `coffee-mail` and `coffee-payments` may. People cannot label Secrets anyway,
so this protects against the operator's own mistakes: label `coffee-settings-age`
by accident and it would be published, encrypted to itself, next to the menu. A
match condition on the label keeps every other Secret write away from it.

**Nothing derived from a key is shown.** People will mostly type short words,
and a short word is found from its hash, its last four characters or a
fingerprint in seconds. Nothing in this design shows any of them (decision 6, P4,
and the signing side note). Encryption is unaffected: SOPS encrypts with a random
key, not one derived from the value. The ciphertext's length gives away the
value's length, and that is accepted.

### 6. The consumer resolves each reference inside the bundle

Whatever reads the settings image (the third coffee app, or later a real mail
sender or checkout) resolves both references **against the Secrets in the
image**, never against the Kubernetes API. Only labelled Secrets reach the
image, so any other reference resolves to nothing, even if admission were ever
bypassed. This is the decision that keeps the confused deputy out for good;
decision 4 makes the mistake impossible to write down in the first place.

If something ever resolves a reference live in the cluster instead, it gets
`get` with `resourceNames: [coffee-mail, coffee-payments]` and nothing wider.
It reports an empty value as "not configured", and never echoes a value, not
even part of one (decision 5).

### 7. Off by default, per key

Voter gains `COFFEE_MAIL_KEY_SECRET` and `COFFEE_PAYMENTS_KEY_SECRET` (both
empty by default) and `AUDIENCE_COFFEE_KEYS_ROLE`, published in `/config.json`
the way `AUDIENCE_COFFEE_ADMIN_ROLE` is. Each setting turns on its own half: with
only the mail one set, payments keeps its free-text fields. With both empty, no
screen changes. A `feat:` release of this changes nothing in demo or
demo-production until `app.yaml` sets them.

## What the screens do

- **`/room`**: a "Keys" row beside the menu grant, with the same "pick someone"
  control. It shows who holds it.
- **Menu editor, mail and payments sections** (when configured): each reference
  is shown, not edited: "Mail key: `coffee-mail`", "Payments key:
  `coffee-payments`". A text field for a value that has one allowed answer is
  how wrong references get typed. The editor fills a reference in when its
  provider is set, so a menu edit cannot leave it half done.
- **"Set a new key"**, shown only to the person whose permissions include
  `patch` on these Secrets (the watchable-permissions machinery already answers
  that): one password input **per key**, with no length limit, each with its
  own Save, and no echo of the value afterwards. Each Save is one patch of one
  Secret, so a refusal names the key it was about.
- **The reveal** is a stage beat, not a screen: the key-holder has kept the key
  to themselves, and Simon opens the commit's `.sops.yaml` with `sops decrypt`
  and reads it out. The room sees that only someone with the age key could.
- A refusal comes back as the policy's own message in the existing "Admin
  request failed" panel, as the price policy's does now.

Showing whether a key is set, and since when, needs something that can read the
Secret. That is optional, in phase 4. Until then the proof is the commit in
coffee-settings, and that commit is beat 2 anyway.

## Not every two seconds

| What limits it | How hard |
| --- | --- |
| One named person holds the grant, given and taken back from `/room` | hard: RBAC |
| A patch must change the value, and not be a dry run | hard: admission |
| Commits are batched (5 s idle, 1 min max window), and a settings image is built per commit | soft: churn is cheap but visible |
| The UI disables a Save for a few seconds after a write | cosmetic only |

There is **no time-based limit** ("once per minute"), deliberately. CEL in
admission has no clock, and API Priority and Fairness shares capacity between
callers; it does not set quotas. A real cooldown would need a webhook or a
controller, and with one person holding the grant there is nothing left for
it to do.

## Costs we accept

- **The audit log carries the values.** `1-talos/files/audit-policy.yaml` logs
  Secrets at `RequestResponse` on purpose, for authorship. Accepted: the keys
  are made-up words for a demo, and someone who pastes a real key puts it there
  themselves (decision 5).
- **The writer sees their own key in the patch response.** Write-only does not
  exist in Kubernetes RBAC. With one key per Secret and no-op patches refused,
  they only ever see the value they just sent.
- **A bad key goes live at the next rollout.** There is no staging slot. The way
  back is a digest pin and a new key (decision 3), not a switch back, and the pin
  takes the other key back with it.
- **coffee-settings is public.** The Secrets' names, their labels and the
  ciphertext are visible; the keys are not.
- **demo-production gets the references but no Secrets.** k8s-trail's
  `configs/coffee/` carries the CoffeeConfig only, so in `demo-production` both
  references resolve to nothing. That is correct until production has Secrets of
  its own, delivered by Git.
- **A namespace rebuild resets both keys** to empty (IfNotPresent seeding), the
  same as the menu.
- **The ciphertext gives away the key's length.** Nothing else about it
  (decision 5).

## Phases

| Phase | Where | Ends when |
| --- | --- | --- |
| **P1** CRD + policy (**built**) | voter: `coffeeconfigs.yaml` (the enums, `required` and the CEL rule), [coffee-keys-policy.yaml](../voter/config/admission/coffee-keys-policy.yaml) (both policies), the Role and the two Secrets in `test/e2e/voter.yaml`, [coffee-keys.spec.js](../test/browser/coffee-keys.spec.js) | a ref to `k8s-trail-git`, a mail ref naming `coffee-payments`, and a provider without a ref are refused by the schema; a second key, a no-op patch, a dry run and a metadata change by the policy; the label on another Secret by the second one. Passing on the fixture, with the other 27 browser tests, on 2026-11-03. Ratcheting checked by hand on k3d 1.37 |
| **P2** cluster | external/k8s: the two Secrets, the Role, the copied CRD and policies. The live `demo-coffee` already meets the new CRD: its free-text references (`Tes`/`Ter` with no provider, `Haa`/`Ns`) were set to `sendgrid` + `coffee-mail` and `stripe` + `coffee-payments` on 2026-11-03. The coffee-settings sink it writes into is live (Ready, committing since 2026-11-03) | a hand-run `kubectl patch` of each, as a test user, lands as `coffee/resources/secrets/voter/<name>.sops.yaml`, authored by them, and opens with `sops decrypt` |
| **P3** Voter UI | voter: `/room` grant, the fixed references in the editor, "Set a new key" for each, behind the two settings | the rehearsal: one phone sets both keys, a new digest, the third app rolls; then a digest pin brings the previous one back |
| **P4** (optional) key status | voter backend: `get` on the two names; returns `{name, set, updated}` only, never any part of the key | the editor shows "mail set 2 min ago; payments not set" |

## To verify

Checked on k3d 1.37.1 on 2026-11-03, before P1:

- **Holds.** Ratcheting: a CoffeeConfig with a wrong ref, a half-filled ref, a
  mail ref naming `coffee-payments`, or a provider without a ref can still have
  a price changed. The enum and `required` ratchet silently; the CEL rule lets
  the edit through with a warning on every write.
- **Holds.** Validating admission sees `stringData` already merged into `data`.
- **Holds.** A no-op patch and a `dryRun=All` patch both go through validating
  admission.
- **Holds.** A map literal indexed by `object.metadata.name` type-checks in a
  policy variable.
- **Fails.** `string(object.data.apiKey)` gives the base64 text, not the word,
  and CEL has no `base64.decode`. The value is not checked at all (decision 5).

Still open:

- Whether gitops-reverser strips `last-applied-configuration` from a mirrored
  Secret. Decision 5 forbids it either way. If it does not strip it, Simon's own
  `kubectl apply` of a Secret is a plain-text leak in **every** sink, not only
  this one.
- The digest pin in decision 3: which of suspending the ImageUpdateAutomation
  or removing the `$imagepolicy` marker is the cleaner way to hold an older
  digest, and that Flux then leaves it alone. Also that a hand revert in
  coffee-settings really is overwritten on the reverser's next resync, as
  decision 3 assumes from `prune: Always` and its reconcile commits.

## Side notes

### This is what happens when configuration and infrastructure share a namespace

Most of this design exists because the menu lives in `voter`, next to the
platform's own keys. Look at what decisions 4–6 defend against: the deploy
keys, the signing key, the age key and the session keys. None of them are
coffee configuration. They are the infrastructure that runs the demo.

If the coffee configuration had a namespace of its own, holding only the
CoffeeConfig and coffee's Secrets, the worst wrong reference would point at
another coffee Secret. A reader with rights over the whole namespace could
leak coffee settings, not the keys to the platform. The pinned names, the
converse label check and most of the worry would shrink to almost nothing. So
this is a good, concrete argument for keeping configuration and infrastructure
apart, and it came up without anyone looking for one.

**The conceptual problem stays, though.** Even in a namespace of its own, the
mail reference can name the payments key, and whatever resolves it still reads
with its own rights, not the author's. Separating the namespaces limits how much
damage a wrong reference can do. It does not remove the confused deputy.
Decision 6 (resolve inside the bundle, read with `resourceNames` only) is needed
either way.

### A typed key object instead of an opaque Secret

Last week's slides proposed a dedicated object for this: a generic `ApiKey`,
or one kind per provider, such as `SendGridKey` and `StripeKey`. For a provider
used widely enough, the schema itself is worth a lot:

```yaml
apiVersion: keys.configbutler.ai/v1alpha1
kind: SendGridKey
metadata:
  name: coffee-mail
spec:
  keyName: coffee-order-confirmations   # the key's name in SendGrid
  scopes: [mail.send]                   # what it is allowed to do
  expiresAt: "2027-01-31T00:00:00Z"
  secretRef:
    name: coffee-mail                   # the value itself, still a Secret
status:
  conditions:
    - type: Verified                    # checked against SendGrid, if anything does
```

Compared with a plain Secret:

- **The reference gets a type.** `apiKeyRef: {kind: SendGridKey, name: …}` on
  mail and `{kind: StripeKey, …}` on payments, each `kind` an enum in the CRD,
  means mail can only ever name a SendGrid key and payments a Stripe key. The
  schema checks the *kind* instead of one pinned name, so a menu can choose
  among several SendGrid keys without the CRD listing them. A deploy key still
  can never be named, because it is not a `SendGridKey`.
- **The format belongs to the kind.** The key's real pattern (`SG.<22>.<43>`,
  `sk_live_…`) is validated in the same place as the rest of the schema, and
  each kind refuses the other's keys. A demo keeps decision 5's unchecked
  value, where telling keys apart is not the point.
- **The metadata is safe to show and to commit.** Expiry, key name, scopes and,
  for payments, live or test mode are not secret. They can go into Git in plain
  text and appear on a page, which answers phase 4's "is the key set, and which
  one?" without reading the Secret. The menu's `payments.mode` could then be
  checked against the key's own mode.
- **It can be checked, depending on the other side.** Admission can refuse a key
  whose `expiresAt` has passed or that is about to. A controller could go
  further and ask the provider whether the key exists and has exactly the
  declared scopes. How far that goes depends on what each provider's API offers
  (**to verify** for SendGrid and Stripe). Expiry may well be our own policy
  rather than something the provider enforces.

That suggests something ConfigButler could ship: **a standard set of typed key
kinds for the providers everybody uses** (SendGrid, Stripe, GitHub, and so on),
each with its format, its metadata, its admission rules and, where the
provider allows it, a check against the real service. Applications would
reference a kind instead of "some Secret, key `apiKey`". gitops-reverser would
mirror the typed object in plain text and only the Secret encrypted.

This design does not wait for that. Pinned Secrets plus admission work with
today's CRD and today's Secrets. If typed keys arrive later, `coffee-mail`
becomes a `SendGridKey`, `coffee-payments` a `StripeKey`, and each reference
gains a `kind`.

### Checking a new key: where in the journey?

This design checks only the **shape** of a key. Whether the key **works**
(SendGrid accepts it, it has `mail.send`, it has not expired) is a question for
the other side, and there are several places on the way from phone to running
app where it could be asked. Each one changes what the journey feels like.
This is worth investigating properly, not just picking one.

| Where | What it gives | What it costs |
| --- | --- | --- |
| **Admission webhook**, at the moment of writing | a key that does not work is never stored, and the person gets the refusal on their phone right away | the API server waits on the provider (timeout at most 30 s); the provider being down blocks every write, or (`failurePolicy: Ignore`) the check is silently skipped; a server to run, and it sees every plaintext key. CEL cannot do this, because it cannot call out |
| **A controller, after the write** | a `Verified` condition on a typed key (previous side note), without blocking anyone | the bad key is already stored, committed and maybe built into an image; the condition informs, it does not stop anything unless something downstream reads it |
| **The consumer's readiness probe** | the cheapest: the new pod checks its key at startup, is not Ready if the key fails, and the rolling update stalls with the **old pod still serving**. The blue/green from decision 3 becomes a guard against bad keys, not only a way back | it catches the key at the very end, in one app; the room sees "rollout stuck", not "your key was refused" |
| **A PR, before production** | the shape that already exists for production: the room's edit lands on a branch, and a merged PR moves it to main (k8s-trail's `coffee-change`). A check on the PR makes the key part of the review | CI must decrypt to check, so it needs an age key, which is a secret in CI, the very thing coffee-settings was built to avoid. And a reviewer cannot see an encrypted value, so what are they approving? |

The last row leads somewhere interesting: **the PR becomes a signing phase.**
The reviewer cannot read the key and does not need to. What they approve is
everything around it: who pushed it, when, its name and scopes and expiry (in
plain text, if it is a typed key), its fingerprint, and the result of a check
that ran somewhere trusted. A plain SHA-256 is a safe fingerprint for a real,
long random key, but not for a short demo word, which a computer finds
from its hash in seconds (decision 5). For the word, the fingerprint has to be
keyed (an HMAC with a key the cluster holds), or left out.
Merging is then a signature over that set, and it could be a real one: an
attestation on the settings image's digest ("key with fingerprint F, checked
against SendGrid by X at T, approved by Y") that production verifies before it
runs the image. Flux can verify signatures on OCI artifacts (**to verify** for
attestations of our own type). The demo room would keep writing freely, while
production only ever runs keys someone checked and signed.

For a demo the "other side" is fictional too. A small fake provider in the
cluster would let every row in the table be shown on stage, including the
refusal. With words for keys it can behave like a real provider: the operator
"issues" a word there, and only that word passes the check. Then the key-holder
has to bring the right word to the menu, and a typo is refused the way a revoked
key would be.

Questions to take further:

- Which refusal is the best experience: on the phone at write time (webhook), on
  `/room` as a stalled rollout (probe), or in a PR (review)? Probably more than
  one, at different stages: shape at write time, function at rollout, approval
  before production.
- Can the check run without anything outside the cluster ever holding a
  decryption key? A check inside the cluster, writing a signed result that the
  PR only displays, would keep coffee-settings' "no secret anywhere".
- Is "a merge as a signature over a checked key" something ConfigButler could
  offer generally, together with the typed key kinds above?

## Open, for Simon

| Question | Default if unanswered |
| --- | --- |
| The Secrets' names | `coffee-mail`, `coffee-payments` |
| The value's rule | none, any length (decided 2026-11-03: admission cannot read it, and the demo does not need one) |
| One grant for both keys, or one per key (two people)? | one grant, one person: that is the rate limit, and `/room` gets one row |
