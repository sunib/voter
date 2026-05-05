# Coffee Demo Implementation Plan

## Goal
Turn the current quiz demo into the `TestNet Coffee` demo described in [`new-direction.md`](../new-direction.md) while preserving the important platform story:

- configuration is stored in Kubernetes
- configuration is read and edited through the Kubernetes API
- the frontend remains a mobile-first SPA
- the Go service stays small
- ordering can stay lightweight for now

## Added Requirement
The actual coffee configuration object must be edited through the Kubernetes API for both:

- `GET` of current config
- `PUT`/`PATCH`/`POST` style updates of current config

This is a hard requirement because it keeps the demo aligned with the configuration story. The admin/settings flow should not bypass Kubernetes with a private app-only persistence path.

## Recommendation
Split the system into two concerns:

1. **Configuration path**
   - Kubernetes-native
   - real object lives in the cluster
   - admin UI reads and writes that object through the Kubernetes API

2. **Ordering path**
   - backend-owned
   - kept in memory for now
   - not written to Kubernetes yet

This is the cleanest compromise for the current demo.

Reason:
- it preserves the strongest part of the repo narrative: config as data in Kubernetes
- it avoids premature CRD design for orders
- it keeps the submit-time voucher behavior easy to control
- it reduces implementation cost for the first cut

## Deployment Assumption
The first-cut order backend must run as a **single pod / single replica** deployment.

This is an explicit requirement, not an implementation detail.

Reason:
- orders are stored in memory
- voucher usage is stored in memory
- live admin order streams are served from in-memory events

Consequences:
- horizontal scaling is not safe yet
- a pod restart resets in-memory orders and voucher usage
- if this is forgotten and multiple replicas are deployed, voucher depletion and the admin live view will become inconsistent

Recommended first-cut deployment rule:
- set the coffee backend deployment to `replicas: 1`

## Chosen Shape

### Runtime shape
1. Frontend loads `CoffeeConfig` from the Kubernetes API.
2. All clients open a live watch on `CoffeeConfig` after initial load.
3. Frontend keeps the cart locally on the phone.
4. Admin/settings UI reads and writes `CoffeeConfig` through a thin backend that preserves Kubernetes API semantics.
5. Admin UI also shows incoming coffee orders live from the backend.
6. Order submit goes to a simple Go backend endpoint.
7. The backend keeps order history and voucher usage in memory.
8. Voucher exhaustion is only checked and returned during order submit.

### What stays
- static Vue/Vite SPA: [`frontend/`](../frontend)
- Go service: [`auth-service/`](../auth-service)
- Kubernetes API access model and CRD-based configuration

### What changes
- quiz domain becomes coffee domain
- config remains Kubernetes-backed
- order submission stops being Kubernetes-backed for now
- voucher state is no longer shown as authoritative on initial page load

## Data Model

### Kubernetes object
Use one primary config object in Kubernetes:

1. `CoffeeConfig`
   - one object per environment/demo
   - source of truth for storefront, products, vouchers, mail, and payment settings

Suggested shape:

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: CoffeeConfig
metadata:
  name: testnet-coffee
spec:
  shopName: TestNet Coffee
  bannerText: Scan the QR code for your free TestNet coffee
  currency: EUR
  products:
    - sku: coffee-flat-white
      name: Flat White
      priceCents: 395
      description: Double shot with silky milk
      enabled: true
    - sku: coffee-espresso
      name: Espresso
      priceCents: 275
      description: Small, strong, and fast
      enabled: true
  vouchers:
    - code: testnet2026
      enabled: true
      discountType: percentage
      discountValue: 100
      maximumUsage: 3
      appliesToProducts:
        - coffee-flat-white
        - coffee-espresso
      displayMessage: TestNet visitors get free coffee
  mail:
    provider: resend
    apiKeySecretRef:
      name: coffee-mail
      key: api-key
    orderConfirmationTemplate: free-order-confirmation-v1
    fromAddress: coffee@testnet.demo
  payments:
    provider: stripe
    apiKeySecretRef:
      name: coffee-payments
      key: api-key
    mode: test
    zeroAmountCheckoutAllowed: true
```

### In-memory runtime state
Orders do not need a CRD yet.

Keep these in memory inside the Go service:
- successful orders
- failed order attempts if useful for demo metrics
- voucher usage counts derived from successful orders

This means the ordering behavior resets on restart. That is acceptable for the first cut.

## API Plan

## Configuration API

### Live config watch requirement
All clients should open a watch on `CoffeeConfig` so configuration changes become visible without a page reload.

This is a hard requirement for the demo UX.

Expected effect:
- typo fixes in titles or labels appear automatically
- product price changes jump to the new value automatically
- voucher display messaging updates automatically
- clients keep their local cart state while visible config refreshes around it

Recommended implementation:
1. load current config once
2. open a watch on the same `CoffeeConfig` through the thin backend
3. update UI state whenever a new object version arrives

The watch should drive both:
- attendee storefront updates
- admin page updates

The order submit path remains authoritative for voucher depletion, even if config updates live.

### Frontend attendee read path
The attendee-facing storefront can still use a backend-shaped response, but the source of truth must come from Kubernetes `CoffeeConfig`.

Recommended endpoint:

- `GET /public/storefront?voucher=<code>`

Behavior:
- backend reads `CoffeeConfig` from Kubernetes
- backend computes display pricing from the voucher code in the URL
- backend does **not** return authoritative depletion state

Important requirement:
- voucher usage/depletion is **not** exposed here as final truth
- the page may show free-looking prices and voucher-applied messaging
- the actual depletion outcome is only decided on submit

Suggested response:

```json
{
  "shop": {
    "name": "TestNet Coffee",
    "currency": "EUR"
  },
  "voucher": {
    "code": "testnet2026",
    "presentInUrl": true,
    "displayMessage": "TestNet visitors get free coffee",
    "state": "assumed-applied"
  },
  "products": [
    {
      "sku": "coffee-flat-white",
      "name": "Flat White",
      "description": "Double shot with silky milk",
      "basePriceCents": 395,
      "displayPriceCents": 0,
      "voucherState": "assumed-applied"
    }
  ]
}
```

The terms `assumed-applied` or equivalent are useful because they avoid lying in the code while still letting the UI feel optimistic.

After the initial storefront load, the client should also subscribe to the live `CoffeeConfig` watch and recompute the rendered storefront whenever config changes arrive.

Cart reconciliation rule on live config changes:
- if text or prices change, update them in place
- if a product already in the cart becomes disabled or disappears, keep it in the local cart but mark it invalid
- block submit until the invalid item is removed

### Admin/settings read path
Admin/settings must use the Kubernetes API for the actual config object.

Chosen shape:
- frontend talks to a very thin backend proxy that forwards Kubernetes-style get/update/watch operations
- backend remains thin and does not invent a separate config model

Recommended operations:
- `GET /public/admin/coffeeconfig`
- `PATCH /public/admin/coffeeconfig`
- `GET /public/admin/coffeeconfig/watch`

Thin-backend requirements:
- backend reads and writes the real `CoffeeConfig` in Kubernetes
- backend should preserve Kubernetes object semantics closely
- backend should preserve the watch shape closely enough that the frontend still treats it as a real config stream rather than a bespoke polling API

### Admin/settings write path
The admin page should update the real `CoffeeConfig` object through the Kubernetes API.

Important:
- do not create a separate backend-only config persistence model
- do not have a fake `Save` button that writes elsewhere
- standardize on `PATCH` using JSON Merge Patch for the first cut

The illustrative settings page can be simplified, but the write path must still touch the real object.

## Ordering API

### Submit endpoint
Add:

- `POST /public/orders`

Request:

```json
{
  "voucherCode": "testnet2026",
  "source": { "channel": "qr" },
  "items": [
    { "sku": "coffee-flat-white", "quantity": 1 }
  ]
}
```

Behavior:
1. read current `CoffeeConfig` from Kubernetes
2. validate SKUs and quantities against current config
3. recompute prices server-side
4. check in-memory voucher usage for the submitted voucher
5. if usage is exhausted, return failure
6. if allowed, record the order in memory and increment usage

Return stable error codes:
- `VoucherDepleted`
- `VoucherInvalid`
- `VoucherNotApplicable`
- `ProductUnavailable`
- `EmptyOrder`

### Read-only order debug endpoint
Optional but useful for demo/dev:

- `GET /public/admin/orders/debug`

Can return:
- successful order count
- orders by voucher code
- current in-memory usage map

This helps rehearse and reset behavior during local development.

### Live admin order feed
Add a backend endpoint for the admin page to see incoming orders live.

Recommended option:

- `GET /public/admin/orders/stream`

Use Server-Sent Events for the first cut.

Reason:
- simple to implement in Go
- simple to consume from the SPA
- a good fit for append-only in-memory order events
- no Kubernetes watch behavior is needed because orders are not stored there yet

Event payload can include:
- order id
- submitted at
- item list
- total price
- voucher code
- result status such as `placed` or `rejected`
- rejection reason such as `VoucherDepleted`

Also keep a snapshot endpoint:

- `GET /public/admin/orders`

The admin page should use:
1. `GET /public/admin/orders` for initial state
2. `GET /public/admin/orders/stream` for live updates

If SSE proves awkward in the deployed environment, short-interval polling is an acceptable fallback, but SSE is the preferred design.

## Voucher Behavior

### Hard requirement
The actual voucher state is only returned when someone is placing an order.

That means:
- storefront read path should not expose depletion as authoritative fact
- submit path is the only place that can return `VoucherDepleted`

### UI consequence
The attendee flow should intentionally allow this mismatch:

- page appears to offer free coffee
- order button remains enabled
- failure appears only after submit

At the same time, the visible config itself should remain live:

- if a product name is corrected, it updates live
- if a displayed base price changes, it updates live
- if voucher display copy changes, it updates live

That matches the live-demo scenario from [`new-direction.md`](../new-direction.md).

## Frontend Plan

### Routes
Replace the quiz routes in [`frontend/src/router.ts`](../frontend/src/router.ts) with:

- `/` -> order page
- `/admin` -> config + live orders page
- `/thanks` -> order confirmation

### Screens
Replace:
- [`frontend/src/screens/JoinScreen.vue`](../frontend/src/screens/JoinScreen.vue)
- [`frontend/src/screens/AnswerScreen.vue`](../frontend/src/screens/AnswerScreen.vue)
- [`frontend/src/screens/ThanksScreen.vue`](../frontend/src/screens/ThanksScreen.vue)

With:
- `OrderScreen.vue`
- `AdminScreen.vue`
- `ThanksScreen.vue`

### Client state
Replace [`frontend/src/stores/draftSubmission.ts`](../frontend/src/stores/draftSubmission.ts) with a cart store:

- cart items by SKU
- voucher code from URL
- last loaded storefront snapshot
- active `CoffeeConfig` watch state

The cart remains local and device-specific.

### Admin page behavior
The admin page should:
- load the real `CoffeeConfig`
- edit relevant fields
- save back through Kubernetes API semantics
- keep a live watch open on `CoffeeConfig`
- show incoming orders live
- show whether each incoming order succeeded or failed

Recommended layout:
- top section for editable config
- lower section for live order activity

The live order section should show:
- time
- coffee items
- voucher code
- final price
- order result
- failure reason when rejected

The config section should react live as well:
- if the config is edited elsewhere, the form/view updates
- visible titles, labels, and prices should jump to the new values

Admin access should be protected by a simple password gate for the first cut.

Recommended first-cut behavior:
- admin enters a shared password
- backend verifies it
- backend sets an admin session cookie
- admin-only config and order endpoints require that session

This is intentionally simple and ugly, but good enough to start with.

Minimum editable fields:
- voucher enabled
- voucher code
- discount percentage
- maximum usage
- eligible products
- product list and base prices
- mail provider and template settings
- payment provider and mode

Keep the secret references in the config object.

Important:
- the config should reference secrets, not inline secret values
- use Kubernetes-style secret key references with `name` + `key`
- interpret those secret refs as pointing to a Secret in the same namespace as the `CoffeeConfig`
- this is useful for the talk because it shows realistic application config without embedding credentials into Git

Recommended shape:

```yaml
mail:
  provider: resend
  apiKeySecretRef:
    name: coffee-mail
    key: api-key

payments:
  provider: stripe
  apiKeySecretRef:
    name: coffee-payments
    key: api-key
```

Recommended Go modeling:
- prefer a small custom type like `SecretKeyRef { name, key }` for cleaner CRD docs and stricter validation
- interpreting refs as same-namespace is the default behavior
- keeping these refs optional for now is acceptable

Using `corev1.SecretKeySelector` is also valid, but the plan should assume the same API shape either way.

### Attendee page behavior
The order page should show:
- title
- banner text
- products
- obvious prices
- local cart state
- voucher messaging
- `Order` button
- submit-time success/failure state

It should not show authoritative depletion status before submit.

## Backend Plan

### Recommended file layout
Add coffee-specific files:

- `auth-service/coffee_types.go`
- `auth-service/coffee_logic.go`
- `auth-service/coffee_handlers.go`
- `auth-service/coffee_runtime.go`

Likely touched existing files:

- [`auth-service/http_handlers.go`](../auth-service/http_handlers.go)
- [`auth-service/kube_client.go`](../auth-service/kube_client.go)
- [`auth-service/main.go`](../auth-service/main.go)
- [`auth-service/config.go`](../auth-service/config.go)

### Handler responsibilities
`coffee_handlers.go`
- register storefront endpoint
- register submit-order endpoint
- register admin order snapshot endpoint
- register admin order stream endpoint
- register admin config get endpoint
- register admin config patch endpoint
- register admin config watch endpoint
- register admin login endpoint
- optionally register debug/reset endpoints for development

`coffee_logic.go`
- compute display prices from config + voucher code
- recompute actual order totals
- validate order payload
- decide submit-time voucher result

`coffee_runtime.go`
- hold in-memory order store
- hold in-memory voucher usage counts
- hold subscriber channels for live admin updates
- provide mutex protection

`kube_client.go`
- get `CoffeeConfig`
- update `CoffeeConfig`
- watch `CoffeeConfig`

### Auth choice
The attendee order page should not require the existing join-code session model as a prerequisite.

Reason:
- direct visitors must be able to see the storefront
- voucher context can come from the URL
- the demo’s main distinction is voucher presence, not session auth

The admin/config page should be protected separately with a simple shared admin password in the first cut.

Recommended first-cut admin auth:
- backend-configured admin password
- explicit admin login endpoint
- admin session cookie after successful login
- protect config edit endpoints and live order endpoints with that admin session

## Kubernetes Plan

### New manifests
Add:

- `k8s/crds/coffeeconfigs.yaml`
- `k8s/examples/coffeeconfig-testnet-good.yaml`
- `k8s/examples/coffeeconfig-testnet-broken.yaml`

### RBAC
Ensure the backend service account can:

- `get` `coffeeconfigs`
- `update` or `patch` `coffeeconfigs`
- `watch` `coffeeconfigs`

If attendee storefront reads go through the backend, backend also needs:
- `get` `coffeeconfigs`

No order-related Kubernetes RBAC is needed in this phase.

### Validation
CRD validation should enforce:
- positive prices
- valid SKUs
- valid discount types
- non-negative `maximumUsage`
- non-empty product references where required

For secret references:
- use `name` + `key` fields
- same-namespace resolution is assumed
- refs may remain optional in the first cut
- if present, `name` and `key` should both be non-empty

## Dev and Test Plan

### Mock mode
Update [`frontend/dev/kube-mock-plugin.ts`](../frontend/dev/kube-mock-plugin.ts) so dev mode can simulate:

- loading `CoffeeConfig`
- saving `CoffeeConfig`
- submit-time voucher depletion from in-memory counters

Useful dev behavior:
- storefront keeps showing free-looking prices
- first N submit requests succeed
- later requests fail with `VoucherDepleted`
- config updates can be pushed live to connected clients

### Tests
Add Go tests for:
- storefront price rendering from config
- order total recomputation
- in-memory voucher exhaustion
- config update path shape
- config watch event handling if proxied through the backend

The highest-value manual test is still the mobile QR flow.

## Implementation Phases

### Phase 1: Kubernetes config schema
1. Add `CoffeeConfig` CRD.
2. Add good and broken example YAML.
3. Add Go kube client methods for get/update.
4. Wire thin backend get/patch/watch operations and admin password auth.

Exit criteria:
- config object can be created
- config object can be fetched
- config object can be patched/updated
- config object can be watched live
- admin endpoints are password-protected

### Phase 2: In-memory order runtime
1. Add in-memory order store and voucher counters.
2. Add submit-time locking.
3. Add order validation logic.
4. Add live subscriber/broadcast support for admin updates.

Exit criteria:
- service can accept orders without Kubernetes persistence
- voucher usage increments in memory
- admin consumers can receive live order events

### Phase 3: Storefront and submit APIs
1. Implement `GET /public/storefront`.
2. Implement `POST /public/orders`.
3. Keep depletion hidden from storefront response.
4. Return depletion only during submit.

Exit criteria:
- QR visitor sees free-looking prices
- direct visitor sees normal prices
- later submit fails with `VoucherDepleted`

### Phase 4: Frontend swap
1. Replace quiz routes and screens.
2. Add local cart store.
3. Add admin page for real config editing and live order viewing.
4. Wire attendee page to storefront + order endpoints.
5. Open and handle the live config watch on all clients.
6. Handle invalid cart items after live config changes.

Exit criteria:
- attendee flow works on phone
- admin flow edits real Kubernetes config
- admin flow shows orders arriving live
- config edits propagate live to connected clients
- cart blocks submit when a previously selected product becomes invalid

### Phase 5: Demo hardening
1. Add simple reset/debug endpoints for in-memory orders.
2. Improve copy and failure messaging.

Exit criteria:
- demo can be rehearsed and reset quickly

## What Not To Do In The First Cut
- do not write orders to Kubernetes yet
- do not build payments
- do not build mail sending
- do not over-model order persistence
- do not expose voucher depletion state on the storefront read path

## Acceptance Criteria
- `CoffeeConfig` is the real source of truth and is edited through the Kubernetes API
- attendee storefront is derived from that config
- all clients open a live watch on `CoffeeConfig`
- config changes update visible text and prices automatically without reload
- config access is mediated by a thin backend that preserves Kubernetes semantics
- admin writes use `PATCH` with JSON Merge Patch
- admin/settings edits the real config object
- admin page shows incoming coffee orders live
- admin endpoints are protected by a simple password-based login
- cart stays local on the phone
- cart items that become invalid after a config change are visibly marked and block submit
- orders are handled in memory only
- QR visitors can appear to have free coffee available
- direct visitors see normal prices
- voucher depletion is only revealed when the user places an order
- changing `maximumUsage` in Kubernetes config changes the live demo behavior

## Next Step
Implement Phase 1 first: add `CoffeeConfig`, Kubernetes get/update support, and the backend/frontend types around that object. That is the foundation for both the admin page and the attendee storefront.
