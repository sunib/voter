# Changelog

## [2.4.2](https://github.com/sunib/voter/compare/v2.4.1...v2.4.2) (2026-10-29)


### Bug Fixes

* read a CommitRequest's commit from status.commit ([cfd192f](https://github.com/sunib/voter/commit/cfd192f124e262c7c558344244bcc9e2af451eb3))

## [2.4.1](https://github.com/sunib/voter/compare/v2.4.0...v2.4.1) (2026-10-29)


### Bug Fixes

* Let's get the page simpler and let's redirect to the coffee ordering ([219acff](https://github.com/sunib/voter/commit/219acff01719d4c0e8c91dfcd1c613f711ca3b14))

## [2.4.0](https://github.com/sunib/voter/compare/v2.3.0...v2.4.0) (2026-10-29)


### Features

* round of updates, lets hope that its not a feat actually haha ([3609a5d](https://github.com/sunib/voter/commit/3609a5d492fb2687209e366345fd0770b3920cba))

## [2.3.0](https://github.com/sunib/voter/compare/v2.2.0...v2.3.0) (2026-10-29)


### Features

* let's fix the mistake with the empty vouchers, that's a nice catch ([9aa6366](https://github.com/sunib/voter/commit/9aa636673f4e8627173e3409af55e418784526d3))
* Let's get a new release out in a more standaridzed way ([e003b63](https://github.com/sunib/voter/commit/e003b63a1643e3fa800df0b99422df5057c0104c))

## [2.2.0](https://github.com/sunib/voter/compare/v2.1.2...v2.2.0) (2026-10-28)


### Features

* "Room admin", under "Not today" with the other pages Friday skips ([0214e2d](https://github.com/sunib/voter/commit/0214e2dce0402115315be932571ba7566a0dc8c1))
* a Seat CRD, one per participant, named after its place ([3f97bdf](https://github.com/sunib/voter/commit/3f97bdfb1840342c81cb2b8313aa580a7135a5cb))
* a SeatingPlan, and every Seat points at it ([cedefe1](https://github.com/sunib/voter/commit/cedefe1688d7164546ef92bb2bcefd815c5eaa4f))
* build info says what is mine and what it stands on ([b679913](https://github.com/sunib/voter/commit/b679913bc436d573711aa7d4891ed7a14ca70f03))
* build info under "Not today", with links to the code it runs on ([3f1088a](https://github.com/sunib/voter/commit/3f1088a4d92419d1c63ce34780f0f0ad29c6a722))
* Seat, Coffee and Room as tabs; quizzes and databases under "Not today" ([f2af671](https://github.com/sunib/voter/commit/f2af671fccc59e243e15b84f37418252fe5a2986))
* take your seat once, then change its colour and emoji, with a why ([954d985](https://github.com/sunib/voter/commit/954d985fb97df6b79ca0b16eb907ef9f84abd34a))


### Bug Fixes

* plainer words on /me, each said once ([5fc89d2](https://github.com/sunib/voter/commit/5fc89d2c2aa27d990dd812b6b5dee272bd288c04))
* say "answer required" and "whole cents", never "invalid ..." ([#39](https://github.com/sunib/voter/issues/39)) ([6ac4115](https://github.com/sunib/voter/commit/6ac4115d4ea94b6fd7ffe1bccebda51f444ce90b))
* say a seat was taken under the map, and the why's error in red ([ac9b3c2](https://github.com/sunib/voter/commit/ac9b3c24089bccd49c5f2e89e8dbb8e9417603f1))

## [2.1.2](https://github.com/sunib/voter/compare/v2.1.1...v2.1.2) (2026-10-07)


### Bug Fixes

* merge concurrent voucher edits per voucher ([986e0f3](https://github.com/sunib/voter/commit/986e0f3139fcb55041b58a966683b210a85005c3))

## [2.1.1](https://github.com/sunib/voter/compare/v2.1.0...v2.1.1) (2026-10-06)


### Bug Fixes

* merge concurrent price edits per product, not per menu ([0251445](https://github.com/sunib/voter/commit/0251445cfec35391a6b7a6b21969bcccc160632b))


### Documentation

* the production plan as it was built ([77b9990](https://github.com/sunib/voter/commit/77b9990ae9d5602605e53184ea6f1ec1f0c848f5))

## [2.1.0](https://github.com/sunib/voter/compare/v2.0.1...v2.1.0) (2026-10-06)


### Features

* a shop-only mode, the coffee bar as production runs it ([3034fdd](https://github.com/sunib/voter/commit/3034fdd81e85e3a8775f3ff137c328a2f3e5660e))
* open voting group by group, and hand the coffee menu to one person ([#33](https://github.com/sunib/voter/issues/33)) ([340089a](https://github.com/sunib/voter/commit/340089af56ecc3db7db3b6df04645d2d6bb28d40))


### Documentation

* plan the coffee-to-production ending of the 2026-10-07 demo ([1d9d6da](https://github.com/sunib/voter/commit/1d9d6da7698176c6ca366fbad53c6fae0cdb4cb9))

## [2.0.1](https://github.com/sunib/voter/compare/v2.0.0...v2.0.1) (2026-10-06)


### Documentation

* plan the 2026-10-07 demo, and the delete-poisons-author bug ([#30](https://github.com/sunib/voter/issues/30)) ([581d449](https://github.com/sunib/voter/commit/581d449212e37cf3a6e9e26bc7a9cf1bde2ccba5))
* the system as it runs after the krm-foyer cutover ([#29](https://github.com/sunib/voter/issues/29)) ([fcee30e](https://github.com/sunib/voter/commit/fcee30ea9e193e3f07ecc22620d150d90d4251f6))

## [2.0.0](https://github.com/sunib/voter/compare/v1.3.1...v2.0.0) (2026-10-06)


### ⚠ BREAKING CHANGES

* Voter no longer serves /auth/* or reads OIDC_*, APP_ORIGIN, APP_COOKIE_* or the session cookie settings, and /public/ must be routed through krm-foyer's identity check.
* /public/rounds, /public/rounds/{name} and /public/rounds/{name}/results are gone, and a ballot from anyone but a Room Pass participant must declare itself.
* /public/coffeeconfig, /public/databases, /public/audience/coffee-admin and /public/rounds/{name}/state are gone.
* Voter no longer serves /public/stream or /metrics, and its frontend needs krm-foyer on the same host. Its /public/* endpoints still expect Voter's own session until steps 3 to 5, so voting, the storefront and saving do not work yet, and the browser suite is red until step 6.

### Features

* run Voter's domain backend behind krm-foyer's identity check ([572b990](https://github.com/sunib/voter/commit/572b99077a7a85e48c4a0520c9b6eec04f7669d9))
* serve the deployment's settings at /config.json, not in the session ([dccd70d](https://github.com/sunib/voter/commit/dccd70df6e06483744b71b6e93ca691ef93a6ad8))
* speak krm-foyer's login, session and stream contract ([79c3b0b](https://github.com/sunib/voter/commit/79c3b0bcb4a1cca1688dde6e9979c960d288306d))
* votes are the participant's own QuizSubmission, held by admission ([744e259](https://github.com/sunib/voter/commit/744e2590f3ae24b469d7e980f462986fb7302957))
* write CoffeeConfigs, Databases, rounds and the grant through /k8s ([ffe5e26](https://github.com/sunib/voter/commit/ffe5e266693bb534d739dd3c6ecf9020fbc99d75))


### Bug Fixes

* wait for a new round's questions digest before casting a ballot ([1c5dc7b](https://github.com/sunib/voter/commit/1c5dc7b6d0e91cb8aeade29364fc29ce73979077))


### Documentation

* record step 2 and what the migration found along the way ([c316c1e](https://github.com/sunib/voter/commit/c316c1ee9deeb98d90667de20956728167e3de7e))

## [1.3.1](https://github.com/sunib/voter/compare/v1.3.0...v1.3.1) (2026-10-06)


### Documentation

* Kubernetes as your backend, one vote through the API server ([#23](https://github.com/sunib/voter/issues/23)) ([953e5b7](https://github.com/sunib/voter/commit/953e5b7e1ca59a78b25869dca6e14fda483bb890))

## [1.3.0](https://github.com/sunib/voter/compare/v1.2.0...v1.3.0) (2026-10-06)


### Features

* pin each ballot to its round's uid and questions ([#21](https://github.com/sunib/voter/issues/21)) ([be6a268](https://github.com/sunib/voter/commit/be6a268ee83862c09f1b6ae468390d071776acd6))


### Documentation

* plan the krm-foyer migration; run Room Pass 2.1.0 in e2e ([#20](https://github.com/sunib/voter/issues/20)) ([c0498ca](https://github.com/sunib/voter/commit/c0498ca8e573fbd7c5115397e9728cc55a33b49a))
* record the 2026-09-17 demo in numbers and the Room Pass 2.0.0 field report ([51dbb1f](https://github.com/sunib/voter/commit/51dbb1f0928794bdd83339b3794bbba1755b2df2))

## [1.2.0](https://github.com/sunib/voter/compare/v1.1.0...v1.2.0) (2026-10-01)


### Features

* run against Room Pass 2.x, which now lives in sunib/room-pass ([e467f8e](https://github.com/sunib/voter/commit/e467f8e753cba20efa44cb338869b4a65af8f0f2))


### Documentation

* item 3 shipped in 0.48.0, and what is left of it ([b2320c5](https://github.com/sunib/voter/commit/b2320c59ac389656126feecb2a39495a0a37ff94))
* move the k8s-front design to its own repository, krm-foyer ([05146e5](https://github.com/sunib/voter/commit/05146e55fe2fc5ab948d5343c54acb05c5ec1f39))
* where a save's four to seven seconds actually go ([7b3166a](https://github.com/sunib/voter/commit/7b3166a8c20562e66c889b681c4026587acc0063))

## [1.1.0](https://github.com/sunib/voter/compare/v1.0.0...v1.1.0) (2026-09-22)


### Features

* **editor:** a save reports the commit it became, and links to it ([e92ab0e](https://github.com/sunib/voter/commit/e92ab0eb1fad005b629d94aa8b01ca513f1d9ea9))
* **editor:** follow the commit instead of quoting the receipt ([a6f3d18](https://github.com/sunib/voter/commit/a6f3d189c671b93d85bbe5f58a7fcb1190aca573))


### Documentation

* pull external/k8s first -- it now has a writer that is not a person ([939bbb1](https://github.com/sunib/voter/commit/939bbb143c808406ccd873be45fe4c050568a915))
