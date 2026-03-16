# Alternative: Nuxt 3 as the auth layer (pros and cons)

Context: chosen direct-to-Kubernetes pattern in [`ARCHITECTURE.md`](../../ARCHITECTURE.md:1) and chosen frontend direction in [`FRONTEND.md`](../../FRONTEND.md:1).

## 1. Key clarification

Using Nuxt 3 to “authenticate” can reduce the need for a *separate* tiny auth helper service, but it does not remove the need for **some server-side component** if you want:

- validation of the join code
- minting `TokenRequest`-based BoundServiceAccountTokens
- injecting `Authorization` toward the Kubernetes API server

Nuxt server routes (Nitro) can *be* that component.

So the decision is really:

- Separate minimal auth helper service behind Traefik
vs
- Nuxt 3 app running server-side routes that implement the auth helper responsibilities

## 2. Option A: keep the small forward-auth helper + Traefik (chosen)

Recap from [`ARCHITECTURE.md`](../../ARCHITECTURE.md:1):

- Traefik terminates TLS and rejects obvious garbage quickly.
- `forwardAuth` calls auth helper.
- Helper validates join code and returns an injected `Authorization` header for upstream.

## 3. Option B: Nuxt 3 server routes as the helper (and possibly as the frontend server)

Nuxt 3 provides:

- a Vue frontend
- a Node-based server runtime (Nitro) for server routes

Two sub-variants:

### B1. Nuxt only provides token exchange endpoints

- You keep the public Kubernetes API endpoint behind Traefik.
- Nuxt exposes something like `POST /api/join` that returns a token.
- Browser then calls Kubernetes API via Traefik using that token.

This is no longer “Traefik injects token”; it becomes “browser holds token”. That changes risk.

### B2. Nuxt acts as forwardAuth responder

- Traefik uses `forwardAuth` to call a Nuxt route.
- Nuxt route returns headers for Traefik to forward to Kubernetes.

This preserves the “token never reaches the browser” property.

## 4. Pros of using Nuxt 3 for auth duties

### 4.1 One deployable unit (often)

- Single codebase for UI + auth endpoints.
- Fewer moving parts than UI + separate helper.

### 4.2 Great DX for Vue teams

- Co-locate join flow UX and server-side join/token logic.
- Easier local dev: one process can serve pages + `/api/*` routes.

### 4.3 Server-side capabilities you might use later

- If you later decide to compute aggregates server-side, Nuxt server routes can do that.
- If you later decide to add “presenter-only” features, SSR and server-side endpoints are convenient.

### 4.4 You can still keep Traefik as the fast rejection layer

- Traefik stays the edge gate.
- Nuxt can be behind Traefik, not directly internet-exposed.

## 5. Cons / risks of using Nuxt 3 for auth duties

### 5.1 It is still a backend (bigger blast radius)

- The “tiny helper” can be extremely small and auditable.
- A Nuxt server app has a larger dependency tree and runtime surface.

If your goal is “throw out untrusted traffic quickly”, you still do that with Traefik, but your second line becomes a bigger target.

### 5.2 Token handling choices can accidentally get worse

If you choose variant B1 (Nuxt returns token to browser):

- The token becomes a browser credential.
- XSS or local storage mistakes become much more severe.
- Users can replay tokens outside the intended UX.

Variant B2 (Nuxt used by forwardAuth) avoids this but requires careful Traefik configuration.

### 5.3 Operational scaling and resilience

- Nuxt server-side needs Node runtime resources.
- Under load, SSR (if enabled) can amplify CPU use.
- For a talk, you want very predictable performance.

Mitigation: disable SSR for attendee paths or serve static assets; keep server routes lightweight.

### 5.4 Harder to keep the “Kubernetes is the API” story pure

With Nuxt server routes, you risk drifting into “we built an API” again.

You can keep the narrative aligned by:

- ensuring the only server logic is code validation and token minting
- ensuring all CRUD for quiz resources still goes to Kubernetes

### 5.5 Security review complexity

- More packages, more updates, more CVEs.
- Need to ensure headers, caching, and error pages never leak secrets.

## 6. Recommendation

If the priority is:

- minimum operational complexity
- Vue-first DX

Then Nuxt 3 can replace the separate auth helper *only if* you use it in the **forwardAuth style** (variant B2), so tokens never reach the browser.

If the priority is:

- smallest possible trusted computing base
- easiest security reasoning

Then keep the dedicated tiny forward-auth helper service (chosen in [`ARCHITECTURE.md`](../../ARCHITECTURE.md:1)) and use Vue 3 + Vite for a static SPA.

## 7. Decision rubric

Choose Nuxt 3 for auth if:

- you want one repo artifact and accept a larger server runtime
- you can keep SSR limited or off
- you can enforce “tokens never leave server side” (forwardAuth injection)

Avoid Nuxt 3 for auth if:

- you want the strongest containment of untrusted traffic
- you want the simplest possible component to audit and lock down
