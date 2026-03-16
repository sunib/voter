# auth-service (ForwardAuth prototype)

This is a minimal **Traefik ForwardAuth** helper.

Prototype behavior:

- Always returns `200`.
- Always returns `Authorization: Bearer <STATIC_BEARER_TOKEN>` so Traefik can inject it upstream.
- If the request does not have a `device_session` cookie, sets one (you must configure Traefik to forward auth cookies back to the browser).
- Supports join-code-only auth via `X-Join-Code`, resolving the session and persisting it in a signed/encrypted cookie.
- Logs rolling join codes for live sessions every `JOIN_CODE_ROTATE_SECONDS`.
- Exposes `/public/session-info` to return the current session metadata for the session cookie.

## Run locally

```bash
cd auth-service
STATIC_BEARER_TOKEN=demo-token COOKIE_SECURE=false go run ./main.go
```

Health:

```bash
curl -i http://localhost:8080/healthz
```

ForwardAuth endpoint:

```bash
curl -i http://localhost:8080/private/forward-auth-decision
```

## Environment variables

- `HOST` (default `0.0.0.0`)
- `PORT` (default `8080`)
- `STATIC_BEARER_TOKEN` (default `demo-token`)
- `COOKIE_NAME` (default `device_session`)
- `COOKIE_SECURE` (default `false`)
- `COOKIE_MAX_AGE_SECONDS` (default `3600`)
- `SESSION_COOKIE_NAME` (default `auth_session`)
- `SESSION_COOKIE_MAX_AGE_SECONDS` (default `3600`)
- `JOIN_CODE_ROTATE_SECONDS` (default `15s`)
- `JOIN_CODE_TTL_SECONDS` (default `60s`)
- `JOIN_CODE_LENGTH` (default `4`)
- `FORWARD_SA` (required)

## Traefik (Kubernetes CRD) example

The key bits are:

- `authResponseHeaders: ["Authorization"]` so Traefik copies the header from the auth response into the upstream request.
- `addAuthCookiesToResponse: ["device_session", "auth_session"]` so the cookies set by this service reach the browser.

See [`auth-service/k8s/traefik-forwardauth-middleware.yaml`](auth-service/k8s/traefik-forwardauth-middleware.yaml:1).

## Join flow

- Client sends `X-Join-Code` without a session name.
- auth-service resolves the live session and writes a signed/encrypted session cookie.
- Subsequent requests use the session cookie (no join code needed).

## Session cookie keys

On startup, auth-service checks for the Kubernetes Secret `auth-session-cookie-keys` in the same namespace. If missing, it generates strong random `hashKey` and `blockKey`, creates the Secret, and uses those keys for `securecookie` signing/encryption.

## Session info endpoint

`GET /public/session-info` returns the resolved session metadata (namespace/name/state/title) for the current session cookie, and rejects non-live sessions.
