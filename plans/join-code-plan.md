# Join Code Resolution and Session Cookie Plan

## Goal
Enable users to join without specifying a session name by resolving `X-Join-Code` to the correct session, storing that session in a cookie, and logging rolling join codes every 15 seconds per open session.

## Current Behavior Summary
- Auth handler requires a session ref in the request URI; missing ref blocks join-code flow: [`auth-service/main.go`](auth-service/main.go:82).
- Join-code validation is per-session and requires session name to validate: [`auth-service/join_codes.go`](auth-service/join_codes.go:33).
- Session ref parsing is URI based: [`auth-service/session_ref.go`](auth-service/session_ref.go:15).

## Proposed Behavior
1. **Join code without session name**
   - Incoming request with `X-Join-Code` and no session name should resolve the session through a join-code index.
   - Once resolved, the session identity is persisted in a session cookie for subsequent requests.

2. **Rolling codes debug output**
   - Every 15 seconds, generate and log current rolling join codes for each open session.
   - “Open” is interpreted as `spec.state == live` from kube: [`auth-service/main.go`](auth-service/main.go:103).

3. **Session info endpoint + frontend display**
   - Provide an auth-service endpoint to return current session info for the resolved session cookie (state, title, time remaining, etc.).
   - Frontend should call this endpoint and surface the data in the UI (e.g., join screen or banner).

## Key Design Decisions
### Join-code indexing
- Update code generator to produce **4-character**, **case-insensitive** codes using digits and `a-z` only.
- Extend join-code store to maintain a reverse index `code -> sessionRef` while keeping per-session code history.
- On rotation, update both per-session list and the reverse index.
- Enforce TTL for both per-session history and reverse index to avoid stale matches.
- Concurrency: single mutex in join-code store should guard all maps to keep updates atomic: [`auth-service/join_codes.go`](auth-service/join_codes.go:16).

### Session authorization storage
- Use an **encrypted/signed session cookie** to keep auth-service stateless.
- Recommend using [`github.com/gorilla/securecookie`](https://pkg.go.dev/github.com/gorilla/securecookie) for best-practice cookie signing/encryption in Go.
- **Key management**: on startup, auth-service checks for Kubernetes Secret `auth-session-cookie-keys` in the same namespace. If missing, generate strong random keys and create the Secret with fields `hashKey` and `blockKey`, then load keys from it. If present, load keys from it.
- Cookie payload should include `namespace/name`, issued-at, expiry, and a version marker.
- Cookie validation should include decryption/signature verification and live session check against kube.

### Background rotation and debug logging
- Add a goroutine ticker with period `JoinCodeRotate` to:
  - List all live sessions via kube.
  - Ensure each live session has a current rolling code.
  - Log the current code per session.
- Log format should be consistent, e.g. `join-code: namespace=<ns> name=<name> code=<code>`.

## Implementation Plan
1. **Update join-code store**
   - Add reverse index map from code to session reference and/or key string.
   - Add a method like `rotateAndGet(sessionRef, now)` to return current code and update indexes.
   - Add a lookup method like `resolve(code, now)` to find session by join code with TTL enforcement.
   - Update pruning to clean expired entries and index values: [`auth-service/join_codes.go`](auth-service/join_codes.go:16).

2. **Add background rotation loop**
   - In startup, launch a goroutine with ticker `cfg.JoinCodeRotate`.
   - For each tick:
     - list sessions via kube client,
     - filter for `live` sessions,
     - call `rotateAndGet` per session,
     - log code for each session.
   - Tie into existing logging patterns: [`auth-service/main.go`](auth-service/main.go:13).

3. **Session cookie key management**
   - On startup, use kube client to check for a Secret in the current namespace.
   - If Secret missing, generate strong random hashKey/blockKey, create Secret, then load keys.
   - If Secret present, load keys for securecookie.

4. **Auth handler changes**
   - If no session ref but `X-Join-Code` present:
     - resolve session via join-code store,
     - set encrypted session identity cookie,
     - continue flow as if the session ref was supplied.
   - If session cookie exists and no join code is provided, use it to establish the session context.
   - Keep device session cookie behavior intact: [`auth-service/http_helpers.go`](auth-service/http_helpers.go:22).

5. **Session info endpoint**
   - Add an endpoint (e.g. `/session-info`) to return the resolved session metadata for the cookie session.
   - Response should include title, state, and time remaining if available (derived from session spec/status fields).
   - This endpoint should validate that the session is still live: [`auth-service/main.go`](auth-service/main.go:75).

6. **Frontend join flow changes**
   - Join screen should send only `X-Join-Code`.
   - Remove any requirement to include session name in request path or headers.
   - Fetch session info after join and display it in the UI (banner or join state): [`frontend/src/screens/JoinScreen.vue`](frontend/src/screens/JoinScreen.vue:1), [`frontend/src/components/session/SessionStateBanner.vue`](frontend/src/components/session/SessionStateBanner.vue:1).
   - Rely on session cookie for subsequent requests: [`frontend/src/stores/session.ts`](frontend/src/stores/session.ts:1).

7. **Configuration and documentation**
   - Add config entry for new session cookie name and TTL, document defaults: [`auth-service/config.go`](auth-service/config.go:9).
   - Update auth-service README with new join flow, logging details, session-info endpoint, and secret creation behavior: [`auth-service/README.md`](auth-service/README.md:1).
   - Update frontend README or join flow docs if applicable: [`frontend/README.md`](frontend/README.md:1).

## Dependencies and Risks
- **Collision risk**: short numeric codes may collide across sessions in the same rotation window. Mitigation: enforce unique codes across live sessions per rotation, retry on collision.
- **State drift**: if kube list fails, join codes may not update; log error and retry next tick.
- **Cookie integrity**: session cookie should be decrypted/verified and validated against kube on each auth request and session-info endpoint to ensure session is still live.

## Acceptance Criteria
- User can join with only `X-Join-Code`, no session name required.
- Resolved session is persisted in cookie and reused on subsequent requests.
- Debug logs show a rolling join code for each live session every 15 seconds.
- Frontend join flow works without session name inputs.

## Next Steps
- Review this plan, then switch to implementation mode to apply changes across auth-service and frontend.
