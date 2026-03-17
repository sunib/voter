# Audience kubectl Access

## What we built

The audience can get a working `kubectl` session using the same join code shown on screen for the voting app.

**The one-liner:**
```sh
export KUBECONFIG=<(curl -s "https://vote.reversegitops.dev/auth/kubeconfig?code=XXXX")
kubectl get quizsessions.examples.configbutler.ai
kubectl get quizsubmissions.examples.configbutler.ai
```

Or for multiple commands on macOS (where process substitution with `export` can be flaky):
```sh
curl -s "https://vote.reversegitops.dev/auth/kubeconfig?code=XXXX" > /tmp/voter.yaml
export KUBECONFIG=/tmp/voter.yaml
kubectl get quizsessions.examples.configbutler.ai
```

The session cookie also works — if they've already joined in the browser:
```sh
curl -s "https://vote.reversegitops.dev/auth/kubeconfig" --cookie "auth_session=..." > /tmp/voter.yaml
```

---

## How it works

### `/public/kubeconfig` endpoint

`GET /auth/kubeconfig?code=XXXX` (or with a session cookie) goes through `requireSessionMiddleware` — the same validation path as every other `/public/` endpoint. On success it mints a short-lived token (~10 min) via the `quiz-access` ServiceAccount and returns a complete kubeconfig YAML.

The returned kubeconfig points at `https://vote.reversegitops.dev`, which Traefik proxies to the Kubernetes API server.

### Ingress

All Kubernetes API paths are exposed through Traefik behind the `auth-forwarder` middleware:

```
PathPrefix(/api) || PathPrefix(/apis) || PathPrefix(/openapi)
```

This covers kubectl's discovery calls (`/api`, `/apis`) as well as the actual resource paths.

### auth-forwarder: two paths

The `forward-auth-decision` handler handles both browser and kubectl traffic:

- **Browser** (no bearer token): validates session cookie / join code, mints a token, injects it.
- **kubectl** (bearer token present): calls the Kubernetes **TokenReview API** to validate the token, then passes it straight through. Invalid tokens are rejected with 401 before reaching K8s.

The auth-service RBAC includes `create` on `tokenreviews.authentication.k8s.io` for this.

---

## User identity in Kubernetes

Right now all audience traffic authenticates as `system:serviceaccount:vote:quiz-access` — every submission looks the same in audit logs. There are a few Kubernetes-native ways to attach a human identity.

### Option I — Kubernetes Impersonation headers

The auth service already sits between the browser and K8s with the ability to inject headers. Kubernetes supports impersonation headers:

```
Impersonate-User: alice@example.com
Impersonate-Extra-displayname: Alice
```

If the audience provides a name/email at join time (stored in the encrypted session cookie), the auth service can inject these headers for every forwarded request. K8s audit logs would then record both the auth-service SA and the impersonated identity.

**What's needed:**
- Join flow: collect optional display name / email (could be a step after the QR code scan)
- Store in session cookie (already encrypted/signed)
- Auth service RBAC: add `impersonate` verb on `users` and `userextras` to the ClusterRole
- Inject `Impersonate-User` / `Impersonate-Extra-*` headers in `forward-auth-decision` before forwarding

**Kubernetes audit log would show:**
```json
"user": {
  "username": "system:serviceaccount:vote:auth-service",
  "impersonatedUser": {
    "username": "alice@example.com",
    "extra": { "displayname": ["Alice"] }
  }
}
```

**Limitation for kubectl users:** the kubeconfig token bypasses the auth service session, so no impersonation headers are injected for kubectl — it shows as `quiz-access` SA. To fix this, the kubeconfig endpoint could embed the display name claim in the audience field of the TokenRequest, but K8s doesn't expose custom claims in TokenReview responses, so it would need a separate side-channel (e.g. store name→token mapping in the auth service).

### Option II — Per-user ServiceAccounts

Create a short-lived ServiceAccount per audience member at join time (named after their email or a slug). Mint a token for that SA. Delete the SA at the end of the talk.

This gives each person their own identity in K8s audit logs without impersonation, and you can revoke individuals by deleting their SA.

**What's needed:**
- Auth service RBAC: `create`/`delete` on `serviceaccounts` and `serviceaccounts/token`
- RBAC binding for each new SA to the `quiz-access` Role
- Cleanup job / TTL controller to remove SAs after the session closes

**Trade-off:** more K8s object churn; requires cluster-admin-level RBAC on the auth service to create RoleBindings.

### Option III — OIDC with audience-specific tokens

With OIDC, **no ServiceAccounts are needed at all**. The identity lives entirely in the JWT token itself — K8s extracts the username directly from a claim (typically `email`). RBAC then binds to those usernames or to a group claim. This is the cleanest model: every audience member is a first-class Kubernetes user.

#### How Dex fits in

[Dex](https://dexidp.io) is an OIDC identity provider that sits in front of upstream IdPs (GitHub, Google, LDAP, etc.) or can issue tokens from its own connectors. K8s is configured to trust Dex as its OIDC issuer; Dex is configured to trust whatever the audience uses to prove identity.

```
Audience browser/terminal
        │
        ▼
   Dex (OIDC IdP)  ←── connector ──► GitHub / Google / join-code flow
        │
        │  issues signed JWT with email, name, groups claims
        ▼
   kubectl / browser
        │
        │  Authorization: Bearer <JWT>
        ▼
   Traefik → Kubernetes API server
               └── validates JWT against Dex JWKS endpoint
               └── extracts username from `email` claim
               └── RBAC checks Role/ClusterRole bindings
```

#### K8s API server configuration

```yaml
# kube-apiserver flags (or k3s config)
oidc-issuer-url: https://dex.vote.reversegitops.dev
oidc-client-id: kubectl
oidc-username-claim: email
oidc-groups-claim: groups
oidc-username-prefix: "oidc:"   # avoids collisions with SA names
```

#### RBAC — group binding instead of per-user

Rather than a RoleBinding per person, Dex adds a `groups` claim to every token it issues (e.g. `audience:voter`). One RoleBinding covers everyone:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: quiz-access-oidc
  namespace: vote
subjects:
- kind: Group
  name: "audience:voter"       # Dex group claim
  apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: Role
  name: quiz-access
  apiGroup: rbac.authorization.k8s.io
```

No per-user ServiceAccounts, no per-user RoleBindings. K8s audit logs show `oidc:alice@example.com` for every request.

#### The audience flow (device flow — most conference-friendly)

The OAuth 2.0 device flow (RFC 8628) is designed for situations where the user can't easily type a URL into the current terminal — perfect for a conference demo.

1. Audience installs [`kubelogin`](https://github.com/int128/kubelogin) (or `kubectl oidc-login`)
2. Dex is configured with a connector: GitHub OAuth, Google, or a custom one that accepts a join code + self-declared email
3. Their kubeconfig uses an exec credential plugin:

```yaml
users:
- name: audience
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: kubectl
      args:
      - oidc-login
      - get-token
      - --oidc-issuer-url=https://dex.vote.reversegitops.dev
      - --oidc-client-id=kubectl
      - --grant-type=device-code   # prints a short URL + code, no browser redirect needed
```

4. On first `kubectl get`, a device code is printed:
   ```
   Open https://dex.vote.reversegitops.dev/device and enter: ABCD-1234
   ```
5. Audience opens that URL, authenticates (e.g. "Continue with Google"), approves
6. Token is cached locally; subsequent `kubectl` calls are instant until expiry
7. K8s sees `oidc:alice@example.com` for every request from Alice

#### Can the auth service call Dex in the background to get the JWT?

Yes — but it leads somewhere more interesting than Dex.

The OAuth2 **Resource Owner Password Credentials (ROPC)** grant lets a trusted server call Dex's `/token` endpoint with a username + password on behalf of the user, without any browser redirect:

```
POST https://dex.vote.reversegitops.dev/token
grant_type=password
&username=alice@example.com   ← self-declared at join time
&password=<join-code>         ← proves they're in the room
&client_id=auth-service
&client_secret=...
```

Dex would call a custom connector to validate the join code (which calls back to the auth service), then issue a signed JWT with `email: alice@example.com` and `groups: ["audience:voter"]`. The auth service embeds that JWT in the kubeconfig and returns it — no `kubelogin` needed, kubectl just works.

**The circular dependency problem:** Dex → custom connector → auth service → join code store. Dex and the auth service are now tightly coupled. You're also relying on ROPC, which OAuth 2.1 deprecates and many providers are dropping.

#### Better: auth service as its own OIDC issuer

The insight is that K8s doesn't care *who* issues the JWT — it just needs to verify the signature against a JWKS endpoint. The auth service can *be* the OIDC issuer:

```
Browser/terminal                Auth service                  K8s API
      │                               │                           │
      │  GET /auth/kubeconfig         │                           │
      │    ?code=XXXX                 │                           │
      │  ──────────────────────────►  │                           │
      │                               │  validate join code       │
      │                               │  sign JWT with own key    │
      │  ◄──────────────────────────  │                           │
      │  kubeconfig with JWT          │                           │
      │                               │                           │
      │  kubectl get quizsessions     │                           │
      │  Authorization: Bearer <JWT>  │                           │
      │  ────────────────────────────────────────────────────►    │
      │                               │  GET /auth/jwks.json ◄──  │
      │                               │  ──────────────────────►  │
      │                               │  (cached after first req) │
      │                               │                           │
      │  ◄────────────────────────────────────────────────────    │
      │  quizsessions list            │                           │
```

**What the auth service needs to add:**

1. Generate an ECDSA key pair at startup, stored in a K8s Secret (same pattern as the session cookie keys)
2. Expose two new endpoints:
   - `GET /.well-known/openid-configuration` — OIDC discovery doc pointing at the JWKS URL
   - `GET /jwks.json` — the public key in JWK format for K8s to verify signatures
3. At `/auth/kubeconfig`, instead of a K8s ServiceAccount token, sign and return a JWT:

```json
{
  "iss": "https://vote.reversegitops.dev/auth",
  "sub": "alice@example.com",
  "email": "alice@example.com",
  "groups": ["audience:voter"],
  "exp": 1773745256,
  "iat": 1773744656
}
```

4. The kubeconfig carries this JWT as a static token — no plugin, no refresh dance.

**K8s API server configuration** (one-time, set at bootstrap):

```yaml
# k3s: /etc/rancher/k3s/config.yaml
kube-apiserver-arg:
  - "oidc-issuer-url=https://vote.reversegitops.dev/auth"
  - "oidc-client-id=kubectl"
  - "oidc-username-claim=email"
  - "oidc-username-prefix=oidc:"
  - "oidc-groups-claim=groups"
```

**RBAC** stays the same single group binding:

```yaml
subjects:
- kind: Group
  name: "audience:voter"
  apiGroup: rbac.authorization.k8s.io
```

**What this gives you at the talk:**

- `kubectl get quizsessions.examples.configbutler.ai` just works, no plugins
- K8s audit logs show `oidc:alice@example.com` directly
- No Dex, no external IdP, no `kubelogin`
- Token expiry and signing all handled by the auth service using the same patterns already in place (key storage in K8s Secrets)
- The join code + self-declared email flows seamlessly into a real OIDC credential

The main cost is that the kube-apiserver `--oidc-*` flags have to be set when the cluster is created (they can't be changed at runtime on managed clusters). For a dedicated demo cluster this is fine.

#### Comparison with Option I (impersonation)

| | Option I — Impersonation | Option III — OIDC/Dex |
|---|---|---|
| Identity source | Stored in session cookie, injected by auth service | In the JWT itself, validated by K8s |
| ServiceAccounts needed | One shared `quiz-access` SA | None |
| Audit log identity | Auth-service SA + impersonated user | `oidc:alice@example.com` directly |
| kubectl support | Works with any kubectl + our kubeconfig | Requires `kubelogin` or similar plugin |
| K8s API server changes | None | OIDC flags must be set at cluster bootstrap |
| Infrastructure | Nothing new | Dex deployment + possible custom connector |
| Token refresh | Auth service handles it | kubelogin handles it transparently |

**Trade-off:** OIDC gives cleaner identity (no impersonation indirection, identity is in the token itself) but requires configuring the API server at bootstrap time and asking the audience to install `kubelogin`. Impersonation is deployable to any existing cluster without touching the API server flags.

### Recommendation for the talk

**Option I (impersonation)** is the most interesting for the demo narrative — you can show the K8s audit log with real email addresses in it while the audience is voting, which is a powerful illustration of Kubernetes-as-a-platform. It only requires:
1. Adding an optional name/email step to the join flow
2. One extra RBAC rule (`impersonate`)
3. Three extra lines in `forward-auth-decision`

---

## Design options considered (archive)

The implementation above is Option C from the original design exploration. Options A, B, and D were also considered:

| Option | Approach | Why not chosen |
|--------|----------|---------------|
| **A** — Static shared token | Pre-mint a long-lived token, distribute as QR code | No per-audience attribution; token persists after talk |
| **B** — Session cookie download | Browser join → "Download kubeconfig" button | Cookie-bridge between browser and terminal is awkward |
| **C** — Join-code exchange | `curl .../auth/kubeconfig?code=XXXX` | ✅ **Chosen** — join code is already on screen, pure terminal flow |
| **D** — kubectl credential plugin | Custom binary with auto-refresh | Requires audience to pre-install a binary |

---

## Security notes

- `quiz-access` RBAC is minimal: `get`/`list`/`watch`/`create` on CRDs in the `vote` namespace only. kubectl access has identical permissions to browser access.
- Token TTL is ~10 minutes (Kubernetes minimum for TokenRequest).
- Tokens returned in kubeconfig are validated by TokenReview on each request through `forward-auth-decision`.
- The join code rotates every 15 seconds; a token exchange must happen within the TTL window (60s by default).
