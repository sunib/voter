# Cross-host handoff protocol

The join host and issuer host have separate Secure, HttpOnly, SameSite=Lax, host-only
cookies. HTTPS origins and Dex upstream are deployment configuration, never accepted
from forwarding headers. The public issuer is mounted at `/` with the connector ID from `CONNECTOR_ID`,
which is `room-pass` in this deployment. The connector ID is also its callback path,
so the two below follow it.

1. Dex redirects a real authorization request to `/callback/room-pass?state=...`. Room Pass
   bounds the state and creates a random, three-minute transaction. It saves Dex state
   separately from application OAuth state, sets `__Host-rp-browser` on the issuer
   host, and redirects to the join host's `/bind?handoff=...`.
2. `/bind` sets/reuses `__Host-rp-join-browser`, binds that cookie to the transaction,
   **replaces the handoff handle with new randomness**, and redirects to issuer
   `/room-pass/confirm`. The original handle is gone.
3. `/room-pass/confirm` checks the original issuer cookie, marks the transaction
   confirmed, replaces its handle again and redirects to `/join`. A copied initial
   link cannot complete this proof from another browser. Replacement handles are not
   returned to the sender of the original URL.
4. `/join` checks the confirmed transaction and join-host browser cookie before showing
   or accepting a form. GET supplies an encrypted CSRF cookie and hidden nonce. POST
   requires the exact join Origin and nonce. It validates the room code/name for a new
   enrollment or reuses the UID-bound `__Host-rp-session`. A fresh Kubernetes read
   rechecks eligibility before authorizing the transaction.
5. Room Pass redirects to issuer `/room-pass/complete`. The issuer cookie must match the
   transaction's browser. The transaction is consumed once, and Room/Participant are
   read again. Room Pass proxies directly to the configured Dex upstream at
   `/callback/room-pass?state=<original-Dex-state>` with server-derived identity headers.
   It removes browser cookies and Authorization from this internal assertion.
6. Dex validates its own transaction, completes the application's OAuth flow, and
   issues tokens. Room Pass never exchanges an application authorization code.

Unknown, expired, reused, unconfirmed and mismatched handoffs fail closed. The handles
are cryptographically random (256 bits), bounded in memory, and absent from logs.
A `same-origin` referrer policy and no-store headers bound referrer and cache
propagation. It is deliberately not `no-referrer`: under that policy a browser
serialises the Origin of a form POST as `null`, which the CSRF check rejected.
Concurrent unfinished logins in different tabs may invalidate the issuer binding cookie;
restart that login. Already-enrolled identities are unaffected.

`/callback` and all other callback suffixes are denied. Every public request has
`X-Remote-*`, `Impersonate-*`, `Forwarded` and `X-Forwarded-*` headers removed before
routing. Only a small allowlist of Dex protocol/static paths is proxied; new Dex releases
must be tested before expanding it. NetworkPolicy and the absence of a direct Dex
Ingress enforce the upstream boundary against ordinary pods.

Application return URLs are exact matches in both Room spec and the platform allowlist.
Dex redirect URIs are separately registered OAuth client configuration. None of these
URLs is inferred from a client Host/forwarded header.

Room Pass sessions refer to both Kubernetes UIDs, participant name and absolute expiry.
A cookie proves possession of enrollment; a public participant ID or listed CR cannot
be used as a credential. Missing/deleting/revoked participants, a replaced Room UID,
stopped/expired Rooms, expired cookies or unavailable Kubernetes all deny assertion.
