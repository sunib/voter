# Plan: Auth Service as OIDC Issuer

## The idea in one sentence

Instead of minting Kubernetes ServiceAccount tokens, the auth service signs its own JWTs and exposes a JWKS endpoint — making it a proper OIDC issuer that K8s trusts natively. Every audience member gets a token with their real email in it, visible in the audit trail.

---

## Why this is interesting for the talk

The current kubeconfig flow already works: audience members can run `kubectl` and see their own submissions. But all traffic looks identical in K8s — every request authenticates as `system:serviceaccount:vote:quiz-access`. There is no way to tell who did what.

With the auth service as an OIDC issuer:

```
kubectl get quizsubmissions   →   audit log: oidc:alice@example.com
kubectl create quizsubmission →   audit log: oidc:bob@example.com
```

This is a significant narrative moment for the talk: the same join code that lets your phone vote also gives your terminal a real, named Kubernetes identity. No extra tools. No separate IdP to deploy. The auth service is already the trust anchor — this just makes that explicit in a standards-compliant way.

---

## Architecture

```
                        auth service
                       ┌────────────────────────────────────────┐
 Browser / terminal    │                                        │
        │              │  /auth/kubeconfig?code=XXXX            │
        │              │  &email=alice@example.com              │
        ├─────────────►│                                        │
        │              │  1. validate join code                 │
        │              │  2. sign JWT with ECDSA private key    │
        │◄─────────────│     {iss, sub, email, groups, exp}     │
        │  kubeconfig  │                                        │
        │  with JWT    │  /.well-known/openid-configuration     │
        │              │  /jwks.json  (public key)              │
        │              └────────────────────────────────────────┘
        │
        │  kubectl get quizsessions
        │  Authorization: Bearer <JWT>
        ├────────────────────────────────────────► K8s API server
        │                                               │
        │                                               │ (first time / cache miss)
        │                              ◄────────────────┤ GET /auth/jwks.json
        │                              ─────────────────►
        │                                               │ verify JWT signature
        │                                               │ extract email claim
        │                                               │ check RBAC for group
        │◄──────────────────────────────────────────────┤
        │  quizsessions list                            │
```

K8s fetches the JWKS once and caches it. After that, all token validation is local — no runtime dependency on the auth service for every request.

---

## What the JWT looks like

```json
{
  "iss": "https://vote.reversegitops.dev/auth",
  "sub": "alice@example.com",
  "email": "alice@example.com",
  "name": "Alice",
  "groups": ["audience:voter"],
  "exp": 1773745256,
  "iat": 1773744656,
  "jti": "d4e8f2a1-..."
}
```

K8s reads `email` as the username and `groups` for RBAC. The `name` claim is extra — not used by K8s but visible in audit events via the extra attributes.

---

## What needs to be built

### 1. Signing key management

Same pattern as session cookie keys: generate an ECDSA P-256 key pair at startup, store in a K8s Secret (`auth-oidc-signing-key`). Load on startup; rotate by replacing the Secret and restarting.

```go
// New fields on handlerDeps
type handlerDeps struct {
    ...
    oidcIssuerURL string
    oidcSigner    *oidcSigner  // holds private key, issues + signs JWTs
}
```

### 2. OIDC discovery endpoints

Two new unauthenticated endpoints (no `requireSessionMiddleware`):

**`GET /.well-known/openid-configuration`**
```json
{
  "issuer": "https://vote.reversegitops.dev/auth",
  "jwks_uri": "https://vote.reversegitops.dev/auth/jwks.json",
  "response_types_supported": ["id_token"],
  "subject_types_supported": ["public"],
  "id_token_signing_alg_values_supported": ["ES256"]
}
```

**`GET /jwks.json`**
```json
{
  "keys": [{
    "kty": "EC",
    "crv": "P-256",
    "kid": "2026-03-17",
    "use": "sig",
    "x": "...",
    "y": "..."
  }]
}
```

Both should be served at `/public/` paths so Traefik's `/auth` route exposes them at `https://vote.reversegitops.dev/auth/jwks.json` etc.

> **Note:** K8s fetches the OIDC discovery doc directly, so the issuer URL's `/.well-known/openid-configuration` must be reachable from inside the cluster too. The easiest way: expose it through the same Traefik ingress.

### 3. JWT issuance in `/public/kubeconfig`

Add an optional `?email=` and `?name=` query parameter to the kubeconfig endpoint. When present, sign an OIDC JWT instead of requesting a K8s ServiceAccount token:

```
GET /auth/kubeconfig?code=XXXX&email=alice@example.com&name=Alice
```

If `email` is absent, fall back to the current ServiceAccount token behaviour (so the endpoint stays backward-compatible and works without the user providing any identity).

The kubeconfig template gets a new `id-token` field instead of `token`:

```yaml
users:
- name: audience
  user:
    token: eyJ...   # the signed JWT
```

### 4. RBAC — swap ServiceAccount binding for group binding

Add a new RoleBinding alongside the existing one (keep the SA binding for backward compatibility during transition):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: quiz-access-oidc
  namespace: vote
subjects:
- kind: Group
  name: "audience:voter"
  apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: Role
  name: quiz-access
  apiGroup: rbac.authorization.k8s.io
```

### 5. K8s API server configuration

One-time change at cluster bootstrap. For k3s, add to `/etc/rancher/k3s/config.yaml`:

```yaml
kube-apiserver-arg:
  - "oidc-issuer-url=https://vote.reversegitops.dev/auth"
  - "oidc-client-id=kubectl"
  - "oidc-username-claim=email"
  - "oidc-username-prefix=oidc:"
  - "oidc-groups-claim=groups"
```

Then restart k3s. This cannot be changed at runtime, which is the main operational constraint (see cons below).

### 6. Token validation in `forward-auth-decision`

The bearer passthrough path currently calls `TokenReview`, which only works for K8s ServiceAccount tokens. OIDC JWTs are validated directly by K8s — the API server doesn't go through TokenReview for them. So the passthrough logic stays the same; it's K8s that now handles both token types natively.

---

## The audience experience

### With email (full identity):
```sh
export KUBECONFIG=<(curl -s \
  "https://vote.reversegitops.dev/auth/kubeconfig?code=AB3X&email=alice@example.com")

kubectl get quizsubmissions.examples.configbutler.ai
# NAME                    AGE
# kubecon-2026-<hash>     2m
```

### What the presenter sees in the audit log:
```sh
kubectl get events  # or stream the audit log during the talk
```
```json
{"user": {"username": "oidc:alice@example.com", "groups": ["audience:voter"]},
 "verb": "create", "resource": "quizsubmissions", ...}
{"user": {"username": "oidc:bob@gmail.com", "groups": ["audience:voter"]},
 "verb": "get", "resource": "quizsessions", ...}
```

### Without email (backward compatible):
```sh
export KUBECONFIG=<(curl -s \
  "https://vote.reversegitops.dev/auth/kubeconfig?code=AB3X")
# Returns SA token as before, authenticates as system:serviceaccount:vote:quiz-access
```

---

## Pros

- **Real identity in audit logs** — `oidc:alice@example.com` not `system:serviceaccount:vote:quiz-access`. This is the core win and the best demo moment.
- **No extra tooling for the audience** — the kubeconfig contains a static JWT; `kubectl` sends it as a plain `Authorization: Bearer` header. No `kubelogin`, no credential plugin, no browser OAuth flow.
- **No Dex or external IdP** — the auth service is already the trust anchor for the whole system. Making it an OIDC issuer is a natural extension, not a new component.
- **Standards-compliant** — the JWT is a real OIDC token. Any tool that understands OIDC (audit dashboards, Falco, OPA/Gatekeeper policies on user identity) works with it out of the box.
- **Self-declared identity is fine for this use case** — email is not verified, but that's OK. The join code proves the person is in the room; the email is just a display name for the audit trail. Nobody is authorising a production deployment based on this.
- **Key management already solved** — the session cookie key pattern (generate at startup, store in K8s Secret, load on restart) applies directly.
- **Group-based RBAC scales** — one RoleBinding covers every audience member regardless of email address.
- **Token expiry** — JWTs have `exp` baked in; no need to call K8s TokenRequest or worry about the 10-minute minimum TTL.

## Cons

- **API server flags must be set at cluster bootstrap** — `--oidc-issuer-url` and friends cannot be changed at runtime. On a managed cluster (EKS, GKE, AKS) this may require a cluster-level feature flag or may not be possible at all. For the dedicated demo cluster this is fine.
- **K8s must reach the JWKS endpoint** — the API server fetches `/.well-known/openid-configuration` from inside the cluster. The auth service must be reachable from the control plane, not just from the ingress. This is already true (auth service is a ClusterIP service), but the OIDC discovery URL must be publicly accessible so K8s can resolve it.
- **No TokenReview for OIDC tokens** — the current bearer passthrough in `forward-auth-decision` uses `TokenReview`, which doesn't work for OIDC JWTs. For the OIDC path, validation is handled by K8s directly; the auth service's passthrough needs to detect OIDC JWTs (check the `iss` claim) and skip the TokenReview call.
- **Self-declared identity** — anyone can claim to be `cto@bigcompany.com`. Acceptable for a conference demo; not acceptable for anything real. Worth calling out explicitly in the talk as a deliberate trade-off.
- **JWT library dependency** — need to add a JWT signing library (e.g. `golang-jwt/jwt`) and JWK serialisation. Small but non-zero addition to the trusted computing base.
- **Key rotation is manual** — rotating the signing key invalidates all outstanding tokens. For a 45-minute talk with 10-minute token TTLs this is fine; for a longer event it needs a proper rotation strategy.

---

## Open questions

1. **Should the kubeconfig endpoint collect email in the URL or prompt for it?** A `?email=` query param is simplest for the demo. A future version could add a small form in the browser UI that then deep-links to a `curl` command with the email pre-filled.
2. **Should the JWT TTL match the current SA token TTL (10 min), or be longer?** Longer (e.g. 1h) is friendlier for the audience since there's no `kubelogin` refresh. The talk is ~45 min so 1h covers it.
3. **How to handle the `forward-auth-decision` passthrough for OIDC tokens?** The simplest approach: decode the JWT header (no signature check needed here — K8s already verified it), check if `iss` matches the configured issuer URL, and if so skip `TokenReview` and pass through. If `iss` doesn't match, try `TokenReview` as before (handles SA tokens).
