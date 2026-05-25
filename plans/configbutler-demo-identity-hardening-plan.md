# ConfigButler Demo Identity Hardening Plan

## Purpose

Harden the voter demo identity flow before exposing Kubernetes API access to
demo participants, while improving ConfigButler/gitops-reverser attribution.

The goal is:

- participants can still use a friendly display name such as `Simon Koudijs`
- participants do not need to type personal details to join the demo
- participants may optionally provide their own email if they want it in Git
  author metadata
- Kubernetes audit users cannot collide with real cluster users
- ConfigButler commits use the friendly display name and a valid email address
- unsafe identity input fails closed instead of falling back to the
  `auth-service` ServiceAccount

This plan is written as a handoff document for a fresh implementation context.

## Current Prototype

The last prototype commits introduced:

- user impersonation for `CoffeeConfig` patches
- creation of ConfigButler `CommitRequest` objects after a successful patch
- a fixed impersonated group named `voter-audience`
- RBAC for `auth-service` to impersonate users and the `voter-audience` group
- RBAC for impersonated audience members to patch `CoffeeConfig` and create
  `CommitRequest`

Important files:

- `auth-service/coffee_handlers.go`
- `auth-service/kube_client.go`
- `auth-service/config.go`
- `auth-service/kube_client_test.go`
- `auth-service/main_test.go`
- `k8s/auth-service-rbac.yaml`
- `plans/configbutler-save-message-plan.md`
- `docs/user-impersonation.md`

## Security Findings To Address

### 1. Unrestricted User Impersonation Is Too Broad

Current RBAC allows `auth-service` to impersonate arbitrary Kubernetes users:

```yaml
- apiGroups: [""]
  resources: ["users"]
  verbs: ["impersonate"]
```

The group is pinned to `voter-audience`, which is good, but Kubernetes RBAC can
also bind permissions directly to `User` subjects. A participant choosing a
nickname such as `admin`, `kubernetes-admin`, or another locally bound username
could inherit that user's direct RBAC grants.

Mitigation:

- never use the raw participant nickname as `Impersonate-User`
- derive a synthetic Kubernetes username with a demo-specific prefix:

```text
Simon Koudijs -> demo:simon-koudijs
system:masters -> demo:system-masters
kubernetes-admin -> demo:kubernetes-admin
```

This prevents collisions with existing Kubernetes users.

### 2. Unsafe Impersonation Currently Fails Open

The current sanitizer rejects `system:` by returning an empty string. The write
path then silently uses the non-impersonated `auth-service` client.

That means a user-controlled identity can accidentally or intentionally bypass
the `voter-audience` authorization path and write as the service account.

Mitigation:

- do not return an empty user as a signal to "skip impersonation" for
  user-initiated writes
- derive an identity before the Kubernetes write
- if identity derivation fails, reject the HTTP request
- only internal/system calls may use the service account directly

### 3. Service Account Fallback Permissions Are Too Wide

`auth-service` currently has direct access to `coffeeconfigs` and
`commitrequests` as a fallback. For the demo write path, the intended
authorization boundary is the impersonated `voter-audience` identity.

Mitigation:

- keep only the service-account permissions needed for non-user operations
- prefer namespaced `Role`s for namespaced resources
- remove unused `get/list/watch` on `commitrequests` unless status is shown
- consider `resourceNames: ["testnet-coffee"]` for the CoffeeConfig object

### 4. Participants Can Patch Infrastructure-Like CoffeeConfig Fields

The admin UI and CRD allow editing fields such as:

- `spec.mail.apiKeySecretRef`
- `spec.payments.apiKeySecretRef`
- provider and payment settings

If a controller dereferences those values, participants can point integrations
at unintended in-namespace secrets or providers.

Mitigation:

- add server-side patch-path allowlisting for the demo
- or add CRD CEL validation that freezes infrastructure fields
- at minimum, prevent changes to `*.apiKeySecretRef` for audience users

### 5. Public Kube API Proxy Accepts Any Valid Cluster Token

The forward-auth path TokenReviews any bearer token and forwards it when
authenticated. If another cluster token leaks, this ingress can become an
external API-server route for that token.

Mitigation:

- if the intended kubectl flow only uses minted `quiz-access` tokens, require:

```text
system:serviceaccount:voter:quiz-access
```

- reject other authenticated bearer users at the public ingress

### 6. CommitRequest Spam Is Possible

Every successful patch can create a `CommitRequest`. A logged-in participant can
hammer the save endpoint and generate audit/commit churn.

Mitigation:

- add per-session or per-IP rate limiting
- skip `CommitRequest` creation if the patch does not change the object
- optionally debounce save finalization per participant

## Target Identity Model

Keep the login flow low-friction. A participant may provide:

- a display name, or use a generated example display name
- an email address, or leave it empty

The email field is optional. If it is empty, generate a valid synthetic email
from the display-name slug and `CONFIGBUTLER_DEMO_EMAIL_DOMAIN`.

The first implementation can keep the existing session shape and synthesize
email from the nickname:

```json
{
  "nickname": "Simon Koudijs"
}
```

If the login form starts accepting an optional email, store it alongside the
nickname:

```json
{
  "nickname": "Simon Koudijs",
  "email": "simon@example.com"
}
```

Derive a Kubernetes/ConfigButler identity at write time:

```go
type audienceIdentity struct {
    Username    string // demo:simon-koudijs
    DisplayName string // Simon Koudijs
    Email       string // simon-koudijs@demo.configbutler.ai
}
```

Use the fields as follows:

- `Username`: Kubernetes `Impersonate-User`
- `DisplayName`: ConfigButler audit extra for Git author name
- `Email`: ConfigButler audit extra for Git author email, either user-provided
  or synthetic
- original session nickname: UI and local change history display

Do not require an email field. A synthetic email is enough for the demo and
keeps the login flow approachable.

## Login UX

Lower the barrier at the door:

- prefill or offer a generated display name, for example `Demo Guest 42`
- let users replace it with their own name if they want
- show email as optional, not required
- if email is blank, generate one from the display-name slug
- if email is provided, validate it before storing it in the session
- if email validation fails, ask the user to fix it or leave it blank

Suggested UI copy:

```text
Display name
Demo Guest 42

Email (optional)
Used only as the Git author email if you want your commit to show it.
```

Generated examples should be friendly and non-personal:

```text
Demo Guest 42
Coffee Editor 17
TestNet Visitor 8
```

## ConfigButler Audit Extra Keys

gitops-reverser reads these audit extra keys:

```text
configbutler.ai/claims/display-name
configbutler.ai/claims/email
```

Kubernetes carries these via impersonation extras:

```go
rest.ImpersonationConfig{
    UserName: "demo:simon-koudijs",
    Groups:   []string{"voter-audience"},
    Extra: map[string][]string{
        "configbutler.ai/claims/display-name": {"Simon Koudijs"},
        "configbutler.ai/claims/email":        {"simon-koudijs@demo.configbutler.ai"},
    },
}
```

Kubernetes will encode these as `Impersonate-Extra-*` headers. For keys with a
slash, the header representation uses percent encoding, e.g.
`configbutler.ai%2Fclaims%2Femail`.

Reference:

- https://kubernetes.io/docs/reference/access-authn-authz/user-impersonation/

## Proposed Configuration

Add config fields:

```go
ConfigButlerIdentityExtrasEnabled bool   `envconfig:"CONFIGBUTLER_IDENTITY_EXTRAS_ENABLED" default:"true"`
ConfigButlerDemoEmailDomain       string `envconfig:"CONFIGBUTLER_DEMO_EMAIL_DOMAIN" default:"demo.configbutler.ai"`
```

Recommended constants:

```go
const (
    audienceGroupName = "voter-audience"

    configButlerDisplayNameExtraKey = "configbutler.ai/claims/display-name"
    configButlerEmailExtraKey       = "configbutler.ai/claims/email"
)
```

## Identity Derivation Rules

Input without email:

```text
Simon Koudijs
```

Output:

```text
Username:    demo:simon-koudijs
DisplayName: Simon Koudijs
Email:       simon-koudijs@demo.configbutler.ai
```

Input with optional user-provided email:

```text
DisplayName: Simon Koudijs
Email:       simon@example.com
```

Output:

```text
Username:    demo:simon-koudijs
DisplayName: Simon Koudijs
Email:       simon@example.com
```

Slug rules — use `github.com/gosimple/slug` so non-ASCII display names
(`佐藤`, `Łukasz`, `Müller`) transliterate to a valid slug instead of collapsing
to empty. The library wraps `go-unidecode` and produces lowercased,
dash-separated ASCII out of the box.

- call `slug.MakeLang(displayName, "en")`
- cap the result at `demoSlugMaxLen` (e.g. 40 characters), then
  `strings.TrimRight(s, "-")` so we don't end on a separator after truncation
- if the result is empty after transliteration, reject the identity — do not
  silently fall back to the service account

Display name rules — keep the human-readable form but make it Git-author safe.
ConfigButler may sanitize again downstream; we reject up front so the user gets
a clear `400` instead of a surprise commit-author rewrite:

- trim whitespace; reject if empty
- reject if it contains `<`, `>`, `\n`, or `\r` (these break a Git author
  signature line)
- cap to a reasonable length (e.g. 64 characters)

Email rules — use `net/mail.ParseAddress` from the standard library, plus a
defensive char filter. We deliberately reject the `Name <addr@host>` form
because we only want a bare address here:

```go
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

When the user provides no email, synthesize `slug@CONFIGBUTLER_DEMO_EMAIL_DOMAIN`.

Example helper shape:

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

func audienceIdentityFromSession(nickname, optionalEmail, emailDomain string) (audienceIdentity, error) {
    displayName, err := normalizeDisplayName(nickname)
    if err != nil {
        return audienceIdentity{}, err
    }

    s := slug.MakeLang(displayName, "en")
    if len(s) > demoSlugMaxLen {
        s = strings.TrimRight(s[:demoSlugMaxLen], "-")
    }
    if s == "" {
        return audienceIdentity{}, errors.New("nickname cannot be converted to a demo identity")
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
        DisplayName: displayName,
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
```

## Kubernetes Client Changes

Rename the kube write path inputs so they no longer accept raw display names as
`actor`.

Preferred shape:

```go
patchCoffeeConfig(ctx context.Context, patch []byte, identity audienceIdentity) (coffeeConfig, error)
createCommitRequest(ctx context.Context, params createCommitRequestParams) (string, error)
```

`createCommitRequestParams` should carry the same identity:

```go
type createCommitRequestParams struct {
    Identity      audienceIdentity
    GitTargetName string
    Namespace     string
    Message       string
}
```

The impersonated client builder should fail if `identity.Username` is empty:

```go
func (c kubeClient) impersonatedDynamic(identity audienceIdentity, includeExtras bool) (dynamic.Interface, error) {
    if c.restConfig == nil {
        return nil, errors.New("rest config unavailable for impersonation")
    }
    if strings.TrimSpace(identity.Username) == "" {
        return nil, errors.New("missing impersonation username")
    }

    cfg := rest.CopyConfig(c.restConfig)
    cfg.Impersonate = rest.ImpersonationConfig{
        UserName: identity.Username,
        Groups:   []string{audienceGroupName},
    }
    if includeExtras {
        cfg.Impersonate.Extra = map[string][]string{
            configButlerDisplayNameExtraKey: {identity.DisplayName},
            configButlerEmailExtraKey:       {identity.Email},
        }
    }
    return dynamic.NewForConfig(cfg)
}
```

Both the CoffeeConfig patch and CommitRequest create must use the exact same
impersonated identity. ConfigButler binds the finalize signal to the open
window by effective audit user plus GitTarget.

## Handler Flow

In `PATCH /public/admin/coffeeconfig`:

1. Load the session.
2. Derive `audienceIdentity` from `session.Nickname` plus optional
   `session.Email`.
3. If identity derivation fails, return `400 Bad Request`.
4. Read current `CoffeeConfig`.
5. Patch `CoffeeConfig` using the impersonated identity.
6. Create `CommitRequest` using the same impersonated identity.
7. Record local change history using the display nickname.
8. Return updated config.

Sketch:

```go
session, _ := getBrowserSession(r)
identity, err := audienceIdentityFromSession(session.Nickname, session.Email, deps.cfg.ConfigButlerDemoEmailDomain)
if err != nil {
    http.Error(w, "invalid session identity", http.StatusBadRequest)
    return
}

updated, err := deps.kube.patchCoffeeConfig(ctx, patchBody, identity)
if err != nil {
    writeKubeError(w, err)
    return
}

if target := strings.TrimSpace(deps.cfg.ConfigButlerGitTargetName); target != "" {
    _, err := deps.kube.createCommitRequest(ctx, createCommitRequestParams{
        Identity:      identity,
        GitTargetName: target,
        Namespace:     deps.cfg.ConfigButlerCommitRequestNamespace,
        Message:       reason,
    })
    if err != nil {
        log.Printf("commitrequest: create failed user=%q display=%q target=%q: %v",
            identity.Username, identity.DisplayName, target, err)
    }
}
```

## RBAC Changes

Keep the group restriction:

```yaml
- apiGroups: [""]
  resources: ["groups"]
  verbs: ["impersonate"]
  resourceNames: ["voter-audience"]
```

Add permission to set the ConfigButler user extras:

```yaml
- apiGroups: ["authentication.k8s.io"]
  resources:
    - "userextras/configbutler.ai/claims/display-name"
    - "userextras/configbutler.ai/claims/email"
  verbs: ["impersonate"]
```

The user impersonation rule still needs attention:

```yaml
- apiGroups: [""]
  resources: ["users"]
  verbs: ["impersonate"]
```

For the demo, using `demo:<slug>` prevents participant input from colliding with
normal user names. Longer term, prefer a stronger RBAC constraint if the
participant set is known:

```yaml
resourceNames:
  - demo:alice
  - demo:bob
```

If the attendee set is not known, keep the prefix rule in code and make sure
there are no cluster RBAC bindings to broad `demo:*` user names.

## Tests To Add

Identity derivation:

- `Simon Koudijs` -> `demo:simon-koudijs`
- `system:masters` -> `demo:system-masters`
- generated display name such as `Demo Guest 42` produces a valid identity
- Unicode names transliterate via `gosimple/slug` and never collapse to empty:
  - `Łukasz` -> `demo:lukasz`
  - `Müller` -> `demo:muller`
  - `佐藤` -> non-empty ASCII slug (assert non-empty + `demo:` prefix; do not
    pin the exact transliteration since `go-unidecode` tables can change)
- display name longer than `demoDisplayNameMaxLen` is rejected
- whitespace-only nickname is rejected
- punctuation-only nickname is rejected (slug collapses to empty)
- display name containing `<`, `>`, `\n`, or `\r` is rejected
- blank email becomes `slug@CONFIGBUTLER_DEMO_EMAIL_DOMAIN`
- valid user-provided email is used as the author email
- invalid user-provided email is rejected or cleared before login completes
- `Foo <a@b.com>` form is rejected (only bare addresses accepted)
- email containing `<`, `>`, `\n`, or `\r` is rejected

Kubernetes wire tests:

- CoffeeConfig patch includes `Impersonate-User: demo:simon-koudijs`
- CoffeeConfig patch includes only `Impersonate-Group: voter-audience`
- CoffeeConfig patch includes display-name extra
- CoffeeConfig patch includes email extra
- CommitRequest create uses the same impersonation headers and extras
- missing identity returns an error and does not issue an HTTP request

Handler tests:

- admin patch derives identity from session nickname
- local change history still records the display nickname
- CommitRequest receives the same derived identity
- invalid session identity fails before patch
- CommitRequest failure still returns `200 OK` after successful patch

Security regression tests:

- a raw `system:` nickname never causes service-account fallback
- no user-initiated write happens without impersonation

## Manual Verification

After deploying:

1. Save as `Simon Koudijs`.
2. Check Kubernetes audit event for the CoffeeConfig patch:
   - `impersonatedUser.username` is `demo:simon-koudijs`
   - `impersonatedUser.groups` includes `voter-audience`
   - `impersonatedUser.extra["configbutler.ai/claims/display-name"]` is
     `Simon Koudijs`
   - `impersonatedUser.extra["configbutler.ai/claims/email"]` is
     `simon-koudijs@demo.configbutler.ai`
3. Check the CommitRequest create audit event has the same effective user and
   extras.
4. Check the resulting ConfigButler Git commit:
   - author name is `Simon Koudijs`
- author email is `simon-koudijs@demo.configbutler.ai`
- commit message matches the save message when supplied
5. Save again with an optional email such as `simon@example.com`:
   - `impersonatedUser.extra["configbutler.ai/claims/email"]` is
     `simon@example.com`
   - the Git author email is `simon@example.com`
6. Try a nickname such as `system:masters`:
   - effective username should be `demo:system-masters`
   - it must not fall back to `system:serviceaccount:voter:auth-service`

## Rollout Order

1. Add identity derivation helpers and tests.
2. Change kube client APIs to accept derived identity instead of raw actor.
3. Add ConfigButler extras to `rest.ImpersonationConfig`.
4. Add RBAC for `authentication.k8s.io/userextras/...`.
5. Update handler to derive identity once and pass it to both write calls.
6. Remove or reduce service-account fallback write permissions where practical.
7. Add optional patch-path allowlist for sensitive CoffeeConfig fields.
8. Update `docs/user-impersonation.md` so integrating parties know which
   identity, RBAC, audit-extra, and fail-closed behavior they must adjust.
9. Deploy and verify audit logs plus Git author output.

## Documentation Follow-Up

When this is implemented, update `docs/user-impersonation.md` as part of the
same change. That document is the integration guide, so it must not keep
teaching the older raw-nickname impersonation pattern.

Spell out the required changes for an integrating party:

- derive a non-colliding Kubernetes username such as `demo:<slug>` instead of
  using a raw display name or email as `Impersonate-User`
- keep the human display name separate from the Kubernetes username
- make email optional at login; use a synthetic valid email if omitted
- optionally generate a default display name so participants can join without
  typing personal details
- provide ConfigButler-compatible impersonation extras:
  - `configbutler.ai/claims/display-name`
  - `configbutler.ai/claims/email`
- add RBAC for `authentication.k8s.io/userextras/...`
- keep the impersonated group pinned with `resourceNames`
- avoid service-account fallback for user-initiated writes
- use the same derived identity and extras for both the watched resource write
  and the `CommitRequest` create
- verify the audit event and resulting Git commit author after deployment

## Decision Summary

Use this split:

- display name stays human: `Simon Koudijs`
- display name may be user-provided or generated, for example `Demo Guest 42`
- email is optional
- Kubernetes username is synthetic: `demo:simon-koudijs`
- ConfigButler author email is user-provided when supplied, otherwise
  synthetic: `simon-koudijs@demo.configbutler.ai`
- ConfigButler receives name/email through Kubernetes impersonation extras
- invalid identity never falls back to the service account

This keeps the demo simple while significantly reducing the chance that a
participant-controlled nickname turns into unexpected Kubernetes authority.
