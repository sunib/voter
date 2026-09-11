# The join form's CSP, and the origin that is easy to forget

Found 2026-09-11, while putting Voter into the local e2e fixture. Fixed the same
day. This is written down because the failure is invisible to every test that is
not a real browser, and because the fix has a configuration consequence.

## Does this need attention fast?

**No for the current deployment, yes before anyone changes its topology.**

Today `voter.koudijs.dev` serves both the application and the join form —
Traefik routes `/bind`, `/join`, `/logout` on that host to Room Pass. The
application origin and the join origin are therefore the same origin, the old
header covered it as `'self'` by coincidence, and browser login works. That is
confirmed live.

It becomes urgent the moment anything separates those two hosts: a second
application on the issuer, a staging deployment on its own name, or splitting
the join form onto a dedicated host. Then **every browser login breaks**, and
the CI suite keeps passing, because neither curl nor a Go HTTP client enforces
CSP.

The fix is already in, so the answer is really: nothing to do now, and the trap
is closed for next time.

## What happens

The join POST is not one request. It is the start of a redirect chain, and
**Chromium applies `form-action` to every hop of that chain**, not only to the
form's immediate target:

```
POST /join                the join origin          <- 'self'
 303 /room-pass/complete  the issuer origin        <- IssuerOrigin
 302 /auth/callback       the APPLICATION origin   <- the forgotten one
```

The header used to be:

```
form-action 'self' <IssuerOrigin>
```

Two of the three. The third was covered only when the application happened to
share the join origin.

Put the application on its own host and the browser reports:

```
Sending form data to 'https://demo.roompass.test:18443/join' violates the
following Content Security Policy directive:
"form-action 'self' https://login.roompass.test:18443". The request has been blocked.
```

Note what that message names: the POST to the **join** origin, which is
`'self'` and perfectly allowed. The origin that is actually missing appears
nowhere in the error. The browser blocks the submission up front because it can
already see where the chain ends, and reports the hop the developer is looking
at rather than the hop that is wrong. Expect to lose time here if it recurs.

## The fix

`form-action` is now derived from configuration rather than assumed:

```go
func formActionSources(cfg Config) string  // internal/server/server.go
```

`'self'`, plus the issuer origin, plus **the origin of every entry in
`ALLOWED_RETURN_URLS`** — de-duplicated, origins only, paths stripped.

That list is the right source because it already means exactly the right thing:
the allowed return URLs are the applications Room Pass is willing to send a
participant back to, so their origins are precisely the origins the chain can
legitimately end on. Nothing new to configure, and no way to do half of it —
adding an application is one change, not two that must be kept in step.

## What this cost, and why

Three quarters of an hour, spent believing it was a test bug. The chain of
misreadings is worth recording:

1. The browser stayed on the join form after clicking Continue, so it looked
   like the click had not worked.
2. Room Pass logged `reason=csrf-mismatch`, which is what it genuinely saw: the
   POST never arrived, so the form nonce never matched anything. **An accurate
   log that pointed away from the cause.**
3. A single sign-in passed in isolation, which suggested a concurrency problem
   between the two browser contexts the test opens.

Only capturing the browser console found it. `page.on("console")` should be
standard in these specs; it is the only channel on which CSP speaks.

This is the third failure of the same shape in two days — the
[authenticator YAML](../../PLAN.md) and the k3d hang were the others. Something
fails closed and correctly, and reports a symptom that names the wrong
component. The guards added alongside each fix matter as much as the fixes.

## The deeper point

This is the second CSP-or-header bug in this service that **only a real browser
can see**. The first was `Referrer-Policy: no-referrer`, which made browsers
serialise the form POST's `Origin` as `null` and get rejected, while curl and
the Go e2e client passed happily.

Same shape, same blind spot, and the same conclusion: the browser suite is not a
nice-to-have here. A Go HTTP client cannot reproduce referrer policy, CSP,
cookie attribute enforcement, or redirect-chain security checks — and this
service's correctness depends on all four.

## If you change the topology

- Add the new application's URL to `ALLOWED_RETURN_URLS` **and** to the `Room`'s
  `spec.allowedReturnURLs`. Both are required: `Ready()` refuses to serve if a
  Room names a return URL the platform allowlist does not, so a Room cannot
  widen its own redirect surface. That check fails the readiness probe with a
  bare `503 Storage or configuration unavailable` and does not log which URL was
  the problem — a second thing worth improving.
- Then run the browser suite. Not the Go one.
