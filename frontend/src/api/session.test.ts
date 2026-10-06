import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { loadAppConfig } from './appConfig'
import {
  canVote,
  currentCsrfHeader,
  currentCsrfToken,
  currentSession,
  getSession,
  loginURL,
  logout,
} from './session'

// krm-foyer's /auth/session, signed in through Room Pass.
const body = {
  authenticated: true,
  issuer: 'https://login.voter.test:19443',
  subject: 'CgQxMjM0Eglyb29tLXBhc3M',
  email: 'someone@koudijs.dev.test',
  displayName: 'Someone',
  groups: ['demo:voter-audience'],
  connector: 'room-pass',
  expiresAt: '2026-10-06T20:00:00Z',
  csrfToken: 'csrf-from-session',
  csrfHeader: 'X-CSRF-Token',
}

const config = {
  namespace: 'voter',
  coffeeConfigName: 'demo-coffee',
  roomName: 'demo',
  commitURLTemplate: '',
  participantConnector: 'room-pass',
}

function stubFetch(handler: (url: string) => Response) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => handler(url)),
  )
}

const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

beforeEach(async () => {
  stubFetch((url) => json(url === '/config.json' ? config : body))
  await loadAppConfig()
  await getSession()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('logout', () => {
  it('drops the cached session so nothing stale is rendered afterwards', async () => {
    expect(currentSession()?.displayName).toBe('Someone')
    expect(currentCsrfToken()).toBe('csrf-from-session')

    stubFetch(() => new Response(null, { status: 204 }))
    await logout()

    // The badge in the top bar and every screen seeded from this cache would
    // otherwise keep showing a name and handing out a CSRF token for a session
    // krm-foyer has just been told to forget.
    expect(currentSession()).toBeNull()
    expect(currentCsrfToken()).toBe('')
  })

  it('sends the CSRF token in the header the session names', async () => {
    const calls: [string, RequestInit][] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init: RequestInit = {}) => {
        calls.push([url, init])
        return new Response(null, { status: 204 })
      }),
    )
    await logout()

    expect(calls).toHaveLength(1)
    const [url, init] = calls[0]!
    expect(url).toBe('/auth/logout')
    expect(init.method).toBe('POST')
    expect(init.credentials).toBe('include')
    expect(init.headers).toEqual({ 'X-CSRF-Token': 'csrf-from-session' })
  })

  // A refused logout (a stale CSRF token, say) left the cookie in place, so
  // the caller must not report the person signed out.
  it('throws when krm-foyer refuses, and still forgets the session', async () => {
    stubFetch(() => new Response(null, { status: 403 }))
    await expect(logout()).rejects.toThrow('403')
    expect(currentSession()).toBeNull()
  })

  // The cookie may be gone even when the response never arrives, so keeping the
  // cache would leave the app claiming a session it cannot prove it has.
  it('clears the cache even when the request fails', async () => {
    stubFetch(() => {
      throw new Error('network down')
    })
    await expect(logout()).rejects.toThrow('network down')

    expect(currentSession()).toBeNull()
    expect(currentCsrfToken()).toBe('')
  })
})

describe('getSession', () => {
  it('returns null on 401 rather than throwing, and forgets the session', async () => {
    stubFetch(() => json({ authenticated: false }, 401))

    await expect(getSession()).resolves.toBeNull()
    expect(currentSession()).toBeNull()
    expect(currentCsrfToken()).toBe('')
  })

  it('carries every field the endpoint reports, not just the name', async () => {
    const session = await getSession()
    // The identity page renders these by key name; a field silently dropped
    // here would show up there as a blank row rather than an error.
    expect(session).toEqual(body)
  })

  // krm-foyer omits a claim the token does not have, rather than sending it
  // empty. A screen should see an empty value, never "undefined".
  it('fills what krm-foyer leaves out with empty values', async () => {
    stubFetch(() =>
      json({ authenticated: true, csrfToken: 't', csrfHeader: 'X-CSRF-Token' }),
    )
    const session = await getSession()
    expect(session?.displayName).toBe('')
    expect(session?.groups).toEqual([])
    expect(session?.connector).toBe('')
  })

  it('falls back to the documented CSRF header before a session names one', async () => {
    stubFetch(() => json({ authenticated: false }, 401))
    await getSession()
    expect(currentCsrfHeader()).toBe('X-CSRF-Token')
  })
})

describe('canVote', () => {
  // Which connector is the audience is deployment configuration, read from
  // /config.json, so the browser never holds a second copy of the rule.
  it('is true for a login through the configured participant connector', () => {
    expect(canVote(currentSession())).toBe(true)
  })

  it('is false for the operator, and for no session at all', async () => {
    stubFetch(() => json({ ...body, connector: 'github' }))
    expect(canVote(await getSession())).toBe(false)
    expect(canVote(null)).toBe(false)
  })
})

describe('loginURL', () => {
  it("names krm-foyer's parameters, not the old ones", () => {
    expect(loginURL('/room', 'github')).toBe(
      '/auth/login?return_to=%2Froom&oidc.connector_id=github',
    )
  })

  it('leaves out a connector it was not given', () => {
    expect(loginURL('/admin')).toBe('/auth/login?return_to=%2Fadmin')
  })

  // Only a path on this site: anything else would make login an open
  // redirect. krm-foyer refuses it as well; this keeps it out of the link.
  it('drops a return address that is not a local path', () => {
    expect(loginURL('//evil.example/x')).toBe('/auth/login')
    expect(loginURL('https://evil.example/x')).toBe('/auth/login')
  })
})
