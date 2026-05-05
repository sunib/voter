# Kubernetes Watch to SSE Gateway

Status: proposed design

This document describes a reusable pattern for exposing Kubernetes-backed live state to browsers without opening one Kubernetes watch per browser tab.

The core idea:

```text
browser
  -> EventSource("/api/watch")
       -> auth-service
            -> shared informer / dynamic informer
                 -> Kubernetes API watch
```

The service should maintain:

- `1` Kubernetes watch per resource type and scope
- `N` browser subscribers over SSE
- local fan-out from a shared cache or event broadcaster

This is the right shape for ConfigButler-style UIs where Kubernetes is the configuration backend and the browser needs live updates.

## 1. Problem statement

The naive implementation is:

- browser opens a watch-like endpoint
- backend opens a fresh Kubernetes watch for that browser
- every browser tab becomes its own upstream watch

That does not scale well:

- it increases load on the Kubernetes API server
- it makes reconnect storms expensive
- it duplicates authorization and watch setup logic
- it makes it harder to reason about watch semantics such as `BOOKMARK`, `ERROR`, reconnect, and resource version handling

What we want instead:

- the browser uses simple `EventSource`
- the backend authenticates and authorizes the caller
- the backend owns Kubernetes watch mechanics
- the backend reuses upstream watches across callers
- the backend fans out normalized events as SSE

## 2. Goals

- Make Kubernetes practical as a live configuration backend for browser UIs.
- Keep browser code simple: `EventSource` or `GET /api/state`.
- Avoid one Kubernetes watch per browser.
- Reuse `client-go` informer machinery instead of handwritten raw watch loops.
- Centralize authorization checks in the auth-service.
- Allow multiple browser subscribers to share the same upstream watch and cache.
- Preserve enough Kubernetes semantics that the UI still feels Kubernetes-native.

## 3. Non-goals

- A raw unrestricted proxy for arbitrary Kubernetes watch URLs.
- Full kubectl parity in the browser.
- Exposing Kubernetes bearer tokens to the browser.
- Supporting every possible watch selector combination in the first cut.

Important constraint:

For safety and cache reuse, the service should accept a normalized watch request, not a free-form upstream URL copied from the browser. A fully generic raw proxy makes authorization, cache reuse, and operational limits much harder.

## 4. High-level architecture

Components:

1. Browser
2. Auth-service watch gateway
3. Shared informer manager
4. SSE broadcaster
5. Kubernetes API server

Flow:

1. Browser opens `GET /api/watch?...`
2. Auth-service authenticates the caller
3. Auth-service authorizes the requested watch scope
4. Auth-service maps the request to a normalized watch key
5. Informer manager reuses or creates a shared informer for that key
6. SSE layer subscribes the browser to the local broadcaster for that key
7. Upstream informer events are normalized and fanned out to all subscribers

## 5. Why informers, not raw per-client watches

The important design choice is using `client-go` informers or dynamic informers.

Why:

- informers already handle list-then-watch
- informers maintain a local cache
- a reflector reuses one upstream watch and keeps local state current
- reconnects and relists are handled in one place
- many browser clients can share the same local store

This is much better than:

- `N` browser clients
- `N` backend watch loops
- `N` upstream Kubernetes watches

## 6. Request model

Recommended browser endpoint:

- `GET /api/watch?resource=coffeeconfigs&namespace=voter&name=testnet-coffee`

Optional later forms:

- `GET /api/watch?gvr=examples.configbutler.ai/v1alpha1/coffeeconfigs&namespace=voter`
- `GET /api/watch?resource=gittargets&namespace=team-a`
- `GET /api/state?...` for an initial snapshot without streaming

Do not start with:

- raw `?url=/apis/...&watch=1...`

That is too unconstrained for a first reusable gateway.

### Normalized watch key

Every accepted request should be normalized into a bounded internal key, for example:

```text
group=examples.configbutler.ai
version=v1alpha1
resource=coffeeconfigs
namespace=voter
name=testnet-coffee
selector=
```

This key is used for:

- authorization
- informer reuse
- subscriber fan-out
- metrics and limits

## 7. Authorization model

Authorization should happen before a subscriber joins a stream.

Two checks are needed:

1. Authenticate the caller
2. Authorize the requested watch scope

Examples:

- attendee session may watch only one `QuizSession`
- admin session may watch one `CoffeeConfig` and order streams
- future ConfigButler user may watch only resources allowed by tenancy rules

Recommended rule:

- do not let the browser describe authorization rules
- let the browser ask for a logical resource scope
- let the backend map that scope to an allowlisted Kubernetes resource

### Suggested interface

```go
type WatchAuthorizer interface {
    AuthorizeWatch(ctx context.Context, principal Principal, req WatchRequest) (AuthorizedWatch, error)
}
```

Where:

- `WatchRequest` is user input
- `AuthorizedWatch` is the normalized and validated internal watch key

This separation matters. It prevents the UI from smuggling extra scope through raw query parameters.

## 8. Informer strategy

### 8.1 Informer manager

The service should keep a registry of active shared informers keyed by normalized watch key.

Conceptually:

```text
watch key -> informer instance + local store + broadcaster + subscriber count
```

When a new subscriber arrives:

1. normalize and authorize request
2. look up informer by key
3. reuse it if already running
4. otherwise create it
5. subscribe browser to local broadcaster

When the last subscriber leaves:

- optionally keep informer warm for a short TTL
- then shut it down if unused

### 8.2 Dynamic informer vs typed informer

For a reusable system, prefer dynamic informers.

Why:

- ConfigButler-like products often watch CRDs, not only built-in resources
- typed informers require generated clients for every resource
- dynamic informers work across arbitrary `GroupVersionResource` combinations

Typed informers are still fine for very stable core resources.

### 8.3 Scope

Start with bounded scopes:

- one resource kind
- one namespace
- optional one object name

This keeps cache cardinality predictable.

Avoid supporting arbitrary combinations of:

- many namespaces
- arbitrary label selectors
- arbitrary field selectors

until the lifecycle and metrics are well understood.

## 9. Event model

The browser should receive SSE events, not raw Kubernetes watch frames.

Recommended SSE event categories:

- `snapshot`
- `upsert`
- `delete`
- `error`
- `heartbeat`

Example:

```text
event: snapshot
data: {"key":"...","items":[...],"resourceVersion":"123"}

event: upsert
data: {"key":"...","object":{...},"resourceVersion":"124"}
```

### Why normalize the events

Raw Kubernetes watch events contain details that are awkward for browsers:

- `BOOKMARK`
- `ERROR`
- metadata-only objects
- relist behavior

The gateway should absorb those mechanics and emit stable browser events.

Recommended rule:

- do not forward `BOOKMARK` as a real object update
- treat metadata-only watch bookkeeping as internal implementation detail

## 10. Initial state and replay

Browsers usually need current state before deltas.

There are two good options:

### Option A: snapshot-first SSE

On subscribe:

1. send a `snapshot` event from the local cache
2. continue with incremental events

This is usually the simplest browser contract.

### Option B: separate state endpoint

- `GET /api/state?...`
- `GET /api/watch?...`

This can be useful if you want snapshot fetches to stay cacheable or debuggable independently.

For this repo, snapshot-first SSE is probably the simplest shape.

## 11. Fan-out and subscriber management

Each normalized watch key should have one broadcaster.

Broadcaster responsibilities:

- register subscribers
- unregister subscribers
- send current snapshot on join
- fan out normalized events
- drop or disconnect slow subscribers
- keep per-subscriber buffers bounded

Important rule:

- one slow browser must not block the informer event pipeline

Recommended behavior:

- each subscriber gets a bounded channel
- if the channel stays full, disconnect the subscriber
- browser reconnects automatically via `EventSource`

## 12. Operational safety

This gateway exists partly to protect the Kubernetes API server.

Recommended limits:

- max number of distinct active watch keys
- max subscribers per key
- max total subscribers
- idle informer TTL when no subscribers remain
- per-principal and per-IP limits on stream creation

Recommended observability:

- active informers
- active subscribers
- informer relist count
- upstream watch restarts
- dropped subscribers
- authorization denials
- per-key fan-out counts

## 13. Failure handling

Expected failures:

- auth expires while browser is connected
- informer relist fails temporarily
- Kubernetes API server disconnects watches
- browser reconnect storms

Recommended behavior:

- authorization is checked on connect
- optionally re-check auth on long-lived streams if your security model needs it
- emit an SSE `error` event for recoverable backend issues when useful
- rely on `EventSource` reconnect for disconnected browsers
- keep reconnect cheap by reusing the informer and local cache

## 14. API shape

Suggested endpoints:

- `GET /api/watch?...`
- `GET /api/state?...`

Optional admin/debug endpoints later:

- `GET /debug/watch-keys`
- `GET /debug/watch-metrics`

Suggested internal packages:

```text
watcher/
  authorization and watch request normalization
  informer manager
  dynamic informer lifecycle

stream/
  SSE framing
  broadcaster
  subscriber lifecycle

api/
  HTTP handlers
  request parsing
  auth integration
```

## 15. Proposed internal interfaces

Illustrative shape:

```go
type WatchRequest struct {
    Group     string
    Version   string
    Resource  string
    Namespace string
    Name      string
}

type WatchKey struct {
    Group     string
    Version   string
    Resource  string
    Namespace string
    Name      string
}

type InformerManager interface {
    Subscribe(ctx context.Context, key WatchKey) (Subscription, error)
}

type Subscription interface {
    Snapshot() WatchSnapshot
    Events() <-chan WatchEvent
    Close()
}
```

The exact types can change. The important part is the separation of concerns:

- API layer parses browser requests
- auth layer decides if the request is allowed
- watcher layer owns Kubernetes mechanics
- stream layer owns SSE fan-out

## 16. Security posture

This design is safer than direct browser watches because:

- Kubernetes credentials stay server-side
- watch authorization is centralized
- raw Kubernetes watch behavior is not exposed directly
- upstream watch count is bounded
- operational limits are enforceable in one service

But it still needs care:

- do not make `/api/watch` a universal arbitrary-cluster read API
- keep the allowed resources explicit
- keep scopes narrow
- treat stream creation as a resource that needs rate limiting

## 17. Fit for this repo

For the current demo codebase, this pattern fits especially well for:

- `CoffeeConfig`
- live order events
- future ConfigButler-style resource views

Recommended first adoption path:

1. keep the existing SSE browser contract
2. replace ad hoc per-request Kubernetes watches with a shared watcher manager
3. start with one or two allowlisted resources
4. emit snapshot-first SSE
5. add metrics before broadening the supported watch surface

## 18. Why this matters

The point of this design is not only scaling.

It makes Kubernetes much easier to use as a configuration backend for web UIs:

- the browser gets a simple event stream
- the backend absorbs watch complexity
- the Kubernetes API server is protected from per-tab watch explosion
- live configuration UIs can stay close to real Kubernetes state without inventing a separate database

That is a strong fit for ConfigButler, reverse GitOps UIs, and demo systems where Kubernetes is the source of truth.
