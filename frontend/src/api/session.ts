// The application session, as the backend reports it.
//
// One endpoint, one shape. The old code had two parallel notions of "who am I"
// -- a legacy /public/session built from a browser-asserted identity, and this
// one -- and the router guard consulted the wrong one, which is why a
// successful OIDC login still bounced you to a login screen.

export interface Session {
  authenticated: boolean
  username: string
  displayName: string
  email: string
  groups: string[]
  csrfToken: string
  expiresAt: number
  /** Where this application's objects live. The SPA needs it to address the
   *  live stream, and getting it from the server means the browser never has
   *  to guess (or be told by a URL). */
  namespace: string
  coffeeConfigName: string
}

// The CSRF token the backend issued for this session.
//
// It is deliberately cached here rather than fetched per mutation: /auth/session
// is the ONLY place that hands it out, and re-fetching before every write would
// turn one save into two round trips. Every getSession() refreshes it, and the
// router guard calls getSession() before each protected navigation, so it is
// current for as long as the session is.
let csrfToken = ''
let lastSession: Session | null = null

/** The most recent session, for callers that need the namespace or object name
 *  without re-fetching. Null when signed out. */
export function currentSession(): Session | null {
  return lastSession
}

/** The CSRF token for the current session, or "" when signed out. Mutating
 *  requests must send it as `x-csrf-token`; the backend rejects them otherwise. */
export function currentCsrfToken(): string {
  return csrfToken
}

/** Returns null when there is no session, rather than throwing: "not logged
 *  in" is an expected answer here, not an error. */
export async function getSession(): Promise<Session | null> {
  const res = await fetch('/auth/session', {
    credentials: 'include',
    headers: { accept: 'application/json' },
  })
  if (res.status === 401) {
    csrfToken = ''
    lastSession = null
    return null
  }
  if (!res.ok) {
    throw new Error(`session check failed: ${res.status}`)
  }
  const body = (await res.json()) as Session
  if (!body.authenticated) {
    csrfToken = ''
    lastSession = null
    return null
  }
  csrfToken = body.csrfToken
  lastSession = body
  return body
}

/** Clears the application session. It does NOT revoke the Dex token or remove
 *  the Room Pass enrolment — saying otherwise would be a lie the demo cannot
 *  back up. CSRF-protected, because it is state-changing. */
export async function logout(csrfToken: string): Promise<void> {
  await fetch('/auth/logout', {
    method: 'POST',
    credentials: 'include',
    headers: { 'x-csrf-token': csrfToken },
  })
}
