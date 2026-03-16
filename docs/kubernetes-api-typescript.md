# TypeScript options for calling the Kubernetes API (from a browser SPA)

This repo’s chosen architecture in [`ARCHITECTURE.md`](../ARCHITECTURE.md:1) means the frontend will call Kubernetes API paths via Traefik, but **Kubernetes bearer tokens never reach the browser**.

That changes what “Kubernetes client library” you actually need in the frontend.

Related canonical frontend doc: [`FRONTEND.md`](../FRONTEND.md:1).

## 0. Decision (for this repo)

For the browser SPA we will **not** use a full Kubernetes client library.

Instead, we will build a **small typed wrapper around** `fetch` for the few endpoints we need (read `QuizSession`, create `QuizSubmission`, presenter polling).

Reason:

- Most Kubernetes JS/TS clients are designed for Node runtimes and kubeconfig-based auth.
- In this repo, Traefik + forwardAuth + cookies provide the auth context, and Kubernetes tokens must not reach the browser.
- A tiny wrapper is easier to reason about, smaller, and sufficient.

## 1. Is direct Kubernetes API usage from a browser unprecedented

It’s uncommon, but not unprecedented.

- Most Kubernetes client libraries are designed for **Node or Go** where you can load kubeconfig, do TLS/mTLS, exec auth plugins, and manage watches robustly.
- Browsers can still call Kubernetes because it is plain HTTP+JSON, but you must handle **CORS**, **cookies**, and **streaming** carefully.

In this repo, Traefik + forwardAuth is doing the heavy lifting, so the browser mostly needs a typed HTTP wrapper around a few endpoints.

## 2. Existing TypeScript Kubernetes clients

### 2.1 `@kubernetes/client-node` (official-ish JS client)

Project: kubernetes-client/javascript

- Strength: mature for Node backends (supports kubeconfig patterns, many API groups, helpers).
- Weakness: **not browser-friendly**. It expects Node runtimes and patterns that bundlers often can’t or shouldn’t ship to the browser.

Recommendation: good choice for the **forward-auth service** (if you implement it in Node/TS), not for the SPA.

### 2.2 Generic OpenAPI-generated client

Kubernetes publishes an OpenAPI spec. You can generate a TypeScript client.

- Strength: type safety and broad endpoint coverage.
- Weakness: huge surface area, can produce a very large client bundle, and you still have to deal with Kubernetes watch semantics.

Recommendation: usually overkill for an MVP quiz tool.

### 2.3 Write a tiny, purpose-built REST wrapper (recommended for the SPA)

Because the SPA only needs a handful of operations:

- read one `QuizSession`
- create one `QuizSubmission`
- presenter polling (optional)

The simplest approach is:

- use `fetch`
- define TypeScript types for your CRDs
- build a small `kubeApi.ts` module with typed functions

This avoids pulling Node-oriented Kubernetes clients into the browser bundle.

## 3. Practical SPA pattern for Kubernetes APIs

### 3.1 URL shapes you will call

For CRDs:

- `GET /apis/<group>/<version>/namespaces/<ns>/<plural>/<name>`
- `POST /apis/<group>/<version>/namespaces/<ns>/<plural>`

For example:

- `GET /apis/examples.configbutler.ai/v1alpha1/namespaces/demo/quizsessions/kubecon-2026`
- `POST /apis/examples.configbutler.ai/v1alpha1/namespaces/demo/quizsubmissions`

### 3.2 Auth handling in this repo

Because tokens must not reach the browser, the SPA should:

- rely on the Secure, HttpOnly device-session cookie set by forwardAuth
- send requests with `credentials: include`

No kubeconfig logic is needed client-side.

### 3.3 CORS and preflight

From the SPA you’ll trigger OPTIONS preflights for `POST` and JSON content.

- Handle CORS at Traefik.
- Ensure `Access-Control-Allow-Credentials: true` if you use cookies.

### 3.4 Watching resources

Kubernetes watch is usually:

- `GET ...?watch=1&resourceVersion=...`

In a browser this becomes a streaming response.

Options:

- MVP: avoid watch; poll periodically.
- Later: implement watch using `fetch` streaming (`ReadableStream`) and parse event lines.

For a talk demo, polling is often the safest choice.

## 4. Recommended approach for this repo

- SPA: tiny typed wrapper over `fetch` (no Kubernetes client library).
- Forward-auth service (if implemented in TS/Node): `@kubernetes/client-node` is a good fit.

This combination keeps the frontend bundle small and keeps Kubernetes credentials and token minting strictly server side.
