# auth-service

Traefik ForwardAuth service for the voter demo. Validates join codes, manages encrypted session cookies, and mints short-lived Kubernetes tokens so the browser can talk to the Kubernetes API without ever seeing a token.

## Endpoints

| Path | Description |
|------|-------------|
| `GET /healthz` | Health check |
| `GET /public/session-info` | Returns current session metadata (namespace/name/state/title) |
| `GET /public/kubeconfig` | Returns a ready-to-use kubeconfig for `kubectl` access |
| `POST /private/forward-auth-decision` | Traefik ForwardAuth endpoint — validates session, injects token |

All `/public/` endpoints accept either a `?code=XXXX` join code or an existing session cookie.

## Audience kubectl access

Once you have the join code on screen, the audience can get `kubectl` access with a single command:

```sh
# One-liner — no file written
KUBECONFIG=<(curl -s "https://vote.reversegitops.dev/auth/kubeconfig?code=XXXX") \
  kubectl get quizsessions.examples.configbutler.ai

# Multi-command version (more reliable on macOS)
curl -s "https://vote.reversegitops.dev/auth/kubeconfig?code=XXXX" > /tmp/voter.yaml
export KUBECONFIG=/tmp/voter.yaml
kubectl get quizsessions.examples.configbutler.ai
kubectl get quizsubmissions.examples.configbutler.ai
```

The returned kubeconfig uses the same `quiz-access` ServiceAccount as the browser and contains a token valid for ~10 minutes.

## Run locally

```bash
cd auth-service
FORWARD_SA=quiz-access FORWARD_SA_NAMESPACE=voter COOKIE_SECURE=false go run .
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
| `COOKIE_NAME` | `device_session` | Device session cookie name |
| `COOKIE_SECURE` | `false` | Set `Secure` flag on cookies (use `true` in production) |
| `COOKIE_MAX_AGE_SECONDS` | `3600` | Device session cookie lifetime |
| `SESSION_COOKIE_NAME` | `auth_session` | Session reference cookie name |
| `SESSION_COOKIE_MAX_AGE_SECONDS` | `3600` | Session cookie lifetime |
| `JOIN_CODE_ROTATE_SECONDS` | `15s` | How often join codes rotate |
| `JOIN_CODE_TTL_SECONDS` | `60s` | How long an old code stays valid after rotation |
| `JOIN_CODE_LENGTH` | `4` | Join code character length |

## How it works

### Join flow

1. Browser visits `/join?code=XXXX`
2. Traefik's ForwardAuth calls `/private/forward-auth-decision` with `X-Forwarded-Uri` containing the code
3. auth-service validates the code, resolves it to a QuizSession, and sets a signed/encrypted session cookie
4. Subsequent requests use the cookie — no code needed

### Token strategy

- Tokens are minted via the Kubernetes TokenRequest API against the `FORWARD_SA` ServiceAccount
- TTL is 10 minutes (Kubernetes minimum)
- A shared token is cached for browser requests; the kubeconfig endpoint mints a fresh token per download
- Tokens are injected by Traefik into upstream requests — they never reach the browser

### Session cookie keys

On startup, auth-service looks for the Kubernetes Secret `auth-session-cookie-keys` in its namespace. If missing, it generates random `hashKey`/`blockKey` values, creates the Secret, and uses them for `gorilla/securecookie` signing and encryption. This means cookie keys survive pod restarts.
