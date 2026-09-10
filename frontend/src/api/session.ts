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
}

/** Returns null when there is no session, rather than throwing: "not logged
 *  in" is an expected answer here, not an error. */
export async function getSession(): Promise<Session | null> {
  const res = await fetch('/auth/session', {
    credentials: 'include',
    headers: { accept: 'application/json' },
  })
  if (res.status === 401) {
    return null
  }
  if (!res.ok) {
    throw new Error(`session check failed: ${res.status}`)
  }
  const body = (await res.json()) as Session
  return body.authenticated ? body : null
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
