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
  "username": "demo:simon-koudijs",
  "groups": ["voter-audience", "system:authenticated"],
  "extra": {
    "configbutler.ai/claims/display-name": ["Simon Koudijs"],
    "configbutler.ai/claims/email": ["simon-koudijs@demo.configbutler.ai"]
  }
}
```

`gitops-reverser` reads the impersonated identity and ConfigButler extras off
the audit event and uses them as the author of the generated Git commit. The
commit log on the demo screen then shows real names ("Simon committed: lowered
espresso price") rather than the same opaque ServiceAccount over and over.

## When this is a fit

| Use it when | Don't use it when |
|---|---|
| Your app already authenticates users itself (cookies, JWTs, join codes) and you just want their identity to surface in audit logs. | You need users to authenticate directly to the Kubernetes API — use OIDC instead. |
| You want per-user attribution without provisioning K8s users or SAs ahead of time. | You need per-user RBAC. Impersonation gives you a free-form *username* but K8s does not look up that user's groups — RBAC has to bind to a group you set explicitly. |
| The set of operations users can perform is small and you can express them as one group. | Each user needs different permissions. |

## How it works (one paragraph)

Your backend keeps its existing ServiceAccount and bearer token. When it makes
a write on behalf of an end user, it adds impersonation headers:

```
Impersonate-User: demo:simon-koudijs
Impersonate-Group: voter-audience
Impersonate-Extra-configbutler.ai%2Fclaims%2Fdisplay-name: Simon Koudijs
Impersonate-Extra-configbutler.ai%2Fclaims%2Femail: simon-koudijs@demo.configbutler.ai
```

The K8s API server then:

1. Authenticates your ServiceAccount (existing behavior).
2. Checks that your SA has `impersonate` permission for the requested user and
   group.
3. **Drops your SA's identity** for the rest of the request and replaces it
   with the impersonated identity (`demo:simon-koudijs`, with group
   `voter-audience` only — no merging with your SA's groups).
4. Authorizes the actual operation (e.g. `patch coffeeconfigs`) against the
   impersonated identity.
5. Writes both identities into the audit event.

Step 3 is the load-bearing part: nothing about Simon needs to exist in K8s.
The group is what carries permissions. The username and extras are attribution
fields that flow into audit logs and, for ConfigButler, into Git authoring.

## Recommended integration shape

For apps integrating with ConfigButler, keep the human display name separate
from the Kubernetes impersonation username.

Example:

```text
display name: Simon Koudijs
k8s user:     demo:simon-koudijs
email:        simon-koudijs@demo.configbutler.ai
group:        voter-audience
```

The display name and email do not both need to come from the user. A good demo
or onboarding flow can generate a friendly display name, such as `Demo Guest
42`, and can leave email blank. When email is blank, synthesize a valid address
from the slug, for example `demo-guest-42@demo.configbutler.ai`.

If a participant wants their real email in Git history, let them provide it as
an optional field and validate it before using it. Make it clear in the UI that
email is not required.

Do not use a raw nickname, email, or OAuth display name as `Impersonate-User`.
Kubernetes RBAC can bind permissions directly to `User` subjects, so names such
as `admin`, `kubernetes-admin`, or `system:*` can collide with real cluster
identities. A synthetic prefix such as `demo:` or your product/domain prefix
keeps participant-controlled input away from real users.

ConfigButler/gitops-reverser reads these audit extras when choosing the Git
author:

```text
configbutler.ai/claims/display-name
configbutler.ai/claims/email
```

If the extras are missing or unusable, ConfigButler falls back to the Kubernetes
username. That is safe, but produces less friendly commit authors.

## Integrator checklist

If you are adding this pattern to another app, adjust these pieces together:

- **Identity derivation:** derive a canonical Kubernetes username such as
  `demo:<slug>` from the display name or login identity.
- **Generated display name:** provide a friendly default such as `Demo Guest
  42` so users can continue without typing personal details.
- **Display name:** keep the human-readable name separate and pass it as
  `configbutler.ai/claims/display-name`.
- **Email:** pass a validated real email or a synthetic valid email such as
  `<slug>@demo.configbutler.ai` as `configbutler.ai/claims/email`. Email should
  be optional for the user.
- **RBAC:** grant `impersonate` for users, the one allowed group, and the
  ConfigButler user-extra keys.
- **Authorization group:** bind resource permissions to the fixed group, for
  example `voter-audience`, not to each user.
- **Fail closed:** if a user-initiated request cannot derive a valid
  impersonation identity, reject the request instead of using the backend
  ServiceAccount.
- **Consistent identity:** use the same impersonated identity and extras for
  the watched resource write and the `CommitRequest` create.
- **Verification:** check both the Kubernetes audit event and the resulting Git
  commit author after deployment.

## Required Kubernetes resources

You need three RBAC pieces. Names below are the ones used in this repo —
substitute your own.

### 1. Let your backend impersonate

Grant your application's ServiceAccount the `impersonate` verb. The example
below leaves usernames unrestricted because demo users are generated with a
non-colliding prefix such as `demo:<slug>`. If your participant set is known,
prefer `resourceNames` on `users` too.

The **group is locked down** so an integrating app can only attach the limited
audience group. ConfigButler display name and email are carried as user extras,
so the service account also needs permission to set those exact extra keys.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: voter-impersonator
rules:
  # Synthetic usernames such as demo:simon-koudijs.
  # If the set is known, add resourceNames here too.
  - apiGroups: [""]
    resources: ["users"]
    verbs: ["impersonate"]
  # Group is restricted to a single value via resourceNames.
  - apiGroups: [""]
    resources: ["groups"]
    verbs: ["impersonate"]
    resourceNames: ["voter-audience"]
  # ConfigButler/gitops-reverser reads these extras from audit events to choose
  # the Git author name and email.
  - apiGroups: ["authentication.k8s.io"]
    resources:
      - "userextras/configbutler.ai/claims/display-name"
      - "userextras/configbutler.ai/claims/email"
    verbs: ["impersonate"]
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
>
> ⚠️ Do not pass raw participant input as `Impersonate-User`. Prefix and slug it
> first, or restrict the `users` rule with `resourceNames`.

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
    "errors"
    "strings"

    "k8s.io/client-go/dynamic"
    "k8s.io/client-go/rest"
)

const audienceGroup = "voter-audience"

type audienceIdentity struct {
    Username    string // demo:simon-koudijs
    DisplayName string // Simon Koudijs
    Email       string // simon-koudijs@demo.configbutler.ai
}

// Stash the rest.Config on whatever wraps your kube client.
type kubeClient struct {
    dynamic    dynamic.Interface
    restConfig *rest.Config
    // ...
}

func (c kubeClient) impersonatedDynamic(identity audienceIdentity) (dynamic.Interface, error) {
    if strings.TrimSpace(identity.Username) == "" {
        return nil, errors.New("missing impersonation username")
    }
    cfg := rest.CopyConfig(c.restConfig)
    cfg.Impersonate = rest.ImpersonationConfig{
        UserName: identity.Username,
        Groups:   []string{audienceGroup},
        Extra: map[string][]string{
            "configbutler.ai/claims/display-name": {identity.DisplayName},
            "configbutler.ai/claims/email":        {identity.Email},
        },
    }
    return dynamic.NewForConfig(cfg)
}
```

Use it in the write path:

```go
func (c kubeClient) patchCoffeeConfig(ctx context.Context, patch []byte, identity audienceIdentity) (coffeeConfig, error) {
    client, err := c.impersonatedDynamic(identity)
    if err != nil {
        return coffeeConfig{}, err
    }
    obj, err := client.Resource(gvr).Namespace(ns).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
    // ...
}
```

The display name can come from your existing session cookie / JWT / OAuth
claims, or from a generated default. The email can come from an optional login
field or be synthesized from the display-name slug. Derive `identity.Username`
before calling the Kubernetes client. For user-initiated writes, missing or
invalid identity should return an error; do not silently fall back to the
backend ServiceAccount.

### Deriving the Kubernetes username

Do not sanitize by dropping unsafe input and continuing as the service account.
For user-initiated writes, derive a synthetic username and fail closed if that
cannot be done.

Use `github.com/gosimple/slug` so non-ASCII names (`佐藤`, `Łukasz`, `Müller`)
transliterate via `go-unidecode` and never collapse to empty — a hand-rolled
ASCII-only slug locks those users out. Use `net/mail.ParseAddress` from the
standard library for email validation and additionally reject `<>\n\r` so the
value can't break a Git author signature line. The display name itself is also
written into the Git author line, so apply the same char filter there.

```go
import (
    "errors"
    "net/mail"
    "strings"

    "github.com/gosimple/slug"
)

const (
    demoSlugMaxLen        = 40
    demoDisplayNameMaxLen = 64
)

func audienceIdentityFromDisplayName(displayName, optionalEmail, emailDomain string) (audienceIdentity, error) {
    name, err := normalizeDisplayName(displayName)
    if err != nil {
        return audienceIdentity{}, err
    }

    s := slug.MakeLang(name, "en")
    if len(s) > demoSlugMaxLen {
        s = strings.TrimRight(s[:demoSlugMaxLen], "-")
    }
    if s == "" {
        return audienceIdentity{}, errors.New("display name cannot form an identity")
    }

    domain := strings.TrimSpace(emailDomain)
    if domain == "" {
        domain = "demo.configbutler.ai"
    }
    email := strings.TrimSpace(optionalEmail)
    if email == "" {
        email = s + "@" + domain
    } else if err := validateAuthorEmail(email); err != nil {
        return audienceIdentity{}, err
    }

    return audienceIdentity{
        Username:    "demo:" + s,
        DisplayName: name,
        Email:       email,
    }, nil
}

func normalizeDisplayName(raw string) (string, error) {
    name := strings.TrimSpace(raw)
    if name == "" {
        return "", errors.New("display name is required")
    }
    if strings.ContainsAny(name, "<>\n\r") {
        return "", errors.New("display name contains characters unsafe for git author lines")
    }
    if len(name) > demoDisplayNameMaxLen {
        return "", errors.New("display name is too long")
    }
    return name, nil
}

// validateAuthorEmail accepts only bare addresses (no "Name <addr>" form) and
// rejects characters that would break a git author signature line.
func validateAuthorEmail(raw string) error {
    if strings.ContainsAny(raw, "<>\n\r") {
        return errors.New("invalid author email")
    }
    addr, err := mail.ParseAddress(raw)
    if err != nil || addr.Name != "" || addr.Address != raw {
        return errors.New("invalid author email")
    }
    return nil
}
```

The group restriction in RBAC is still the primary authorization guard, but the
synthetic username prevents participant-controlled names from colliding with
real cluster users.

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

kubectl auth can-i --as=system:serviceaccount:voter:auth-service \
  impersonate userextras/configbutler.ai/claims/display-name.authentication.k8s.io
# expect: yes

kubectl auth can-i --as=system:serviceaccount:voter:auth-service \
  impersonate userextras/configbutler.ai/claims/email.authentication.k8s.io
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

Expect `impersonatedUser.username` to carry the synthetic user
(`demo:simon-koudijs`) and `impersonatedUser.extra` to carry the ConfigButler
display name and email keys.

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

Go code that creates it under the same impersonated identity:

```go
func (c kubeClient) createCommitRequest(ctx context.Context, params createCommitRequestParams) (string, error) {
    if strings.TrimSpace(params.GitTargetName) == "" {
        return "", errors.New("missing git target name")
    }

    client, err := c.impersonatedDynamic(params.Identity)
    if err != nil {
        return "", err
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
identity, err := audienceIdentityFromDisplayName(session.Nickname, session.Email, "demo.configbutler.ai")
if err != nil {
    http.Error(w, "invalid session identity", http.StatusBadRequest)
    return
}

// 1. Patch the watched resource as the user.
updated, err := kube.patchCoffeeConfig(ctx, body, identity)
if err != nil {
    writeKubeError(w, err)
    return
}

// 2. After a successful patch, ask ConfigButler to finalize the window.
//    Failures here must not fail the response — the resource is already saved.
if target := cfg.ConfigButlerGitTargetName; target != "" {
    if _, err := kube.createCommitRequest(ctx, createCommitRequestParams{
        Identity:      identity,
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
- **Extra fields:** K8s supports `Impersonate-Extra-<key>` headers via
  `ImpersonationConfig.Extra`. ConfigButler/gitops-reverser reads
  `configbutler.ai/claims/display-name` and `configbutler.ai/claims/email`;
  other extras should only be added when a downstream consumer explicitly
  reads them.
- **Rollout order for `userextras` RBAC:** apply the
  `authentication.k8s.io/userextras/...` impersonate rules *before* rolling a
  binary that sends those extras — without them, every patch 403s. If you can't
  guarantee the ordering, gate the `Extra:` map behind a config flag (default
  on) so you have a kill switch while the RBAC propagates.

## Limitations

- **kubectl path is not covered.** Users who download a kubeconfig and run
  `kubectl get ...` directly hit the API server with a ServiceAccount token
  and bypass your backend. The audit log shows the SA, not the user, for
  those calls. If you need attribution there too, you have to either:
  - put a proxy in front that injects impersonate headers based on a session
    cookie (more complex), or
  - move to OIDC, where the identity is in the token itself.
- **No automatic group resolution.** `demo:simon-koudijs` doesn't mean anything
  to K8s.
  If you forget to set `Groups` on the impersonation config, the request
  authenticates as the synthetic user with zero groups and gets a `403` on every
  authorization check.
- **`system:authenticated` is auto-added** to the impersonated identity's
  groups by the API server. Don't bind `system:authenticated` to anything
  privileged in your cluster — it now includes every synthetic user that ever
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
