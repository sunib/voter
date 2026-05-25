# Integrator Guide — Direct K8s Calls With Per-User Impersonation

This guide is for a repo that already has the shape:

- **Browser → Traefik → Kubernetes API server**, with a `forwardAuth` middleware
  pointing at an auth service that validates a session cookie and injects an
  `Authorization` header upstream.
- A single shared ServiceAccount whose token is what the API server actually
  sees. Audit logs record that SA, not the real participant.

…and wants every K8s write to be **audited as the actual user** without giving
up the "browser talks directly to the K8s API" property.

The pattern is plain Kubernetes user impersonation, plumbed through `forwardAuth`
response headers. No OIDC issuer, no per-user ServiceAccounts, no apiserver
flags. See the sibling design note [user-impersonation.md](user-impersonation.md)
for the in-process variant (auth service calls K8s itself with impersonation);
this doc is the variant where **the browser keeps making direct K8s calls**.

The two main things that change in your YAML:

1. A new **impersonator ServiceAccount** with impersonate-only RBAC.
2. A new **header-strip Traefik middleware** chained in front of `forwardAuth`,
   plus `Impersonate-*` listed in `forwardAuth.authResponseHeaders`.

Everything else flows from those.

## Request flow after the change

```
Browser  ──POST /apis/.../<crd>───►  Traefik
                                       │
                                       │  1) strip-impersonate middleware
                                       │     wipes any client-supplied
                                       │     Impersonate-* request headers
                                       ▼
                                     Traefik
                                       │
                                       │  2) auth-forwarder middleware calls
                                       │     auth-service /forward-auth-decision
                                       ▼
                                  auth-service ──► validate session cookie
                                       │           derive demo:<id>, group, extras
                                       │           mint SA token (impersonator SA)
                                       │           respond with
                                       │             Authorization: Bearer <token>
                                       │             Impersonate-User: demo:<id>
                                       │             Impersonate-Group: <audience-group>
                                       │             Impersonate-Extra-...: ...
                                       ▼
                                     Traefik
                                       │
                                       │  3) copies the listed authResponseHeaders
                                       │     onto the upstream request
                                       ▼
                              Kubernetes API server
                                       │
                                       │  authenticates SA token
                                       │  checks SA has `impersonate` RBAC
                                       │  re-authorizes the request as demo:<id>
                                       ▼
                                  audit log records demo:<id>
```

## Step 1 — Create the impersonator ServiceAccount

This SA's only job is to be the bearer-token identity that the API server
authenticates. It has **no CRUD verbs** on your CRDs. All resource access is
granted to the audience *group*, not to this SA.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: audience-impersonator   # name whatever you want
  namespace: <your-ns>
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: audience-impersonator
rules:
  # "users" verb is unrestricted at the RBAC layer because the auth-service
  # always sets Impersonate-User to a value it controls (e.g. demo:<stableId>).
  # The security boundary is the strip middleware + the auth-service code,
  # NOT this rule. See "Important parts" below.
  - apiGroups: [""]
    resources: ["users"]
    verbs: ["impersonate"]
  # Pin Impersonate-Group to a single value so even a misbehaving auth-service
  # can never elevate someone into system:masters or another high-power group.
  - apiGroups: [""]
    resources: ["groups"]
    verbs: ["impersonate"]
    resourceNames: ["voter-audience"]   # <-- the one group bindings target
  # Only required if you also forward Impersonate-Extra-* (e.g. for downstream
  # commit attribution). Each extra key needs its own line.
  - apiGroups: ["authentication.k8s.io"]
    resources:
      - "userextras/configbutler.ai/claims/display-name"
      - "userextras/configbutler.ai/claims/email"
    verbs: ["impersonate"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: audience-impersonator
subjects:
  - kind: ServiceAccount
    name: audience-impersonator
    namespace: <your-ns>
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: audience-impersonator
```

Then make sure your **auth-service** can mint tokens for this SA — its existing
Role probably already has `serviceaccounts/token` create with a `resourceNames`
list; add the new SA name to that list:

```yaml
- apiGroups: [""]
  resources: ["serviceaccounts/token"]
  verbs: ["create"]
  resourceNames:
    - audience-impersonator   # <-- add
```

## Step 2 — Grant the audience group the actual write permissions

The impersonator SA does no writes. **The audience group does the writes.**
This is the binding that controls "what can a logged-in participant change?":

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: voter-audience
  namespace: <your-ns>
rules:
  - apiGroups: ["examples.configbutler.ai"]
    resources: ["quizsubmissions"]
    verbs: ["create"]            # whatever your participants need
  # ...add more resources/verbs here as your demo grows
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: voter-audience
  namespace: <your-ns>
subjects:
  - kind: Group                  # NOTE: Group, not ServiceAccount
    name: voter-audience
    apiGroup: rbac.authorization.k8s.io
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: voter-audience
```

Keep this Role narrow — it's the effective permission set of every signed-in
participant.

## Step 3 — Traefik: strip inbound Impersonate-\*

This is the single most important YAML in the whole change. Without it, a
participant can send their own `Impersonate-User: system:admin` header to the
public endpoint, and because the impersonator SA has unrestricted
`impersonate users`, the API server will accept it.

```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: strip-impersonate
  namespace: <your-ns>
spec:
  headers:
    customRequestHeaders:
      # Setting to empty string removes the header in Traefik.
      Impersonate-User: ""
      Impersonate-Group: ""
      Impersonate-Uid: ""
      # Enumerate every Impersonate-Extra-* key you forward in step 4.
      # Traefik does not support wildcard removal — if you add a new extra
      # later, add it here too.
      Impersonate-Extra-configbutler.ai%2Fclaims%2Fdisplay-name: ""
      Impersonate-Extra-configbutler.ai%2Fclaims%2Femail: ""
```

## Step 4 — Traefik: forward Impersonate-\* from the auth response

Extend the existing `forwardAuth` middleware so the headers your auth-service
sets on the auth-response actually reach the upstream:

```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: auth-forwarder
  namespace: <your-ns>
spec:
  forwardAuth:
    address: http://auth-service.<your-ns>.svc.cluster.local:8080/private/forward-auth-decision
    trustForwardHeader: true
    authResponseHeaders:
      - Authorization
      - Impersonate-User
      - Impersonate-Group
      - Impersonate-Extra-configbutler.ai%2Fclaims%2Fdisplay-name
      - Impersonate-Extra-configbutler.ai%2Fclaims%2Femail
    addAuthCookiesToResponse:
      - <your-session-cookie-name>
```

`authResponseHeaders` is a **whitelist**: any header the auth-service sets that
is not on this list is dropped by Traefik. So if you add a new
`Impersonate-Extra-*` to the code path, list it here too.

## Step 5 — IngressRoute middleware order

The strip middleware **must run before** the forwardAuth middleware. Traefik
chains middlewares in array order — get this wrong and step 3 buys you nothing:

```yaml
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: kube-api
  namespace: <your-ns>
spec:
  entryPoints: [websecure]
  routes:
    - match: Host(`coffee.example.com`) && (PathPrefix(`/api`) || PathPrefix(`/apis`))
      kind: Rule
      middlewares:
        - name: strip-impersonate    # ORDER MATTERS: this first
        - name: auth-forwarder       # then this
      services:
        - name: kubernetes
          namespace: default
          port: 443
          scheme: https
          serversTransport: default-kube-apiserver-transport@kubernetescrd
  tls: {}
```

## Step 6 — auth-service code contract

Three rules your auth-service handler must follow on the `forwardAuth` endpoint.
None of these are YAML, but they are part of the same security contract:

1. **Always set `Impersonate-User` on a 200 response.** If you can't derive the
   user — for any reason — return 401, do not return 200. A 200 with
   `Authorization` but without `Impersonate-User` lands at the API server as
   the raw impersonator SA, which can impersonate anything.

2. **Set `Impersonate-Group`** to the same audience group the binding in step 2
   targets. Hardcode this value; don't take it from the cookie.

3. **Validate the identity** before emitting headers. Run any nickname/email
   normalisation up front and reject malformed sessions with 401. The headers
   are a trust boundary — what you set is what authorizes the upstream call.

In Go, the body of the browser branch is roughly:

```go
session, ok := getBrowserSession(r)
if !ok {
    http.Error(w, "session required", http.StatusUnauthorized)
    return
}
identity, err := audienceIdentityFromSession(session.StableID, session.DisplayName, session.Email)
if err != nil {
    http.Error(w, "invalid session identity", http.StatusUnauthorized)
    return
}

token, err := mintImpersonatorToken(...)
if err != nil {
    http.Error(w, "token request failed", http.StatusForbidden)
    return
}

w.Header().Set("Authorization", "Bearer "+token)
w.Header().Set("Impersonate-User", identity.Username)        // e.g. "demo:521541"
w.Header().Set("Impersonate-Group", "voter-audience")        // hardcoded
// Extra keys containing "/" must be percent-encoded in the HTTP header name.
w.Header()["Impersonate-Extra-configbutler.ai%2Fclaims%2Fdisplay-name"] = []string{identity.DisplayName}
w.Header()["Impersonate-Extra-configbutler.ai%2Fclaims%2Femail"] = []string{identity.Email}
w.WriteHeader(http.StatusOK)
```

## Important parts (don't skip)

The security model rests on **four** invariants. If any one of them is broken,
the whole thing collapses into "anyone can impersonate anyone."

| Invariant | Enforced where | What breaks if missing |
|---|---|---|
| Inbound `Impersonate-*` are stripped | `strip-impersonate` middleware (step 3) chained before forwardAuth (step 5) | Client supplies `Impersonate-User: kubernetes-admin`. Auth-service also sets `Impersonate-User: demo:521541`. Traefik sees both — auth response wins for headers Traefik forwards, but headers Traefik does **not** know about (e.g. an `Impersonate-Extra-*` the auth-service never sets) pass through. |
| Only listed headers reach upstream | `forwardAuth.authResponseHeaders` (step 4) is a whitelist | Same as above, in the reverse direction — auth-service sets a header that never reaches the API server. |
| Impersonator SA token cannot do anything *as itself* | ClusterRole in step 1 has only `impersonate` verbs, no CRUD | A leaked token becomes a backdoor to whatever else you grant it. |
| Auth-service fails closed on bad identity | Code contract in step 6 | A 200 with `Authorization` but no `Impersonate-User` lets the raw SA through. |

A useful belt-and-braces: pin `Impersonate-Group` with `resourceNames` (step 1)
so even a buggy auth-service can't elevate anyone into a powerful group. Doing
the same for `users` is harder because nicknames are dynamic, but you can add a
`ValidatingAdmissionPolicy` that rejects any request whose `Impersonate-User`
doesn't match your expected pattern (e.g. `^demo:[1-9][0-9]{5}$`). Optional;
nice if you're paranoid.

## Header naming notes

- `Impersonate-User`, `Impersonate-Group`, `Impersonate-Uid` are fixed names.
  HTTP header names are case-insensitive but Traefik's
  `authResponseHeaders` matcher has historically been case-sensitive in older
  2.x releases. Match casing between your auth-service `w.Header().Set(...)`
  call, the strip middleware, and the `authResponseHeaders` list. We use
  `Impersonate-User` everywhere.
- `Impersonate-Extra-<key>` — the `<key>` part becomes a userextras subresource
  name. Each key needs its own `userextras/<key>` line in the ClusterRole and
  its own entry in `authResponseHeaders` *and* in the strip middleware.

## Verification (do this once after rollout)

1. Submit one write through the UI, then look at the audit log:
   ```
   user.username                  == "system:serviceaccount:<ns>:audience-impersonator"
   impersonatedUser.username      == "demo:521541"
   impersonatedUser.groups        contains "voter-audience"
   impersonatedUser.extra.<key>   matches the cookie
   ```
2. Confirm the strip middleware works end-to-end:
   ```
   curl -k -X POST 'https://<host>/apis/...' \
     -H 'Cookie: <your-session-cookie>' \
     -H 'Impersonate-User: system:admin'
   ```
   The request should land as `demo:<id>`, not `system:admin`. If it lands as
   `system:admin`, the strip middleware isn't in the chain or isn't running
   before forwardAuth.
3. Confirm a missing session fails closed:
   ```
   curl -k -X POST 'https://<host>/apis/...'   # no cookie
   ```
   Should be 401, not 200.

## Cleanup checklist

If your repo currently routes browser writes through a shared SA like
`quiz-access`, you can usually delete its `create` verbs once impersonation
is verified — those grants are now redundant. Keep a separate commit for the
cleanup so it's easy to roll back independently.
