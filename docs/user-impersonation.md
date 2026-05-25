# Attributing Kubernetes Writes To End Users

This doc is for an application that lets people sign in with a lightweight
identity (a nickname, a join code, an OAuth handle) and then mutates Kubernetes
resources on their behalf — and wants the **Kubernetes audit log** (and anything
that watches it, such as `gitops-reverser`) to record the **end user**, not the
application's ServiceAccount.

The pattern used here is plain Kubernetes user impersonation. No OIDC issuer,
no per-user ServiceAccounts, no cluster bootstrap flags. Any cluster that
honors `Impersonate-*` headers — which every conformant cluster does — supports
this out of the box.

## What you get

Before:

```json
"user": {
  "username": "system:serviceaccount:voter:auth-service",
  "groups": ["system:authenticated", ...]
}
```

After, when `Simon` clicks "save" in the admin UI:

```json
"user": {
  "username": "system:serviceaccount:voter:auth-service",
  "groups": ["system:authenticated", ...]
},
"impersonatedUser": {
  "username": "Simon",
  "groups": ["voter-audience", "system:authenticated"]
}
```

`gitops-reverser` reads the impersonated identity off the audit event and uses
it as the author of the generated Git commit. The commit log on the demo
screen then shows real names ("Simon committed: lowered espresso price") rather
than the same opaque ServiceAccount over and over.

## When this is a fit

| Use it when | Don't use it when |
|---|---|
| Your app already authenticates users itself (cookies, JWTs, join codes) and you just want their identity to surface in audit logs. | You need users to authenticate directly to the Kubernetes API — use OIDC instead. |
| You want per-user attribution without provisioning K8s users or SAs ahead of time. | You need per-user RBAC. Impersonation gives you a free-form *username* but K8s does not look up that user's groups — RBAC has to bind to a group you set explicitly. |
| The set of operations users can perform is small and you can express them as one group. | Each user needs different permissions. |

## How it works (one paragraph)

Your backend keeps its existing ServiceAccount and bearer token. When it makes
a write on behalf of an end user, it adds two headers:

```
Impersonate-User: Simon
Impersonate-Group: voter-audience
```

The K8s API server then:

1. Authenticates your ServiceAccount (existing behavior).
2. Checks that your SA has `impersonate` permission for the requested user and
   group.
3. **Drops your SA's identity** for the rest of the request and replaces it
   with the impersonated identity (`Simon`, with group `voter-audience` only —
   no merging with your SA's groups).
4. Authorizes the actual operation (e.g. `patch coffeeconfigs`) against the
   impersonated identity.
5. Writes both identities into the audit event.

Step 3 is the load-bearing part: nothing about Simon needs to exist in K8s.
The group is what carries permissions; the username is essentially a label
that flows into audit logs.

## Required Kubernetes resources

You need three RBAC pieces. Names below are the ones used in this repo —
substitute your own.

### 1. Let your backend impersonate

Grant your application's ServiceAccount the `impersonate` verb. The username
is left unrestricted (it's just a label), but the **group is locked down** so
a malicious or buggy nickname can never grant elevated rights — the worst case
is "Simon" inheriting whatever `voter-audience` is allowed to do.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: voter-impersonator
rules:
  # Free-form usernames — cosmetic, end up in audit logs / commit authors.
  - apiGroups: [""]
    resources: ["users"]
    verbs: ["impersonate"]
  # Group is restricted to a single value via resourceNames.
  - apiGroups: [""]
    resources: ["groups"]
    verbs: ["impersonate"]
    resourceNames: ["voter-audience"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: voter-impersonator
subjects:
  - kind: ServiceAccount
    name: auth-service          # whatever your backend runs as
    namespace: voter
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: voter-impersonator
```

> ⚠️ Without the `resourceNames` constraint on `groups`, your backend could
> impersonate `system:masters` and become cluster-admin. Always pin the group.

### 2. Define what the impersonated users can do

A normal `Role` (or `ClusterRole`) bound to the **group**, not to any
individual user:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: voter-audience
  namespace: voter
rules:
  - apiGroups: ["examples.configbutler.ai"]
    resources: ["coffeeconfigs"]
    verbs: ["get", "list", "watch", "patch", "update"]
  - apiGroups: ["examples.configbutler.ai"]
    resources: ["quizsubmissions"]
    verbs: ["get", "list", "watch", "create"]
  # Optional — only needed if your backend also creates a ConfigButler
  # CommitRequest under the same impersonated identity (see "Post-write
  # finalize signals" below).
  - apiGroups: ["configbutler.ai"]
    resources: ["commitrequests"]
    verbs: ["create", "get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: voter-audience
  namespace: voter
subjects:
  - kind: Group
    name: voter-audience
    apiGroup: rbac.authorization.k8s.io
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: voter-audience
```

One binding to a group covers every user your backend ever impersonates. No
per-user RoleBindings to create or clean up.

### 3. Your backend's own access stays as-is

Anything the backend does on its own behalf (reading config to render a page,
rotating secrets, etc.) keeps using the same ServiceAccount with its existing
permissions. Impersonation is purely opt-in per-call on the write paths where
you want the user's name to appear.

## Backend code: making an impersonated call

The Go client uses `rest.ImpersonationConfig` on a copy of the in-cluster
config. Build a fresh dynamic (or typed) client per request — it's cheap and
keeps the impersonated config out of any shared cache.

```go
import (
    "k8s.io/client-go/dynamic"
    "k8s.io/client-go/rest"
)

const audienceGroup = "voter-audience"

// Stash the rest.Config on whatever wraps your kube client.
type kubeClient struct {
    dynamic    dynamic.Interface
    restConfig *rest.Config
    // ...
}

func (c kubeClient) impersonatedDynamic(user string) (dynamic.Interface, error) {
    cfg := rest.CopyConfig(c.restConfig)
    cfg.Impersonate = rest.ImpersonationConfig{
        UserName: user,
        Groups:   []string{audienceGroup},
    }
    return dynamic.NewForConfig(cfg)
}
```

Use it in the write path:

```go
func (c kubeClient) patchCoffeeConfig(ctx context.Context, patch []byte, actor string) (coffeeConfig, error) {
    client := c.dynamic
    if user := sanitizeImpersonationUser(actor); user != "" {
        impersonated, err := c.impersonatedDynamic(user)
        if err != nil {
            return coffeeConfig{}, err
        }
        client = impersonated
    }
    obj, err := client.Resource(gvr).Namespace(ns).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
    // ...
}
```

`actor` comes from your existing session cookie / JWT / whatever. If it's
empty the call falls back to the SA's own identity, which is fine for
internal/system paths.

### Sanitizing the username

The username is free-form, so sanitize it before sending. Two rules matter:

```go
func sanitizeImpersonationUser(actor string) string {
    user := strings.TrimSpace(actor)
    if user == "" {
        return ""
    }
    // Refuse to ever impersonate a built-in identity even if RBAC happened
    // to allow it. This is a defense-in-depth check, not the primary guard.
    if strings.HasPrefix(strings.ToLower(user), "system:") {
        return ""
    }
    return user
}
```

The primary guard is still the group restriction in RBAC — sanitization is
just to keep the audit log readable and to refuse footguns at the edge.

## Verification

Once deployed, check three things:

**1. RBAC is wired up:**

```sh
kubectl auth can-i --as=system:serviceaccount:voter:auth-service \
  impersonate users
# expect: yes

kubectl auth can-i --as=system:serviceaccount:voter:auth-service \
  impersonate groups/voter-audience
# expect: yes

kubectl auth can-i --as=system:masters --as-group=voter-audience \
  patch coffeeconfigs -n voter
# expect: yes (the impersonated identity has the permission)
```

**2. End-to-end attribution:**

Make an authenticated change through your app, then look at audit logs (or
whatever consumes them):

```sh
kubectl logs -n configbutler deploy/gitops-reverser | grep impersonatedUser
```

Expect the `impersonatedUser.username` field to carry the nickname you signed
in with.

**3. Group restriction holds:**

Manually test that an attempt to escalate fails. From a debug pod with your
backend's token mounted:

```sh
curl -k \
  -H "Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)" \
  -H "Impersonate-User: attacker" \
  -H "Impersonate-Group: system:masters" \
  https://kubernetes.default.svc/api/v1/namespaces
# expect: 403 Forbidden
```

If this returns `200`, the `resourceNames` constraint on the group rule isn't
applied — fix that before going further.

## Post-write finalize signals (ConfigButler CommitRequest)

If you're using [gitops-reverser](https://github.com/configbutler/gitops-reverser),
you'll often want to do **two** things under the user's identity:

1. Mutate the watched resource (here: `CoffeeConfig`) — so the change shows up
   in the audit log as the user.
2. Create a `CommitRequest` to finalize the open commit window immediately,
   with the user's typed save-message as the Git commit message — so the
   resulting Git commit has both the right author *and* the right subject
   line.

Both calls must be made under the **same** impersonated identity. ConfigButler
binds the finalize signal to its open window by `(effective audit user,
GitTarget)`; if the patch is impersonated and the CommitRequest is not (or
vice versa), the bind fails silently and the CommitRequest terminates with
`NoOpenWindow`.

The CommitRequest resource itself is small:

```yaml
apiVersion: configbutler.ai/v1alpha1
kind: CommitRequest
metadata:
  generateName: coffee-save-
  namespace: voter        # must match the referenced GitTarget's namespace
spec:
  gitTargetRef:
    name: voter-coffee    # the GitTarget you configured at install time
  message: "Lowered espresso price"  # optional; subject + body, 1–1024 chars
```

Go code that creates it under impersonation, lifted from the auth-service:

```go
func (c kubeClient) createCommitRequest(ctx context.Context, params createCommitRequestParams) (string, error) {
    if strings.TrimSpace(params.GitTargetName) == "" {
        return "", errors.New("missing git target name")
    }

    client := c.dynamic
    if user := sanitizeImpersonationUser(params.Actor); user != "" {
        impersonated, err := c.impersonatedDynamic(user)  // same helper as the patch path
        if err != nil {
            return "", err
        }
        client = impersonated
    }

    spec := map[string]interface{}{
        "gitTargetRef": map[string]interface{}{"name": params.GitTargetName},
    }
    if msg := strings.TrimSpace(params.Message); msg != "" {
        spec["message"] = msg
    }

    obj := &unstructured.Unstructured{
        Object: map[string]interface{}{
            "apiVersion": "configbutler.ai/v1alpha1",
            "kind":       "CommitRequest",
            "metadata": map[string]interface{}{
                "generateName": "coffee-save-",
                "namespace":    params.Namespace,
            },
            "spec": spec,
        },
    }

    created, err := client.Resource(commitRequestGVR()).
        Namespace(params.Namespace).
        Create(ctx, obj, metav1.CreateOptions{})
    if err != nil {
        return "", err
    }
    return created.GetName(), nil
}
```

Wiring it into a handler:

```go
// 1. Patch the watched resource as the user.
updated, err := kube.patchCoffeeConfig(ctx, body, session.Nickname)
if err != nil {
    writeKubeError(w, err)
    return
}

// 2. After a successful patch, ask ConfigButler to finalize the window.
//    Failures here must not fail the response — the resource is already saved.
if target := cfg.ConfigButlerGitTargetName; target != "" {
    if _, err := kube.createCommitRequest(ctx, createCommitRequestParams{
        Actor:         session.Nickname,
        GitTargetName: target,
        Message:       strings.TrimSpace(r.Header.Get("X-Change-Reason")),
    }); err != nil {
        log.Printf("commitrequest: create failed: %v", err)
    }
}

writeJSON(w, http.StatusOK, updated)
```

Three rules that catch the common bugs:

- **Build one impersonated client and reuse it for both calls** (or recreate
  it consistently via the same helper — the implementation above does the
  latter). The audit identities must match exactly.
- **Trim the message before send.** ConfigButler's CEL validation rejects
  pure-whitespace strings and most ASCII control characters. Empty/whitespace
  → omit `spec.message` entirely so it falls back to the grouped message.
- **Don't fail the HTTP response on a CommitRequest error.** The user's data
  is already in `etcd`. Log it, surface it later in a status feed if you
  want, but return `200`.

Extra RBAC the audience group needs for this path is one rule on
`configbutler.ai/commitrequests` with verb `create` (plus `get/list/watch` if
you later poll `status.phase` / `status.sha`). It's already shown in the
RBAC block above under "Required Kubernetes resources" as an optional rule.

## Operational notes

- **Token refresh after RBAC changes:** ServiceAccount tokens cache the bearer
  identity but not RBAC decisions, so K8s picks up new permissions on the next
  request. No pod restart needed unless your client caches `selfsubjectreview`
  results.
- **Cost per request:** `rest.CopyConfig` plus `dynamic.NewForConfig` is on the
  order of a few hundred microseconds. For high-RPS paths, build a small cache
  keyed on `(username, group-tuple)` — but most apps don't need it.
- **Multiple groups:** if you outgrow one group, list them in
  `ImpersonationConfig.Groups`. Each one needs its own `resourceNames` entry on
  the impersonate rule.
- **Extra fields:** K8s also supports `Impersonate-Extra-<key>` headers (set
  via `ImpersonationConfig.Extra`). `gitops-reverser` and most audit consumers
  ignore them; use them only if you have a downstream that explicitly reads
  one.

## Limitations

- **kubectl path is not covered.** Users who download a kubeconfig and run
  `kubectl get ...` directly hit the API server with a ServiceAccount token
  and bypass your backend. The audit log shows the SA, not the user, for
  those calls. If you need attribution there too, you have to either:
  - put a proxy in front that injects impersonate headers based on a session
    cookie (more complex), or
  - move to OIDC, where the identity is in the token itself.
- **No automatic group resolution.** "Simon" doesn't mean anything to K8s.
  If you forget to set `Groups` on the impersonation config, the request
  authenticates as Simon with zero groups and gets a `403` on every
  authorization check.
- **`system:authenticated` is auto-added** to the impersonated identity's
  groups by the API server. Don't bind `system:authenticated` to anything
  privileged in your cluster — it now includes every nickname that ever
  signs in.

## Why not (alternatives in one line)

- **One ServiceAccount per user:** works, but every login churns the K8s API
  with SA + RoleBinding create/delete. Painful at scale.
- **OIDC:** cleaner identity model (no impersonation indirection), but
  requires setting `--oidc-*` flags at API-server bootstrap, which managed
  clusters often don't expose. Worth it for a permanent setup, overkill for
  a demo or short-lived audience flow.
- **Custom audit annotations:** can record a correlation ID, but K8s won't
  recognize it as an identity — your audit consumer has to parse it. Fine if
  you control the consumer, awkward if you don't (e.g. `gitops-reverser`
  expects a real impersonated user).
