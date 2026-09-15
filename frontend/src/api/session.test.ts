import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  currentCsrfToken,
  currentSession,
  getSession,
  logout,
} from './session'

const body = {
  authenticated: true,
  username: 'demo:CgQxMjM0Eglyb29tLXBhc3M',
  displayName: 'Someone',
  email: 'someone@koudijs.dev.test',
  groups: ['demo:voter-audience'],
  csrfToken: 'csrf-from-session',
  expiresAt: 0,
  namespace: 'voter',
  coffeeConfigName: 'demo-coffee',
  roomName: 'demo',
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
  stubFetch(() => json(body))
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
    await logout(currentCsrfToken())

    // The badge in the top bar and every screen seeded from this cache would
    // otherwise keep showing a name and handing out a CSRF token for a session
    // the server has just been told to forget.
    expect(currentSession()).toBeNull()
    expect(currentCsrfToken()).toBe('')
  })

  it('sends the CSRF token, because the backend refuses the POST without it', async () => {
    const calls: [string, RequestInit][] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init: RequestInit = {}) => {
        calls.push([url, init])
        return new Response(null, { status: 204 })
      }),
    )
    await logout('csrf-from-session')

    expect(calls).toHaveLength(1)
    const [url, init] = calls[0]!
    expect(url).toBe('/auth/logout')
    expect(init.method).toBe('POST')
    expect(init.credentials).toBe('include')
    expect(init.headers).toEqual({ 'x-csrf-token': 'csrf-from-session' })
  })

  // The cookie may be gone even when the response never arrives, so keeping the
  // cache would leave the app claiming a session it cannot prove it has.
  it('clears the cache even when the request fails', async () => {
    stubFetch(() => {
      throw new Error('network down')
    })
    await expect(logout('csrf-from-session')).rejects.toThrow('network down')

    expect(currentSession()).toBeNull()
    expect(currentCsrfToken()).toBe('')
  })
})

describe('getSession', () => {
  it('returns null on 401 rather than throwing, and forgets the session', async () => {
    stubFetch(() => json({ authenticated: false, loginUrl: '/auth/login' }, 401))

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
})
