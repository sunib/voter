import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  getAdminCoffeeConfig,
  getStorefront,
  getVoucherUsage,
  patchAdminCoffeeConfig,
  submitOrder,
} from './coffee'
import { getSession } from './session'
import { useTestAppConfig } from './testAppConfig'

const COFFEE_URL =
  '/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/coffeeconfigs/demo-coffee'
const COMMITREQUESTS_URL =
  '/k8s/apis/configbutler.ai/v1alpha3/namespaces/voter/commitrequests'

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

/** Answers each request by URL and method, recording it; for the two-write
 *  save, where one stub body cannot answer both. */
function route(
  answer: (url: string, method: string) => { body?: unknown; status?: number },
) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = (init?.method ?? 'GET').toUpperCase()
      calls.push({ url, init: init ?? {} })
      const { body = {}, status = 200 } = answer(url, method)
      return new Response(JSON.stringify(body), {
        status,
        headers: { 'content-type': 'application/json' },
      })
    }),
  )
}

const bodyOf = (call: FetchCall) => JSON.parse(String(call.init.body))
const headersOf = (call: FetchCall) => new Headers(call.init.headers)

beforeEach(async () => {
  calls = []
  vi.stubGlobal('window', { location: { origin: 'https://demo.koudijs.dev' } })
  await useTestAppConfig()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

/** Signs in, so the module-level CSRF token is populated the way the router
 *  guard populates it in the real app. */
async function signIn(csrfToken = 'csrf-from-session') {
  stubFetch({
    authenticated: true,
    displayName: 'Someone',
    email: 'someone@koudijs.dev.test',
    groups: ['demo:voter-audience'],
    connector: 'room-pass',
    csrfToken,
    csrfHeader: 'X-CSRF-Token',
    expiresAt: '2026-10-06T20:00:00Z',
  })
  await getSession()
  calls = []
}

describe('endpoint paths', () => {
  it('reads the coffee config through /k8s, projected like the stream', async () => {
    stubFetch({
      metadata: {
        name: 'demo-coffee',
        managedFields: [{ manager: 'kubectl' }],
        annotations: {
          'kubectl.kubernetes.io/last-applied-configuration': '{}',
        },
      },
      spec: {},
    })
    const config = await getAdminCoffeeConfig()

    expect(only().url).toBe(COFFEE_URL)
    // The editor reconciles this read against what the stream delivered; the
    // machinery the stream leaves out must not arrive as a server change.
    expect(config.metadata).toEqual({ name: 'demo-coffee' })
  })

  it('patches the coffee config through /k8s, conditional on the version edited', async () => {
    await signIn()
    route(() => ({ body: { metadata: { name: 'coffee-save-x' } } }))
    await patchAdminCoffeeConfig({
      uid: 'coffee',
      resourceVersion: '7',
      patch: { spec: { shopName: 'New' } },
    })

    const patch = calls[0]!
    expect(patch.url).toBe(`${COFFEE_URL}?fieldManager=voter`)
    expect(patch.init.method).toBe('PATCH')
    expect(headersOf(patch).get('content-type')).toBe(
      'application/merge-patch+json',
    )
    // uid and resourceVersion in a merge patch are the API server's
    // preconditions: a replaced or moved-on object is a 409, never overwritten.
    expect(bodyOf(patch)).toEqual({
      spec: { shopName: 'New' },
      metadata: { uid: 'coffee', resourceVersion: '7' },
    })
  })

  it('refuses a patch outside spec without sending anything', async () => {
    await signIn()
    route(() => ({}))
    await expect(
      patchAdminCoffeeConfig({
        uid: 'coffee',
        resourceVersion: '1',
        patch: { metadata: { labels: { x: 'y' } } },
      }),
    ).rejects.toThrow('Only spec is editable.')
    expect(calls).toHaveLength(0)
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

  it('sends the session token on both writes of a save', async () => {
    await signIn('token-abc')
    route(() => ({ body: { metadata: { name: 'coffee-save-x' } } }))
    await patchAdminCoffeeConfig({
      uid: 'coffee',
      resourceVersion: '1',
      patch: { spec: {} },
    })

    expect(calls).toHaveLength(2)
    for (const call of calls) {
      expect(headersOf(call).get('x-csrf-token')).toBe('token-abc')
    }
  })

  it('does not send a CSRF header on reads', async () => {
    await signIn('token-abc')
    stubFetch({ spec: {} })
    await getAdminCoffeeConfig()

    expect(headerOf('x-csrf-token')).toBeNull()
  })

  it('asks ConfigButler to commit, with the reason as the message', async () => {
    await signIn('token-abc')
    route(() => ({ body: { metadata: { name: 'coffee-save-x' } } }))
    const result = await patchAdminCoffeeConfig(
      { uid: 'coffee', resourceVersion: '1', patch: { spec: {} } },
      { reason: '  raise the limit  ' },
    )

    const commit = calls[1]!
    expect(commit.url).toBe(`${COMMITREQUESTS_URL}?fieldManager=voter`)
    expect(commit.init.method).toBe('POST')
    expect(bodyOf(commit)).toEqual({
      apiVersion: 'configbutler.ai/v1alpha3',
      kind: 'CommitRequest',
      metadata: { generateName: 'coffee-save-', namespace: 'voter' },
      spec: {
        gitTargetRef: { name: 'voter-demo' },
        message: 'raise the limit',
        closeDelaySeconds: 2,
      },
    })
    expect(result).toEqual({
      saved: true,
      commitRequested: true,
      commitRequest: 'coffee-save-x',
    })
  })

  it('asks for no commit when the deployment names no GitTarget', async () => {
    await useTestAppConfig({ gitTargetName: '' })
    await signIn()
    route(() => ({}))
    const result = await patchAdminCoffeeConfig({
      uid: 'coffee',
      resourceVersion: '1',
      patch: { spec: {} },
    })

    expect(calls).toHaveLength(1)
    expect(result).toEqual({ saved: true })
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
    route((url) =>
      url.startsWith(COMMITREQUESTS_URL)
        ? { status: 403, body: { kind: 'Status', message: 'forbidden' } }
        : { body: {} },
    )

    const result = await patchAdminCoffeeConfig({
      uid: 'coffee',
      resourceVersion: '1',
      patch: { spec: {} },
    })

    expect(result.saved).toBe(true)
    expect(result.commitRequested).toBe(false)
    expect(result.commitError).toBeTruthy()
    expect(result).not.toHaveProperty('config')
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
    ).rejects.toMatchObject({
      status: 403,
      message: 'coffeeconfigs is forbidden',
    })
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
