# voter

The application: the coffee and quiz APIs, the Dex OIDC client that owns the
browser session, and — in the container image — the server for the built
frontend. One image, one origin, one deployable.

This directory was called `auth-service`, which described neither what it does
nor what it owns. It is not an identity provider: Dex issues tokens and Room
Pass decides who a participant is. See
[room-pass/login-explained.md](../room-pass/login-explained.md) for how the
three fit together.

Two authentication models live here, and they are mutually exclusive:

- **`OIDC_ENABLED=true`** — the intended one. The backend is a confidential OIDC
  client; each participant's own ID token is the credential used against the
  Kubernetes API.
- **`OIDC_ENABLED` unset** — legacy. The browser asserts its own identity and
  the backend impersonates with its ServiceAccount. It is on its way out and
  must not be exposed publicly. Everything below describes this path.

## Serving the frontend

The image builds `frontend/` and copies the bundle to `/srv/www`, which
`STATIC_DIR` points at. Requests under `/auth/`, `/public/`, `/private/`,
`/apis/` and `/healthz` are API paths and 404 when unmatched; everything else
falls back to `index.html` for vue-router. Leave `STATIC_DIR` empty in
development — Vite serves the frontend on :5173 and proxies the API here.

Because the Dockerfile builds from both `frontend/` and `voter/`, its build
context is the repository root:

```bash
task image-voter                     # or: docker buildx build -f voter/Dockerfile .
```

## How it works

### Join flow

1. Browser visits `/join?code=XXXX`
2. Traefik's ForwardAuth calls `/private/forward-auth-decision` with `X-Forwarded-Uri` containing the code
3. voter validates the code, resolves it to a QuizSession, and sets a signed/encrypted session cookie
4. Subsequent requests use the cookie — no code needed

### QuizSession example

For a live session, voter rotates a join code and patches it into `status.joinCode`:

```yaml
apiVersion: examples.configbutler.ai/v1alpha1
kind: QuizSession
metadata:
  name: kubecon-2026
  namespace: voter
spec:
  title: KubeCon 2026
  state: live
  questions:
    - id: q1
      type: singleChoice
      title: Was the talk useful?
      required: true
      choices:
        - "Yes"
        - "Somewhat"
        - "No"
    - id: q2
      type: freeText
      title: One thing to improve
      placeholder: Short and specific...
status:
  joinCode: 6b1d  # New random join code is pushed every 30 seconds
```

### Token strategy

- Tokens are minted via the Kubernetes TokenRequest API against the `FORWARD_SA` ServiceAccount (impersonator-only RBAC)
- TTL is 10 minutes (Kubernetes minimum), shared across browser sessions and cached
- Traefik injects the token plus server-derived `Impersonate-User` / `Impersonate-Group` / extras into upstream requests — the audit identity is the impersonated audience member, not the SA
- Tokens never reach the browser
- Any inbound `Authorization` on `/private/forward-auth-decision` is rejected with 401 before session lookup — there is no client-bearer path

### Session lookup strategy

- `QuizSession` lookups for `/public/session-info` are coalesced with `singleflight` and cached for 5 seconds to avoid hammering the Kubernetes API during login spikes

### Session cookie keys

On startup, voter looks for the Kubernetes Secret `auth-session-cookie-keys` in its namespace. If missing, it generates random `hashKey`/`blockKey` values, creates the Secret, and uses them for `gorilla/securecookie` signing and encryption. This means cookie keys survive pod restarts.

## Endpoints

| Path | Description |
|------|-------------|
| `GET /healthz` | Health check |
| `GET /public/session-info` | Returns current session metadata (namespace/name/state/title) |
| `GET` or `POST /private/forward-auth-decision` | Traefik ForwardAuth endpoint — browser-only, rejects inbound `Authorization` |

All `/public/` endpoints accept either a `?code=XXXX` join code or an existing session cookie.

## Audience access

Audience members reach the demo through the browser only. The Kubernetes API surface (`/api`, `/apis`, `/openapi`) is fronted by Traefik, which strips any client-supplied `Authorization` / `Impersonate-*` headers, calls voter to mint a short-lived impersonator-SA token, and attaches the audience member's identity as `Impersonate-User` / `Impersonate-Group`. There is no downloadable kubeconfig and no `kubectl` path.

## Run locally

Local runs need Kubernetes access via `KUBECONFIG` or `~/.kube/config` because the service falls back to kubeconfig when it is not running in-cluster.

```bash
cd voter
FORWARD_SA=voter-audience-impersonator FORWARD_SA_NAMESPACE=voter COOKIE_SECURE=false go run .
```

Health check:

```bash
curl -i http://localhost:8080/healthz
```

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HOST` | `0.0.0.0` | Listen address |
| `PORT` | `8080` | Listen port |
| `FORWARD_SA` | *(required)* | ServiceAccount name to mint tokens for |
| `FORWARD_SA_NAMESPACE` | *(required)* | Namespace of that ServiceAccount |
| `COOKIE_SECURE` | `false` | Set `Secure` flag on cookies (use `true` in production) |
| `SESSION_COOKIE_NAME` | `auth_session` | Session reference cookie name |
| `SESSION_COOKIE_MAX_AGE_SECONDS` | `3600` | Session cookie lifetime |
| `JOIN_CODE_ROTATE_SECONDS` | `15s` | How often join codes rotate |
| `JOIN_CODE_TTL_SECONDS` | `60s` | How long an old code stays valid after rotation |
| `JOIN_CODE_LENGTH` | `4` | Join code character length |
