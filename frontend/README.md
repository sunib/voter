# Voter frontend: Vue 3 + TypeScript + Vite

The single-page app Voter serves. Since 2.0.0 (2026-10-06) it talks to two
backends on the same origin, and holds no credential of its own:

- **krm-foyer** for everything about the person: `/auth/session` (display name,
  groups, connector, CSRF token), `/auth/whoami`, login links built by `loginURL`
  in [`src/api/session.ts`](src/api/session.ts), `POST /auth/logout`, reads and
  writes to Kubernetes through `/k8s` ([`src/api/kube.ts`](src/api/kube.ts)), and
  live streams over `/stream/v1` with `@configbutler/krm-stream` 0.10.0.
- **Voter** for the files, `/config.json` (namespace and object names, loaded
  before the app mounts) and the `/public` endpoints of the coffee story.

There is no Voter session cookie. The session cookie is krm-foyer's, `HttpOnly`,
and page scripts never see a token.

## Running it

```bash
npm ci
npm run build    # type-check and build into dist/
npm test         # unit tests (vitest)
```

The real way to see it work is the fixture, which serves this bundle inside the
Voter image behind krm-foyer: `task e2e-up` at the repository root, then
https://app.voter.test:19443 (see [test/browser](../test/browser/README.md)).
A Vite loop against the fixture does not exist yet; it is an open item.

### The two dev modes in `vite.config.ts`, and why they no longer work

Both predate krm-foyer and are left in place, not maintained:

- `npm run dev` proxies `/public`, `/auth` and `/apis` to
  `https://demo.configbutler.ai` over self-signed HTTPS. The app now also needs
  `/config.json`, `/k8s` and `/stream`, which are not proxied, and krm-foyer's
  session cookie is set for the production host, not for Vite's origin. The page
  will not get past loading its configuration.
- `npm run dev:mock` serves `dev/kube-mock-plugin.ts`: a mock of the old
  `/apis/...` paths with an `X-Join-Code` header. Nothing in the current app calls
  those, and there is no mock of `/auth/session` or `/config.json`.

## Image

One image: the frontend bundle is built into the Voter image. The build context is
the repository root, not this directory:

```bash
cd .. && docker build -f voter/Dockerfile .
```

CI publishes it as `ghcr.io/sunib/voter`; the cluster gets it through Flux, never
`kubectl apply` ([docs/ci.md](../docs/ci.md)).

## Join flow

- The QR code points at Voter's `/join-room?code=...`, which hands the code to Room
  Pass in a cookie and sends the browser to krm-foyer's `/auth/login` with the
  `room-pass` connector.
- Room Pass's join page (`/join`, not a route in this app) takes a display name;
  Dex issues the token, and krm-foyer sets its session cookie.
- The app then reads `/auth/session`. Signing out posts krm-foyer's
  `/auth/logout`; a Room Pass participant is then sent to `/join`, where Room
  Pass's own form ends the enrolment.
