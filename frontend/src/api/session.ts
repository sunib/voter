// The session, as krm-foyer reports it.
//
// krm-foyer owns /auth/*: login, the callback, the sealed session cookie and
// logout. This module is the browser's half of that contract
// (docs/krm-foyer-migration.md, step 2). It never sees a token: /auth/session
// answers with display claims and the CSRF proof, nothing a script could use as
// a Kubernetes credential.

import { appConfig } from './appConfig'

export interface Session {
  authenticated: boolean
  /** The issuer and its subject. The subject is stable per login identity,
   *  which makes it the key for anything kept per person in this browser. */
  issuer: string
  subject: string
  email: string
  displayName: string
  groups: string[]
  /** The Dex connector that authenticated this login -- "room-pass" for someone
   *  who came through Room Pass, "github" for an operator. */
  connector: string
  /** When the session ends, as an RFC 3339 timestamp. */
  expiresAt: string
  csrfToken: string
  /** The header the CSRF token goes in on every change. */
  csrfHeader: string
}

// The CSRF proof krm-foyer issued for this session.
//
// Cached rather than fetched per mutation: /auth/session is the only place that
// hands it out, and re-fetching before every write would turn one save into two
// round trips. Every getSession() refreshes it, and the router guard calls
// getSession() before each protected navigation, so it is current for as long
// as the session is.
let lastSession: Session | null = null

/** The most recent session, for callers that need the name or CSRF token
 *  without re-fetching. Null when signed out. */
export function currentSession(): Session | null {
  return lastSession
}

/** The CSRF token for the current session, or "" when signed out. */
export function currentCsrfToken(): string {
  return lastSession?.csrfToken ?? ''
}

/** The header name the CSRF token goes in. krm-foyer names it in the session;
 *  before one is known, its documented default. */
export function currentCsrfHeader(): string {
  return lastSession?.csrfHeader || 'X-CSRF-Token'
}

/** Whether this session may cast a ballot, for what the page shows. The rule
 *  that counts is the API server's: only a Room Pass login may create a ballot
 *  (docs/quiz-admission.md). Which connector that is, is the deployment's
 *  setting, so the page never has a second copy of it. */
export function canVote(session: Session | null): boolean {
  return (
    session !== null && session.connector === appConfig().participantConnector
  )
}

/** Returns null when there is no session, rather than throwing: "not logged
 *  in" is an expected answer here, not an error. */
export async function getSession(): Promise<Session | null> {
  const res = await fetch('/auth/session', {
    credentials: 'include',
    headers: { accept: 'application/json' },
  })
  if (res.status === 401) {
    lastSession = null
    return null
  }
  if (!res.ok) {
    throw new Error(`session check failed: ${res.status}`)
  }
  const body = (await res.json()) as Partial<Session>
  if (!body.authenticated) {
    lastSession = null
    return null
  }
  // A fixed shape on our side, whatever krm-foyer leaves out: it omits a claim
  // the token does not have, and a screen should render that as empty rather
  // than "undefined".
  lastSession = {
    authenticated: true,
    issuer: body.issuer ?? '',
    subject: body.subject ?? '',
    email: body.email ?? '',
    displayName: body.displayName ?? '',
    groups: body.groups ?? [],
    connector: body.connector ?? '',
    expiresAt: body.expiresAt ?? '',
    csrfToken: body.csrfToken ?? '',
    csrfHeader: body.csrfHeader ?? '',
  }
  return lastSession
}

/** Where a login link goes: krm-foyer's /auth/login, back to `returnTo` (a
 *  path on this site; anything else is dropped here and refused there), with
 *  Dex's connector chosen when given. The connectors a link may name are
 *  krm-foyer's setting (login.authorizationParameters), not free text. */
export function loginURL(returnTo = '/', connector = ''): string {
  const params = new URLSearchParams()
  if (returnTo.startsWith('/') && !returnTo.startsWith('//')) {
    params.set('return_to', returnTo)
  }
  if (connector !== '') {
    params.set('oidc.connector_id', connector)
  }
  const query = params.toString()
  return query === '' ? '/auth/login' : `/auth/login?${query}`
}

/** Ends krm-foyer's session: its cookie, and the streams it has open. It does
 *  NOT revoke the Dex token or end the Room Pass enrolment -- saying otherwise
 *  would be a lie the demo cannot back up. Room Pass's own sign-out is a form
 *  on its /join page, which only that page can post (krm-foyer's room-pass.md,
 *  "Logout is two programs").
 *
 *  The cache is dropped whether the request succeeded or threw: the cookie may
 *  well be gone either way, and currentSession() handing out a name and a CSRF
 *  token for a session that no longer exists is worse than handing out
 *  nothing. A caller that needs certainty calls getSession() again. */
export async function logout(): Promise<void> {
  const header = currentCsrfHeader()
  const token = currentCsrfToken()
  try {
    const res = await fetch('/auth/logout', {
      method: 'POST',
      credentials: 'include',
      headers: { [header]: token },
    })
    if (!res.ok) {
      throw new Error(`sign-out failed: ${res.status}`)
    }
  } finally {
    lastSession = null
  }
}
