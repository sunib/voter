import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  getAdminCoffeeConfig,
  getStorefront,
  getVoucherUsage,
  patchAdminCoffeeConfig,
  submitOrder,
} from './coffee'
import { getSession } from './session'

// Every one of these tests exists because of a bug that actually shipped:
// the editor called /public/admin/coffeeconfig, which the backend deleted with
// the legacy session and now answers 404, and no mutation carried the CSRF
// token the backend requires. Neither was visible in a type-check or a build.

type FetchCall = { url: string; init: RequestInit }

let calls: FetchCall[]

/** Installs a fetch stub returning `body`, and records what was requested. */
function stubFetch(body: unknown, status = 200) {
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init: init ?? {} })
      return new Response(JSON.stringify(body), {
        status,
        headers: { 'content-type': 'application/json' },
      })
    },
  )
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

/** The request the code under test made. Fails loudly rather than returning
 *  undefined, so a test that asserts on a request that never happened says so. */
function only(): FetchCall {
  if (calls.length !== 1) {
    throw new Error(`expected exactly 1 request, got ${calls.length}`)
  }
  return calls[0]!
}

function headerOf(name: string): string | null {
  return new Headers(only().init.headers).get(name)
}

beforeEach(() => {
  calls = []
  vi.stubGlobal('window', { location: { origin: 'https://voter.koudijs.dev' } })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

/** Signs in, so the module-level CSRF token is populated the way the router
 *  guard populates it in the real app. */
async function signIn(csrfToken = 'csrf-from-session') {
  stubFetch({
    authenticated: true,
    username: 'demo:abc',
    displayName: 'Someone',
    email: 'someone@demo.invalid',
    groups: ['demo:voter-audience'],
    csrfToken,
    expiresAt: 0,
  })
  await getSession()
  calls = []
}

describe('endpoint paths', () => {
  it('reads the coffee config from the participant endpoint', async () => {
    stubFetch({ spec: {} })
    await getAdminCoffeeConfig()

    expect(only().url).toBe('/public/coffeeconfig')
    // The legacy path is gone from the backend; calling it is a silent 404.
    expect(only().url).not.toContain('/public/admin/')
  })

  it('patches the coffee config on the participant endpoint', async () => {
    await signIn()
    stubFetch({ config: { spec: {} }, saved: true })
    await patchAdminCoffeeConfig({ spec: { shopName: 'New' } })

    expect(only().url).toBe('/public/coffeeconfig')
    expect(only().init.method).toBe('PATCH')
  })

  it('passes the voucher through to the storefront as a query parameter', async () => {
    stubFetch({ shop: {}, voucher: {}, products: [] })
    await getStorefront('TESTNET')

    expect(only().url).toBe('/public/storefront?voucher=TESTNET')
  })

  it('omits the voucher parameter when there is no code', async () => {
    stubFetch({ shop: {}, voucher: {}, products: [] })
    await getStorefront()

    expect(only().url).toBe('/public/storefront')
  })
})

describe('CSRF proof', () => {
  it('sends the session token on an order', async () => {
    await signIn('token-abc')
    stubFetch({ orderId: 'x', status: 'placed' })
    await submitOrder({ items: [{ sku: 'coffee-espresso', quantity: 1 }] })

    expect(headerOf('x-csrf-token')).toBe('token-abc')
  })

  it('sends the session token on a config patch', async () => {
    await signIn('token-abc')
    stubFetch({ config: { spec: {} }, saved: true })
    await patchAdminCoffeeConfig({ spec: {} })

    expect(headerOf('x-csrf-token')).toBe('token-abc')
  })

  it('does not send a CSRF header on reads', async () => {
    await signIn('token-abc')
    stubFetch({ spec: {} })
    await getAdminCoffeeConfig()

    expect(headerOf('x-csrf-token')).toBeNull()
  })

  it('keeps the merge-patch content type while adding CSRF', async () => {
    await signIn('token-abc')
    stubFetch({ config: { spec: {} }, saved: true })
    await patchAdminCoffeeConfig({ spec: {} }, { reason: 'raise the limit' })

    expect(headerOf('content-type')).toBe('application/merge-patch+json')
    expect(headerOf('x-change-reason')).toBe('raise the limit')
    expect(headerOf('x-csrf-token')).toBe('token-abc')
  })

  it('forgets the token when the session is gone', async () => {
    await signIn('token-abc')
    stubFetch({}, 401)
    expect(await getSession()).toBeNull()

    calls = []
    stubFetch({ orderId: 'x', status: 'placed' })
    await submitOrder({ items: [{ sku: 'coffee-espresso', quantity: 1 }] })

    // Better to send an empty token and get an honest 403 than to replay a
    // stale one from a session that has ended.
    expect(headerOf('x-csrf-token')).toBe('')
  })
})

describe('the save result', () => {
  it('reports a save that reached Kubernetes but not ConfigButler', async () => {
    await signIn()
    stubFetch({
      config: { spec: { shopName: 'New' } },
      saved: true,
      committed: false,
      commitError: 'asking ConfigButler to commit it failed',
    })

    const result = await patchAdminCoffeeConfig({ spec: {} })

    expect(result.saved).toBe(true)
    expect(result.committed).toBe(false)
    expect(result.commitError).toBeTruthy()
    // The config is nested in the result, not the result itself. Treating the
    // envelope as a CoffeeConfig is what the old type signature invited.
    expect(result.config.spec.shopName).toBe('New')
  })

  it('surfaces a rejected order as a failure body, not a thrown error', async () => {
    await signIn()
    stubFetch({
      orderId: '',
      status: 'rejected',
      currency: 'EUR',
      totalPriceCents: 0,
      failure: {
        code: 'VoucherDepleted',
        message: 'This voucher has been used the maximum number of times.',
      },
    })

    const order = await submitOrder({
      voucherCode: 'TESTNET',
      items: [{ sku: 'coffee-espresso', quantity: 1 }],
    })

    expect(order.status).toBe('rejected')
    expect(order.failure?.code).toBe('VoucherDepleted')
  })

  it('throws on a Kubernetes denial so the screen can show it', async () => {
    await signIn()
    stubFetch({ error: 'coffeeconfigs is forbidden', reason: 'Forbidden' }, 403)

    await expect(
      submitOrder({ items: [{ sku: 'coffee-espresso', quantity: 1 }] }),
    ).rejects.toMatchObject({ status: 403, message: 'coffeeconfigs is forbidden' })
  })
})

describe('voucher usage', () => {
  it('reads the redemption counts from the participant endpoint', async () => {
    await signIn()
    stubFetch({ voucherUsage: { testnet: 3 }, scope: 'process' })

    const usage = await getVoucherUsage()

    expect(only().url).toBe('/public/vouchers')
    // Keyed by the lower-cased code, which is how the admin screen looks it up
    // next to each voucher's maximumUsage.
    expect(usage.voucherUsage.testnet).toBe(3)
    expect(usage.scope).toBe('process')
  })
})
