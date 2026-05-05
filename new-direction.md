# Coffee App Minimum

This document describes the minimum the existing vote app needs in order to become the TestNet coffee demo.

Goal:

- small enough to build quickly
- strong enough for the live demo
- closely aligned with the configuration story
- concrete without over-prescribing the implementation

## 1. Core demo idea

The app is phone-first.

People scan a QR code and arrive on a small coffee ordering page.  
Everyone who scans the QR code gets the `TestNet` voucher by default.

That voucher is supposed to make the coffee free.

The core bug is:

> the voucher gets depleted because `maximumUsage` is set far too low

Important nuance:

> the depletion error only becomes visible when the user actually places the order

So the UI can still look fine for a while:

- products may still appear as free
- the voucher may still look valid
- the failure only appears at the last moment on submit

That makes the bug more annoying for users and harder to test reliably in advance.

That gives you a very visible live effect:

- a few people successfully place free orders
- then others suddenly fail at submit
- the room gets a natural `huh?`

That is the main failure mode.

## 2. What the app must minimally do

The app only needs to do a few things:

1. show a phone-friendly page after QR scan
2. carry voucher context in the scanned URL
3. show coffee products with prices
4. apply voucher logic to those prices
5. register or simulate an order
6. show clearly when the voucher is depleted or misconfigured
7. expose enough configuration to support a PR + preview demo

That is enough for the talk.

## 3. What to keep from the existing vote app

The current app already has useful properties:

- QR flow
- rolling code / temporary access pattern
- Kubernetes connectivity
- CRDs as backend storage

Those are good assets, because they map well to:

- configuration as data
- environment-aware state
- file-backed configuration
- Git-backed change flows

So the main idea is:

> Keep the platform shape. Change the domain from voting to ordering.

## 4. Main user flows

### QR visitor

- scans the QR code
- lands on the coffee page
- automatically gets the `TestNet` voucher
- expects free coffee
- may only discover at order time that the voucher is already depleted
- keeps the cart locally on the phone

### Direct visitor

- opens the URL manually
- sees normal prices
- does not automatically get the voucher

This path is useful as contrast, but not the center of the demo.

### Presenter flow

- let the audience scan the QR code
- let the first few people succeed
- let the room notice that later people only fail when they actually submit
- show that this is configuration, not a code bug
- then show the PR, preview, and fix

The local cart is an intentional choice here.

It is slightly awkward, but usefully awkward:

- voucher depletion happens centrally
- cart state happens locally
- that mismatch helps make the failure mode visible
- and it keeps the app simple

## 5. Minimum screens

Three screens are enough.

### 1. Mobile order page

Must show:

- page title
- voucher state
- usage state such as `3 / 3 used`
- 2 or 3 coffee products
- price per product
- local cart state
- `Order` button
- simple status message

Important:

The price must be visually obvious.

Examples:

- `€3.95`
- `€0.00`
- `Voucher depleted`
- `Voucher could not be applied`

That is the emotional center of the demo.

### 2. Admin / settings page

This page is mainly illustrative.

It should show:

- voucher enabled
- voucher code
- discount percentage
- maximum usage
- eligible segment or QR audience
- product list
- optional mail/payment settings
- `Save` button

Important:

This does not need to be the real production write path.
It is mainly there to show how teams often do this today.

### 3. Preview / test context view

This can be a block on the preview page.

It should show:

- what change is being tested
- which preview or test version this is
- which config version or commit is active
- that this is not production yet

## 6. Minimum live behavior

For the live demo, the app should behave like this:

### Expected behavior

- QR visitors see free coffee
- direct visitors see normal prices

### Broken production behavior

- QR visitors may still see free prices on screen
- some QR visitors successfully place free orders
- later QR visitors only fail when they submit because the voucher is depleted

That broken behavior should be explicit in the UI.

For example:

- `Voucher applied`
- `Voucher depleted`
- `Free coffee no longer available`
- `Order failed: voucher usage limit reached`

## 7. Minimum data model

The app does not need much.

### Voucher data

- enabled
- code
- discount type
- discount value
- maximum usage
- current usage
- applies to products

### Product data

- sku
- name
- price
- description
- enabled

### Environment context

- environment or branch name
- hostname or URL
- config version or commit

### Order data

- enough data to count or record that an order happened
- enough data to create a `CoffeeOrder` object after submit

For the demo, this does not need to become a full order system.

Important distinction:

- the cart lives locally on the phone
- the submitted order becomes a backend object

That is a good pragmatic split for the demo.

## 8. Example YAML

This is the most useful part of the document: what the configuration could look like in files.

### `app.yaml`

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: CoffeeShopConfig
metadata:
  name: coffee-shop
spec:
  shopName: "TestNet Coffee"
  bannerText: "Scan the QR code for your free TestNet coffee"
  qrUrl: "/?voucher=testnet2026"
  currency: "EUR"
  previewContextEnabled: true
  localCartEnabled: true
```

### `products.yaml`

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: ProductCatalog
metadata:
  name: coffee-products
spec:
  products:
    - sku: coffee-flat-white
      name: "Flat White"
      priceCents: 395
      description: "Double shot with silky milk"
      enabled: true
    - sku: coffee-espresso
      name: "Espresso"
      priceCents: 275
      description: "Small, strong, and fast"
      enabled: true
    - sku: coffee-cappuccino
      name: "Cappuccino"
      priceCents: 375
      description: "Foamy and conference-proof"
      enabled: true
```

### `vouchers.yaml` - good

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: VoucherCatalog
metadata:
  name: coffee-vouchers
spec:
  vouchers:
    - code: "testnet2026"
      enabled: true
      discountType: "percentage"
      discountValue: 100
      maximumUsage: 500
      currentUsage: 0
      appliesToProducts:
        - coffee-flat-white
        - coffee-espresso
        - coffee-cappuccino
      displayMessage: "TestNet visitors get free coffee"
```

### `vouchers.yaml` - broken live demo version

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: VoucherCatalog
metadata:
  name: coffee-vouchers
spec:
  vouchers:
    - code: "testnet2026"
      enabled: true
      discountType: "percentage"
      discountValue: 100
      maximumUsage: 3
      currentUsage: 0
      appliesToProducts:
        - coffee-flat-white
        - coffee-espresso
        - coffee-cappuccino
      displayMessage: "TestNet visitors get free coffee"
```

This is now the primary broken scenario.

### `mail.yaml`

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: MailConfig
metadata:
  name: coffee-mail
spec:
  provider: "resend"
  apiKeyRef: "secret/mail-api-key"
  orderConfirmationTemplate: "free-order-confirmation-v1"
  fromAddress: "coffee@testnet.demo"
```

### `payments.yaml`

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: PaymentConfig
metadata:
  name: coffee-payments
spec:
  provider: "stripe"
  apiKeyRef: "secret/stripe-test-key"
  mode: "test"
  zeroAmountCheckoutAllowed: true
```

### `coffee-order.yaml` example shape

This is not something you store in Git as static config.
This is the kind of object the app creates after a successful order.

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: CoffeeOrder
metadata:
  generateName: coffee-order-
spec:
  voucherCode: "testnet2026"
  voucherApplied: true
  totalPriceCents: 0
  items:
    - sku: coffee-flat-white
      name: "Flat White"
      unitPriceCents: 395
      finalPriceCents: 0
      quantity: 1
  source:
    channel: "qr"
  status: "placed"
```

## 9. Main business logic

The app only needs these rules:

1. products have a base price
2. a valid voucher can reduce that price to zero
3. voucher exhaustion is enforced when the order is submitted
4. only enabled products are shown
5. a voucher can be limited to selected products
6. zero-price checkout must still be allowed by payment config
7. placing an order creates a `CoffeeOrder` object
8. cart state stays local until submit

That is enough to support the full talk.

## 10. Core bug scenarios

You only need a few.

### Scenario A: voucher depletion

Example:

```yaml
maximumUsage: 3
```

Effect:

- the first few people can successfully place free orders
- later people only discover the problem on submit
- the room feels the failure

This is now the main scenario.

### Scenario B: voucher does not apply to coffee

Example:

```yaml
appliesToProducts:
  - merch-sticker
```

Effect:

- voucher exists
- voucher looks enabled
- coffee still shows normal prices

Good as a backup deterministic scenario.

### Scenario C: bad product data

Example:

```yaml
- sku: coffee-testnet-special
  name: "TestNet Specail"
  priceCents: 1295
  description: "Seasonal coffee for testers"
  enabled: true
```

Effect:

- typo is visible
- price looks obviously wrong

Good as the second PR or secondary review story.

## 11. Preview checks

The preview environment should ideally run checks like:

1. `qr visitor receives voucher context`
2. `voucher-depleted error is returned when usage limit is reached`
3. `voucher-active state allows successful zero-price order when usage is below limit`
4. `enabled products have valid positive base price`
5. `preview shows test context`
6. `zeroAmountCheckoutAllowed is true when voucher discount is 100%`
7. `submitting an order creates a CoffeeOrder object`

Optional:

8. `new product is visible in preview`
9. `display message matches voucher state`

That is enough to support the testing story.

## 12. Minimum backend support

Without prescribing implementation too much, the backend should be able to:

- load configuration data
- load product data
- evaluate voucher usage
- register or count orders
- create a `CoffeeOrder` object on submit
- expose environment context when needed for preview/test use

It does not need to:

- perform real payments
- send real email
- be a full transactional commerce backend
- store the cart server-side before submit

## 13. Why CRDs fit nicely

If the current app already talks to Kubernetes and can use CRDs, that is actually a good match.

Why:

- voucher data is declarative
- product data is declarative
- order objects are a natural resource shape
- it maps well to files
- it maps well to Git

Conceptually, resources could be:

- `CoffeeShopConfig`
- `ProductCatalog`
- `VoucherCatalog`
- `MailConfig`
- `PaymentConfig`
- `CoffeeOrder`

You do not need to emphasize all of that in the talk, but it is a strong internal model.

## 14. Why this supports the Git and PR story

This app is strong for the talk because the important behavior lives in reviewable data:

- voucher rules
- maximum usage
- product names
- prices
- descriptions
- zero-price checkout behavior

And importantly:

- the failure is late in the flow
- which makes it exactly the kind of thing you want to catch before production

That means:

- the configuration can live in files
- the files can live in Git
- the PR can show exactly what changed
- the preview can rebuild and test that change
- the audience can verify the effect before production

That is almost the full point of the talk.

## 15. What you do not need

Do not build these unless you discover the demo really needs them:

- real payment integration
- real mail provider
- login
- complete order history
- stock management
- full back office
- complex checkout flow

Smaller is better.

## 16. Minimum technical principles

Without being overly prescriptive, these are good boundaries:

- mobile first
- data-driven UI
- configuration separate from code
- preview context visible
- easy demo overrides

For the live demo, you want to be able to force:

- QR visitor path
- voucher active
- voucher depleted

So the live demo stays controllable.

## 17. Recommended first working version

If the goal is to get to a working demo quickly, start with exactly these files as the functional core:

1. `app.yaml`
2. `products.yaml`
3. `vouchers.yaml`

And show these config areas visibly in the app:

- voucher state
- product prices
- preview/test context

Then add, only if useful:

- `mail.yaml`
- `payments.yaml`

That is already enough for a very strong live demo.
