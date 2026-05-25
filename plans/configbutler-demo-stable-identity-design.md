# ConfigButler Demo Stable-Identity & Zero-Friction Login Design

## Purpose

The voter demo currently asks for a nickname at login and derives everything —
Kubernetes username, Git author name, Git author email — from that single
field. This document proposes a different model:

- a short **stable ID** (e.g. `521541`) drives the Kubernetes username
- a **display name** is human-readable, editable, and only affects the Git
  author line in commits
- an **email** is editable, only affects the Git author email

The goals are:

- a participant can join the demo without typing anything (just press save)
- a participant can personalize either field if they want their name in the
  commit history
- the Kubernetes audit identity stays short, stable, and immune to display-name
  edits or Unicode artifacts
- the integration story stays "lightweight, no OIDC needed"

This builds on top of [configbutler-demo-identity-hardening-plan.md](configbutler-demo-identity-hardening-plan.md),
which already landed the `demo:<slug>` username scheme, fail-closed identity,
and ConfigButler impersonation extras. This proposal swaps `<slug>` for a
random ID and splits the login form.

## Why split identity into three fields

The hardening plan derives all three identity components from the display name:

| Field | Today | Comes from |
|---|---|---|
| K8s username | `demo:simon-koudijs` | slug of display name |
| Git author name | `Simon Koudijs` | display name verbatim |
| Git author email | `simon-koudijs@demo.configbutler.ai` | slug + domain |

This is convenient but conflates three concerns into one field. Practical
consequences:

- **Long display names produce long K8s usernames.** `Anonymous Demo
  Participant 17` becomes `demo:anonymous-demo-participant-17` in every audit
  event.
- **Unicode display names produce surprising slugs.** `佐藤` becomes
  `demo:zuo-teng` via `go-unidecode` — accurate, but not what an integrator
  expects when grepping audit logs.
- **Changing the display name mid-session changes the K8s identity.** If we
  ever let users edit their name from the storefront, the audit trail splits.
- **There's no "skip the form" path.** Today the demo requires typing.

Splitting identity fixes all four. The K8s username becomes a stable ID
generated up front. The display name becomes a free-form, optionally-edited
label. The email becomes whatever the user wants (with a synthetic default).

## Target identity model

```go
type audienceIdentity struct {
    Username    string // "demo:521541" — from stableID, never from displayName
    DisplayName string // "Anonymous 521541" — editable, cosmetic
    Email       string // "521541@demo.configbutler.ai" — editable
}
```

Three sources, three fields. None of them derives from any other after the
session is created.

### stableID rules

- Generated client-side once per browser, persisted in `localStorage`
- 6 ASCII digits, in `[100000, 999999]` — short, predictable shape, fits
  comfortably in a kubectl audit line and a UI badge
- Forms the K8s username directly: `demo:<stableID>`
- Validated server-side as `^[1-9][0-9]{5}$` (no leading zero, no other chars)
- Immutable for the lifetime of the session — display name edits do not touch
  it

Why client-generated and not server-issued:

- No new endpoint, no server-side counter, no DB
- Random IDs already exist in the URL bar of every analytics pixel ever; this
  is the same kind of soft-uniqueness assumption
- Collision risk for a demo audience of ~100 people is ~0.5%
  (birthday-problem) and a collision just means two participants share an
  audit identity for the rest of the demo. Acceptable.
- If collision avoidance ever matters, the server can later add a
  `POST /public/demo-identity` endpoint that reserves an unused ID.

### displayName rules

- Default (frontend-side): `"Anonymous " + stableID`
- User-editable; whatever they submit is what shows up as the Git author name
- Server-side validation only: trim, length cap, reject `<>\n\r`. The server
  does not know or care that the default starts with "Anonymous" — it's just
  a string the user chose.
- Empty/whitespace after trim → reject the request

### email rules

- Default (frontend-side): `<stableID> + "@demo.configbutler.ai"`
- User-editable; validated via `net/mail.ParseAddress` plus `<>\n\r` filter
  (already implemented in [auth-service/identity.go](../auth-service/identity.go))
- Server-side: the frontend always sends a fully-formed email (default or
  user-supplied), so the server only validates. **No server-side synthesis.**
  This lets us delete the `CONFIGBUTLER_DEMO_EMAIL_DOMAIN` env var entirely —
  the domain only exists in one place: the frontend constants module.

## Frontend UX

### Login screen

Today the login screen is one input ("Nickname") plus a code field. The new
screen has:

```
┌────────────────────────────────────────────────────┐
│  Demo access code                                   │
│  [ AB12 ]                                           │
│                                                     │
│  Display name                                       │
│  [ Anonymous 521541              ]                  │
│  Shown as the commit author on your changes.        │
│                                                     │
│  Email                                              │
│  [ 521541@demo.configbutler.ai   ]                  │
│  Used as the commit author email. Replace it with   │
│  your own if you want your real address in Git.     │
│                                                     │
│  [  Join the demo  ]                                │
└────────────────────────────────────────────────────┘
```

Behavior:

1. On mount, frontend reads stableID from `localStorage`; if absent, generates
   one and writes it back.
2. Both inputs are pre-filled with defaults derived from the stableID, using
   the single frontend constants module:

   ```ts
   // src/lib/demoIdentity.ts (or wherever auth/login state lives)
   export const DEMO_EMAIL_DOMAIN = "demo.configbutler.ai";

   export function defaultDisplayName(stableID: string): string {
       return `Anonymous ${stableID}`;
   }
   export function defaultEmail(stableID: string): string {
       return `${stableID}@${DEMO_EMAIL_DOMAIN}`;
   }
   ```

3. The user can press **Join the demo** immediately — no typing required.
4. The user can edit either field. The stableID under the hood does not
   change.

The K8s username (`demo:521541`) is intentionally **not displayed** in the
login screen or anywhere else in the UI — it's an audit-log/kubectl detail,
not part of the participant's experience.

### What changes vs today's flow

| Step | Today | New |
|---|---|---|
| Form fields visible | 2 (code + nickname) | 3 (code + display name + email) |
| Fields pre-filled | 0 | 2 (display name + email) |
| Typing required to join | 1 nickname | 0 (defaults work) |
| K8s username | depends on what they typed | stable, set on first load |

### Session badge

The existing [SessionIdentityBadge.vue](../frontend/src/components/layout/SessionIdentityBadge.vue)
shows the nickname. After the change, it shows the display name. The K8s
username stays hidden from the UI on purpose — participants don't need to
think about `demo:521541`, only about the commit author they'll see in Git.

### Logout / re-login

Two options, pick one:

- **Wipe stableID on logout.** Fresh K8s identity on the next login. Cleanest
  "anonymous each visit" semantics. **Recommended.**
- **Keep stableID across logouts.** Same K8s identity if the user logs back in
  on the same browser. Useful if you want a returning participant to thread
  their commits back to the same audit user.

A third option — `localStorage` survives logout but a "Forget me" button wipes
it — is the most flexible, but it adds UI surface for negligible payoff in a
demo.

### UX copy

The default display name shouldn't feel sneaky. Some options:

```text
Anonymous 521541      ← matches the user's request
Demo Guest 521541
Demo Visitor 521541
TestNet Guest 521541
```

`Anonymous person` is honest about what's happening (you haven't picked a
name) without being unfriendly. The number reads as an identifier, not a
ranking. The same number appearing in the email reinforces the connection.

## API changes

### POST /public/login

Request shape:

```json
{
  "code": "AB12",
  "stableId": "521541",
  "displayName": "Anonymous 521541",
  "email": "521541@demo.configbutler.ai"
}
```

- `code`: required, as today
- `stableId`: required, validated as `^[1-9][0-9]{5}$`
- `displayName`: required after trim (reject empty); validation per
  [hardening plan rules](configbutler-demo-identity-hardening-plan.md#identity-derivation-rules)
- `email`: required, validated. The frontend always sends one (synthetic
  default or user-edited), so the server never has to fall back. This is what
  lets us delete `CONFIGBUTLER_DEMO_EMAIL_DOMAIN` from the backend.

Backward-compat note: the existing field is `nickname`. Two options:

- **Rename `nickname` → `displayName` in the JSON.** Clearer, but breaks any
  external integrator currently posting `{nickname: "..."}`.
- **Accept both `nickname` (deprecated) and `displayName`.** Server prefers
  `displayName` if both are present. Lower risk.

The voter demo has no public API contract outside itself, so either is fine —
go with the rename since this is the moment to do it.

### Session payload

Today's session is `{nickname}`. Becomes:

```json
{
  "stableId": "521541",
  "displayName": "Anonymous 521541",
  "email": "521541@demo.configbutler.ai"
}
```

Existing cookies become invalid on schema change — for a demo, just invalidate
on deploy rather than building a migration shim.

### GET /public/admin/session

Today returns `{nickname}`. Should expand to:

```json
{
  "stableId": "521541",
  "displayName": "Anonymous 521541",
  "email": "521541@demo.configbutler.ai"
}
```

The K8s username is intentionally **not** returned — the UI doesn't show it,
and adding it to the wire would invite some component to render it
"helpfully". If a debug screen ever needs it, derive `demo:<stableId>` on the
spot rather than serving a separate field.

## Backend changes

### identity.go

The `audienceIdentity` struct stays as-is. The derivation function gets
simpler — the server no longer synthesizes anything, it just validates:

```go
// Before (current code):
func audienceIdentityFromSession(nickname, optionalEmail, emailDomain string) (audienceIdentity, error)

// After:
func audienceIdentityFromSession(stableID, displayName, email string) (audienceIdentity, error)
```

Rough shape:

```go
func audienceIdentityFromSession(stableID, displayName, email string) (audienceIdentity, error) {
    if err := validateStableID(stableID); err != nil {
        return audienceIdentity{}, err
    }
    name, err := normalizeDisplayName(displayName)
    if err != nil {
        return audienceIdentity{}, err
    }
    if err := validateAuthorEmail(strings.TrimSpace(email)); err != nil {
        return audienceIdentity{}, err
    }

    return audienceIdentity{
        Username:    demoIdentityUsernamePrefix + stableID,
        DisplayName: name,
        Email:       strings.TrimSpace(email),
    }, nil
}

func validateStableID(raw string) error {
    if !stableIDPattern.MatchString(raw) {
        return errors.New("stableId must be 6 digits, leading digit 1-9")
    }
    return nil
}

var stableIDPattern = regexp.MustCompile(`^[1-9][0-9]{5}$`)
```

Notably:

- **No more slug derivation in the K8s username path.** `gosimple/slug` can be
  dropped from `go.mod` if nothing else uses it.
- **No more `emailDomain` parameter, no more env var.** The frontend owns the
  default; the server only validates what arrives.
- **No more `optionalEmail` — email is required on the wire** (frontend
  always sends one). Removes a code path and simplifies tests.

### Handler

The PATCH handler stays nearly identical to its post-hardening form — the only
difference is that it now passes three fields into the identity helper:

```go
session, _ := getBrowserSession(r)
identity, err := audienceIdentityFromSession(
    session.StableID,
    session.DisplayName,
    session.Email,
)
if err != nil {
    http.Error(w, "invalid session identity: "+err.Error(), http.StatusBadRequest)
    return
}
```

The handler no longer reads `deps.cfg.ConfigButlerDemoEmailDomain` — that
config field gets deleted in this change.

The CommitRequest and patch paths see no change beyond the new field flow into
`audienceIdentity` — the fail-closed contract from the hardening plan still
holds.

## Worked examples

### Zero-typing flow

1. User loads `/login`.
2. Frontend generates stableID `521541`, stores in `localStorage`.
3. Form pre-fills `Anonymous 521541` and `521541@demo.configbutler.ai`.
4. User presses **Join the demo** without touching the fields.
5. Server derives:

   ```text
   K8s username:  demo:521541
   Display name:  Anonymous 521541
   Email:         521541@demo.configbutler.ai
   ```

6. First CoffeeConfig patch produces audit event:

   ```json
   "impersonatedUser": {
     "username": "demo:521541",
     "groups": ["voter-audience"],
     "extra": {
       "configbutler.ai/claims/display-name": ["Anonymous 521541"],
       "configbutler.ai/claims/email":        ["521541@demo.configbutler.ai"]
     }
   }
   ```

7. ConfigButler's Git commit author: `Anonymous 521541 <521541@demo.configbutler.ai>`.

### Personalize-display-name flow

1. User loads `/login`. stableID `521541` is generated.
2. User edits display name to `Simon Koudijs`. Leaves email alone.
3. Submits.
4. Server derives:

   ```text
   K8s username:  demo:521541     (unchanged — stableID-driven)
   Display name:  Simon Koudijs
   Email:         521541@demo.configbutler.ai
   ```

5. Audit log still shows `demo:521541` — easy to grep, doesn't change if Simon
   later edits his name again. Git commit author: `Simon Koudijs <521541@demo.configbutler.ai>`.

### Personalize-everything flow

1. User loads `/login`. stableID `521541`.
2. Edits display name to `Simon Koudijs`, email to `simon@example.com`.
3. Submits.
4. Server derives:

   ```text
   K8s username:  demo:521541
   Display name:  Simon Koudijs
   Email:         simon@example.com
   ```

5. Git commit author: `Simon Koudijs <simon@example.com>`. K8s audit identity
   still `demo:521541`.

## Decision points

These are calls the implementer should make explicitly before coding.

### 1. Empty display name on submit

The hardening plan rejects an empty display name with 400. With pre-filled
defaults, a deliberately-blanked display name probably means "I want the
default" — but it could also mean "I don't want any name attached." Two
options:

- **Reject with 400** (matches current). Forces the user to either type a name
  or restore the default. Predictable.
- **Substitute the default server-side.** Friendlier; the user can't
  accidentally lock themselves out. But then the server needs the default's
  format string, which couples server to UI copy.

**Recommendation: reject.** The frontend pre-fills and disables submit while
the display name is empty — same result, no server-side defaulting.

### 2. stableID persistence

- **`localStorage`** (recommended): ID survives page reload, browser restart.
  Same audit identity across the whole event from one browser.
- **`sessionStorage`**: ID dies on tab close. Maximum anonymity. Less useful
  for the "go check your commit on GitHub" demo moment.
- **None (always fresh on mount)**: Every page load is a new identity. Audit
  log fragments. Probably worse.

**Recommendation: `localStorage`, wiped on explicit logout.**

### 3. Rename `nickname` → `displayName` in the API

Already discussed above. **Recommendation: rename**, because the voter demo
has no external consumers and the hardening plan already touched everything in
this area.

Two earlier open questions are now closed:

- **Default email domain location.** Lives in the frontend constants module
  (`src/lib/demoIdentity.ts`) as a single string literal. No env var, no
  config endpoint. If the demo ever needs a different domain, a frontend
  rebuild is the right place to change it — the backend never needed to know.
- **K8s username in the UI.** Not shown anywhere. It's an audit-log detail,
  not a participant-facing identity. The badge and storefront show the
  display name only.

## Open questions

- **Collisions.** Two participants with the same `stableID` share an audit
  identity. Acceptable for a demo. If you ever want guaranteed uniqueness,
  move generation to a server endpoint with a small in-memory `set` of issued
  IDs (clear on auth-service restart, since collisions across restarts only
  matter if the demo's authority is held for >hours).
- **kubectl path.** A participant who downloads a kubeconfig and runs
  `kubectl apply` directly still hits the API as the `quiz-access` SA, not as
  `demo:521541`. Already documented as a known gap in
  [docs/user-impersonation.md](../docs/user-impersonation.md). Out of scope
  here.
- **Audit-log discoverability.** If many participants spam saves, grepping
  audit logs for `demo:521541` is fine; grepping for the display name is not
  (it can be anything). Worth mentioning in the verification doc.

## Rollout order

1. Update `audienceIdentityFromSession` signature to `(stableID, displayName,
   email)` and rewrite tests in
   [auth-service/identity_test.go](../auth-service/identity_test.go).
2. Add `validateStableID` + regex constant.
3. Update session cookie schema: `Nickname` → `StableID` + `DisplayName` +
   `Email`. Invalidate old sessions on deploy.
4. Update `POST /public/login` request shape: rename `nickname` →
   `displayName`, add required `stableId` and `email`.
5. Update `GET /public/admin/session` response shape (stableId + displayName +
   email; no K8s username).
6. Delete `ConfigButlerDemoEmailDomain` from
   [auth-service/config.go](../auth-service/config.go) and any deployment env
   that sets it.
7. Frontend: add the constants module (`src/lib/demoIdentity.ts` or wherever
   login state lives) with `DEMO_EMAIL_DOMAIN`, `defaultDisplayName`,
   `defaultEmail`, and a 6-digit stableID generator backed by `localStorage`.
8. Frontend: `LoginScreen.vue` — generate/restore stableID, pre-fill both
   inputs, no regenerate button.
9. Frontend: `SessionIdentityBadge.vue` — show display name.
10. Update [docs/user-impersonation.md](../docs/user-impersonation.md) to
    reflect the split-identity pattern (stableID is the canonical K8s
    identity; display name and email are cosmetic Git fields).
11. Drop `github.com/gosimple/slug` from `go.mod` if no other callers need it.
12. Deploy, verify a zero-typing join works end-to-end and produces a Git
    commit with the expected author.

## Tests to add

Identity derivation (replace the existing display-name-driven tests):

- `audienceIdentityFromSession("521541", "Anonymous 521541", "521541@demo.configbutler.ai")`
  → `demo:521541`
- `audienceIdentityFromSession("521541", "Simon Koudijs", "simon@example.com")`
  → `demo:521541` / `Simon Koudijs` / `simon@example.com` (display name does
  NOT change the username)
- `validateStableID` rejects: `"012345"` (leading zero), `"abc"`, `""`,
  `"12345"` (too short), `"1234567"` (too long)
- empty `displayName` rejected; whitespace-only rejected
- unsafe-char display name rejected (`<>\n\r`)
- empty email rejected (frontend must always send one)
- bad email rejected

Handler tests:

- login with bad `stableId` → 400
- login with bad `displayName` → 400
- login with bad/missing `email` → 400
- PATCH with valid session → identity has `demo:<stableID>` regardless of
  display name content

Frontend (manual or component-test):

- Fresh visit pre-fills both fields from the constants module
- Pressing **Join the demo** without edits succeeds
- Editing display name doesn't update the email (and vice versa)
- stableID survives page reload via `localStorage`
- Logout wipes `localStorage.stableId`

## Decision summary

- **stableID** is the K8s identity, generated client-side, persisted in
  `localStorage`, 6 ASCII digits. Never displayed in the GUI.
- **displayName** is the Git author name, editable, defaults to `Anonymous
  <stableID>`.
- **email** is the Git author email, editable, defaults to
  `<stableID>@demo.configbutler.ai`.
- Defaults live in **one place: a frontend constants module**. The backend
  has no email-domain config, no fallback synthesis — it only validates.
- The K8s username does **not** derive from the display name anymore — so it
  can no longer change when the user edits their name, and no longer carries
  Unicode-transliteration artifacts.
- The login form has two pre-filled fields plus the access code; the
  zero-typing path works on the first visit.
- This proposal supersedes the slug-from-display-name part of
  [configbutler-demo-identity-hardening-plan.md](configbutler-demo-identity-hardening-plan.md);
  the fail-closed, group-restricted, extras-bearing parts of that plan stay.
