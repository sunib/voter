# Joining by QR code

A participant scans one code and lands on a chosen page of the application,
signed in, having typed only a display name.

```
QR  ->  https://voter.koudijs.dev/auth/login?code=BCDFGH&return=/answer/round-1
        |
        |  application stores the destination in its login transaction,
        |  sets __Host-room-pass-joincode, redirects to Dex
        v
        Dex -> /callback/room-pass -> /bind -> /room-pass/confirm -> /join
        |
        |  the cookie arrives with the browser; the code becomes a prefill
        |  and the page asks only for a display name
        v
        enrollment -> /room-pass/complete -> Dex -> application callback
        |
        v
        https://voter.koudijs.dev/answer/round-1
```

## The two halves and why they travel differently

**Where the participant ends up** is the application's business. It goes in
`?return=`, is validated as a local absolute path, and is kept server-side in
the login transaction, keyed by the OAuth state. It never appears in a URL
after that. It deliberately does *not* use this gateway's `return`: that is an
exact-match entry from the Room's immutable `allowedReturnURLs`, which is a
short list of approved origins, not a place to put a deep link.

**The room code** cannot travel in a URL at all. The `/join` URL is built
*here*, after Dex, from a handoff id the application never sees — see
[handoff.md](handoff.md). There is no parameter to thread it through.

So Room Pass publishes a channel for it, described next. It is a Room Pass
feature, not something the Voter demo invented: any application on the join
host can use it, and nothing in it knows what a QR code is.

## The pre-supplied code cookie

> **Contract.** An application sharing the join host may set
> `__Host-room-pass-joincode` to a room code. `/join` will render that code
> into the form instead of asking for it, leaving the participant only a
> display name to fill in. Room Pass owns this name and these rules.

| | |
|---|---|
| **Name** | `__Host-room-pass-joincode` |
| **Value** | The room code. Case, hyphens and surrounding whitespace are normalised, as for a typed code. Uppercase letters and digits, at most 12 — anything else is ignored and the form asks for a code as usual. |
| **Attributes** | `Secure`, `HttpOnly`, `Path=/`, **`SameSite=Lax`** |
| **Lifetime** | The application's choice; a few minutes is ample. Room Pass expires it on use. |
| **Alternative** | `/join?code=…` for a QR pointing straight here, when no application destination is involved. The query parameter wins if both are present. |

The name is spelled out rather than following the internal `__Host-rp-*`
convention, because the other cookies are Room Pass talking to itself while
this one is written by somebody else's code, where `rp` is an abbreviation only
we can expand.

Four properties are load-bearing:

- **Plain text, not signed.** An integrator needs no key material from us, and
  signing would imply the writer vouches for the code. Nobody does until the
  Room says so.
- **`SameSite=Lax`.** The request that needs it is a top-level navigation
  arriving from the issuer's origin. `Strict` drops exactly that request, which
  is the single most likely way to get this wrong.
- **`HttpOnly` and `Secure`,** with the `__Host-` prefix, so script cannot read
  it and it cannot be set for a wider domain.
- **Used once.** `/join` expires it while rendering the form. A code left in
  the jar is one that gets silently replayed on a later join, long after it
  stopped being the code on the screen.

**Same host or nothing.** The cookie is only delivered because the join origin
and the application origin are one host (`JOIN_ORIGIN`; a proxy splits the
paths between two Services). Put the application on its own host and the cookie
never arrives — the flow degrades to typing the code, which is the correct
failure but a confusing one to debug.

## What the cookie is worth

Nothing on its own, which is the point. Anything on this host can write it, so
the design assumes something hostile did.

The value is a prefill. It is rendered into the form, comes back through the
ordinary CSRF-checked POST, and `enrollParticipant` checks it against the Room's
currently valid codes exactly as it checks a typed one. A forged code is an
invalid code. A stale code is an expired code. Both already had answers before
any of this existed, and the QR path did not add a way around them.

`normalizeScannedCode` bounds what may be echoed back — uppercase letters and
digits, at most the CRD's 12 — so a cookie cannot put unbounded junk on the
page. That is a rendering guard, not a validity check; `html/template` escapes
the value either way.

The one property the QR path does change: the code is now in a URL, and URLs
end up in the application's access log and the scanning browser's history. What
leaks is a credential that expires on its own after `validFor` — seconds to a
couple of minutes, depending on the Room. The authorization request sent on to
Dex does **not** carry it; that is asserted by a test.

None of this is the main exposure anyway. The code is on a projector in front
of a room for `validFor` seconds, so anyone who can see the screen — including
a recording — can use it. RBAC and cookie hygiene only decide who can obtain a
code *without being in the room*.

## Showing the QR code

The code rotates every `rotateEvery` and stays valid for `validFor`, so a
*printed* QR can never carry one. It has to be re-rendered on the presenter's
screen as the code turns over:

```
task room-pass:present BASE=https://voter.koudijs.dev NEXT=/answer/round-1
```

That is `cmd/room-qr`, which follows the Room's status and redraws. It runs
under the operator's own kubeconfig, so Kubernetes decides who may see a join
code — the same reason it is not a page in the application, which would need an
operator login of its own built and got right.

Today the only credential that can read it is the cluster's X509 admin cert.
Room Pass's own ServiceAccount has `rooms/status`; no operator identity from
the issuer is bound to it. Running the presenter tool as a `github:` operator
would need a Role granting `get`/`watch` on `rooms` in the Room's namespace,
which is a platform change, not an application one.

A participant who cannot scan is not stuck: the screen prints the code in
speakable groups, and `/join` still offers the code field to anyone who arrives
without one.

## Timing

The CRD defaults — `rotateEvery: 15s` / `validFor: 30s` — are wrong for a QR
flow. A participant who scans just before a rotation gets about fifteen seconds
to think of a display name, and the name is the whole point: it ends up on a Git
commit. The deployed Room therefore overrides them with **`30s` / `120s`**.

`validFor` may be at most **four rotations**. That ceiling is enforced twice,
by a CEL rule on the CRD and again in the controller's `Advance`, because
`status.validJoinCodes` retains at most four entries. So 120s of validity
requires rotating no faster than 30s; at `10s` rotation the ceiling is 40s and
the enum has no 40s, which pins `validFor` back to 30s.

Rotating slower gives nothing up. What a code photographed off the screen is
worth is decided by `validFor`, not `rotateEvery` — at 120s the exposure window
is two minutes whichever rotation is chosen. Rotation only sets how often the
projected QR redraws.

`joinCode` is **immutable per Room**. Changing it means recreating the Room,
which mints a new UID, cascade-deletes every Participant and invalidates every
enrollment cookie. Under GitOps that needs
`kustomize.toolkit.fluxcd.io/force: enabled` on the object, which stays armed
afterwards and will silently wipe the participant list on the next immutable
edit. Settle this before the event, not during it.

## Integrating another application

Against the contract above:

1. Take a code on the endpoint the QR points at — `?code=` is the obvious
   spelling — and bound it to uppercase alphanumerics of at most 12 before it
   goes anywhere near a `Set-Cookie` header. Validity is not your problem; Room
   Pass decides that.
2. Set `__Host-room-pass-joincode` with `Secure`, `HttpOnly`, `Path=/` and
   **`SameSite=Lax`**, then start your normal login redirect.
3. Keep your own post-login destination server-side, keyed by your OAuth state.
   It cannot travel through this gateway.
4. Never log the code, and keep it out of the authorization request you send to
   the issuer.
5. Serve that endpoint from the same host as `JOIN_ORIGIN`.

`test/e2e/demo-client` does all of this in about ten lines and is the shortest
worked example. `test/browser/room-auth.spec.js` drives it through a real
browser, which is the only thing that actually enforces the `SameSite` rule the
design rests on — a Go test will pass with `Strict` and a phone will not.
