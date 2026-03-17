# Design: Audience kubectl Access

## Context

The browser frontend currently reaches the Kubernetes API through Traefik's ForwardAuth middleware — tokens are minted server-side by the auth-service and never reach the browser. The audience can already *use* the Kubernetes API indirectly.

The goal here is to give audience members direct `kubectl` access so they can introspect the same CRDs (QuizSession, QuizSubmission) from their own terminal, live during the talk. This turns the demo from "trust me, it's Kubernetes" into "try it yourself".

---

## Prerequisite: Ingress Coverage

The current Traefik IngressRoute only covers `/apis/examples.configbutler.ai/`.

kubectl discovery calls:
- `/api` and `/apis` — API group enumeration
- `/apis/examples.configbutler.ai/` — our group ✅
- `/apis/examples.configbutler.ai/v1alpha1/` — resources in group ✅

Without the top-level `/api` and `/apis` paths being proxied, `kubectl get quizsessions` will fail with a discovery error. There are two ways around this:

### N1 — Expand the Ingress (Recommended for kubectl UX)

Add an IngressRoute rule for `PathPrefix(/apis/)` and `PathPrefix(/api)` pointing at the K8s API server, behind the same `auth-forwarder` middleware.

Pros:
- Standard `kubectl get quizsessions -n voter` just works
- Looks clean in a demo

Cons:
- Slightly larger attack surface (more K8s API surface exposed, but still RBAC-gated)
- The token used is still scoped to `quiz-access` Role, so blast radius is bounded

### N2 — Workaround: Explicit API Group in kubectl

Audience uses:
```
kubectl get quizsessions.examples.configbutler.ai -n voter
```
…with the `--server-side-apply` flag or a patched kubeconfig that sets a custom `extensions` block.

In practice this is unreliable — kubectl still attempts discovery. **Not recommended for a live demo.**

### N3 — Direct K8s API with curl/httpie

Skip kubectl entirely. Audience uses:
```
curl -H "Authorization: Bearer $TOKEN" \
  https://voter.z65.nl/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/quizsessions
```

Works today without any ingress changes. Good for showing raw API calls, but loses the `kubectl` narrative.

---

## Token Distribution Options

Regardless of networking choice, audience members need a bearer token. Options:

---

### Option A — Static Shared Token (Simplest)

**How it works:**

1. Before the talk, mint a long-lived token for the `quiz-access` ServiceAccount:
   ```
   kubectl -n voter create token quiz-access --duration=4h
   ```
2. Bundle this token into a kubeconfig file hosted at a public URL or shown as a QR code.
3. Audience downloads: `curl https://voter.z65.nl/kubeconfig.yaml -o kc.yaml`
   Then: `kubectl --kubeconfig kc.yaml get quizsessions -n voter`

**kubeconfig template:**
```yaml
apiVersion: v1
kind: Config
clusters:
- name: voter
  cluster:
    server: https://voter.z65.nl
    insecure-skip-tls-verify: true   # or embed CA
users:
- name: audience
  user:
    token: <minted-token>
contexts:
- name: voter
  context:
    cluster: voter
    user: audience
    namespace: voter
current-context: voter
```

**Pros:**
- Zero extra infrastructure — works with what exists today
- Easy to put on a slide or QR code
- Everyone gets the same experience instantly

**Cons:**
- Single shared token — no per-audience attribution
- Token persists after the talk unless you rotate the ServiceAccount
- Anyone who grabs the token could use it beyond the demo window (mitigated by short `--duration`)
- Cannot revoke individual audience members

**Verdict:** Best choice for a conference talk where speed of setup and reliability matter most.

---

### Option B — Per-Session Token Download (via Auth Service)

**How it works:**

1. Audience member joins the quiz in their browser (gets a valid device session cookie).
2. A new endpoint `/public/kubeconfig` is added to the auth-service.
3. The endpoint validates the session cookie, mints a short-lived token (10 min, the K8s minimum), and returns a ready-to-use kubeconfig.
4. Audience runs:
   ```
   curl -b cookies.txt https://voter.z65.nl/auth/kubeconfig -o kc.yaml
   kubectl --kubeconfig kc.yaml get quizsubmissions -n voter
   ```
   Or provide a shell one-liner shown on the talk slides.

**Auth Service changes needed:**
- New handler: `GET /public/kubeconfig` — requires `requireSessionMiddleware`
- Calls `tokens.GetOrRefresh()` (already exists) to get a short-lived token
- Returns kubeconfig YAML as `application/x-yaml` with `Content-Disposition: attachment`

**Pros:**
- Token is tied to their existing device session
- Same 10-minute TTL as browser — expires naturally
- Demonstrates the full auth flow as part of the talk narrative
- Still no token ever visible in the browser

**Cons:**
- Requires cookie-aware curl (most audiences won't have their browser cookies accessible from terminal)
- Two-step flow: browser join → terminal download — adds friction
- Need to implement the new endpoint

**Verdict:** Architecturally elegant and great for the talk narrative, but the cookie-bridge between browser and terminal is awkward in practice. Good if you can simplify the UX (e.g. a "Download kubeconfig" button in the browser UI that returns the file directly).

---

### Option C — Join-Code Token Exchange (No Cookie Required)

**How it works:**

1. Add a new endpoint: `GET /public/token?code=<join-code>`
2. Validates the join code (same logic as the browser join flow)
3. Returns a JSON response:
   ```json
   { "token": "<short-lived-token>", "expires_in": 600 }
   ```
4. Audience can use this directly or pipe into a kubeconfig:
   ```sh
   TOKEN=$(curl -s "https://voter.z65.nl/auth/token?code=AB3X" | jq -r .token)
   kubectl --token=$TOKEN --server=https://voter.z65.nl get quizsessions -n voter
   ```
   Or as an env var:
   ```sh
   export KUBECONFIG=<(curl -s ".../auth/kubeconfig?code=AB3X")
   ```

**Join code display:** The rotating code is already logged and shown in the talk, so audience can see it on screen.

**Pros:**
- No browser needed — pure terminal flow
- Join code is already a first-class concept in the talk narrative
- Short-lived tokens, same as browser path
- Shows the join code as a multi-purpose credential

**Cons:**
- Exposes token in terminal history / curl output (less than ideal, but acceptable for a conference demo)
- Join code rotation (every 15s) means audience needs to move fast
- Rate limiting on the code endpoint is important (already exists on join flow)
- Need to implement the new endpoint

**Verdict:** Best fit for a terminal-first demo moment. The join code is already displayed, so this extends it naturally. The `export KUBECONFIG=<(...)` one-liner is a memorable demo moment.

---

### Option D — kubectl Credential Plugin

**How it works:**

A small binary (`kubectl-voter-auth`) is distributed (e.g. via a GitHub release) and acts as a [kubectl exec credential plugin](https://kubernetes.io/docs/reference/access-authn-authz/authentication/#client-go-credential-plugins). It calls Option B or C internally to get a fresh token, handles refresh automatically.

**Pros:**
- `kubectl get quizsessions` just works, tokens refresh transparently
- Most "production-like" demo

**Cons:**
- Requires audience to install a binary before the talk — unrealistic for a live conference slot
- Significant implementation effort

**Verdict:** Cool but impractical for a live conference setting.

---

## Recommendation

| Goal | Recommended Option |
|------|-------------------|
| Simplest possible, works in 30 seconds | **A** (static shared token, QR code kubeconfig) |
| Best demo narrative, terminal-first | **C** (join-code token exchange) |
| Tied to browser auth flow | **B** (kubeconfig download endpoint) |
| Production-like for a workshop (not a talk) | **D** (credential plugin) |

**For KubeConEU 2026:**

Use **Option A** as the baseline with **Option C** as the highlight moment:

1. Pre-generate a short-duration kubeconfig (Option A) and put it on a slide/QR code — this is the fallback that always works.
2. During the talk, demo Option C live: show the rotating join code on screen, then run `export KUBECONFIG=<(curl ...)` in your terminal to show that the same join code that your phone uses to vote is also how your terminal gets credentials.

This requires:
1. **Ingress**: Add `PathPrefix(/api)` and `PathPrefix(/apis/)` routes to ingress-kubeapi.yaml (with the same `auth-forwarder` middleware).
2. **Auth service**: New `GET /public/token?code=<code>` endpoint that validates the join code and returns a short-lived token + ready-to-use kubeconfig YAML.
3. **Rate limiting**: Ensure the new endpoint is covered by existing rate limiting (it should be, since it goes through Traefik).

---

## Security Considerations

- `quiz-access` RBAC is already minimal (get/list/watch/create on CRDs in `voter` namespace only) — kubectl access is not more powerful than browser access.
- Short token TTL (10 min) limits the blast radius of any leaked token.
- The join code rotating every 15 seconds means a token exchange window is tight.
- No new ServiceAccounts or RBAC changes are needed for Options A–C.
- Consider: after the talk, rotate/delete the `quiz-access` ServiceAccount token secret to invalidate any static tokens from Option A.
