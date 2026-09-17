import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import type { StreamEvent } from '@configbutler/krm-stream'
import { leafChanges } from './fieldChanges'
import { useLiveCoffeeConfig } from './liveCoffeeConfig'

const object = (rv = '1', shopName = 'Base', uid = 'coffee') => ({
  apiVersion: 'examples.configbutler.ai/v1alpha1',
  kind: 'CoffeeConfig',
  metadata: { uid, name: 'demo', namespace: 'voter', resourceVersion: rv },
  spec: {
    shopName,
    bannerText: '',
    products: [{ sku: 'a', name: 'A', priceCents: 1, enabled: true }],
    vouchers: [],
  },
})
let scope: EffectScope
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
async function fixture(editable = true) {
  let controller: ReadableStreamDefaultController<Uint8Array>
  let seq = 0
  const requests: RequestInit[] = []
  const host = vi.fn(async (_init: RequestInit) => json({ saved: true }))
  const fetch = vi.fn(async (url: string, init: RequestInit = {}) => {
    if (url.startsWith('/public/stream'))
      return new Response(
        new ReadableStream<Uint8Array>({
          start(c) {
            seq = 0
            controller = c
          },
        }),
        { headers: { 'Content-Type': 'text/event-stream' } },
      )
    requests.push(init)
    return host(init)
  })
  vi.stubGlobal('fetch', fetch)
  scope = effectScope()
  const live = scope.run(() => useLiveCoffeeConfig('voter', 'demo', editable))!
  await vi.waitFor(() => expect(fetch).toHaveBeenCalled())
  const event = async (event: Omit<StreamEvent, 'seq'>) => {
    controller!.enqueue(
      new TextEncoder().encode(
        `data: ${JSON.stringify({ ...event, seq: ++seq })}\n\n`,
      ),
    )
    // Let the actual fetch SSE parser and synchronous store subscriptions run.
    for (let i = 0; i < 8; i++) await Promise.resolve()
  }
  await event({ type: 'reset' })
  await event({ type: 'added', object: object(), redacted: [] })
  await event({ type: 'synced' })
  await vi.waitFor(() => expect(live.synced.value).toBe(true))
  return {
    live,
    event,
    host,
    requests,
    fetch,
    disconnect: () => controller.close(),
  }
}
afterEach(() => {
  scope?.stop()
  vi.unstubAllGlobals()
})

describe('CoffeeConfig library integration', () => {
  // The exact words, pinned.
  //
  // These two sentences are asserted verbatim by live-stream.spec.js, which
  // runs in a Playwright job behind a k3d cluster and takes six minutes to tell
  // you that you reworded a notice. Generalizing "Configuration refreshed" to
  // "Refreshed" while extracting the shared editor engine did exactly that, and
  // this test is here so the next person finds out in one second instead.
  //
  // They are per-resource for a reason: a CoffeeConfig is a "configuration" and
  // a database request is not, so the copy lives in each binding rather than in
  // the engine both of them share.
  it('keeps the refresh notice in the words the browser tests assert', async () => {
    const { live, host } = await fixture()
    live.setValue(['spec', 'shopName'], 'Mine')
    host.mockImplementationOnce(async () => json(object()))
    await live.refreshFromServer()

    expect(live.notice.value).toBe(
      'Configuration refreshed. Your edits are intact; review and save again.',
    )
  })

  // The reported symptom: change one price on the coffee screen and every
  // field appears changed. The cause is not the array being mangled -- it is
  // that a merge patch replaces a list whole, so the library reports the edit
  // at the list, and a screen prefix-matching that marks everything beneath it.
  // Pinned here against the real store, because the fix in the screen is only
  // correct as long as this is what the store actually says.
  it('reports an edit inside a list at the list, and leafChanges narrows it', async () => {
    const { live } = await fixture()
    live.setValue(
      ['spec', 'products'],
      [
        { sku: 'a', name: 'A', priceCents: 1, enabled: true },
        { sku: 'b', name: 'B', priceCents: 2, enabled: true },
      ],
    )
    live.setValue(['spec', 'products', 1, 'priceCents'], 350)

    expect(live.changes.value.map((change) => change.path)).toEqual([
      ['spec', 'products'],
    ])
    expect(
      leafChanges(live.changes.value).map((change) => change.path.join('.')),
    ).toEqual(['spec.products.1'])
  })

  // The screen's Revert button now addresses the field it is next to, not the
  // whole list it sits in. Reverting one price must leave the other product
  // exactly where it was.
  it('reverts one price without disturbing the rest of the menu', async () => {
    const { live, event } = await fixture()
    const menu = object()
    menu.spec.products = [
      { sku: 'a', name: 'A', priceCents: 300, enabled: true },
      { sku: 'b', name: 'B', priceCents: 350, enabled: true },
    ]
    menu.metadata.resourceVersion = '2'
    await event({ type: 'modified', object: menu, redacted: [] })

    live.setValue(['spec', 'products', 0, 'priceCents'], 275)
    live.setValue(['spec', 'products', 1, 'name'], 'Renamed')
    live.takeTheirs(['spec', 'products', 0, 'priceCents'])

    expect(
      leafChanges(live.changes.value).map((change) => change.path.join('.')),
    ).toEqual(['spec.products.1.name'])
    expect(live.draft.value?.spec.products).toEqual([
      { sku: 'a', name: 'A', priceCents: 300, enabled: true },
      { sku: 'b', name: 'Renamed', priceCents: 350, enabled: true },
    ])
  })

  // Pinned because a screen marks FIELDS: the library flashes a remote change
  // inside a list at the list's own path, so a per-field flash lookup finds
  // nothing to highlight. See docs/krm-stream-feedback.md.
  it('flashes a remote change inside a list at the list, not at the field', async () => {
    const { live, event } = await fixture()
    const moved = object('2')
    moved.spec.products = [
      { sku: 'a', name: 'A', priceCents: 425, enabled: true },
    ]
    await event({ type: 'modified', object: moved, redacted: [] })

    // resourceVersion moves on every event; spec.products is the interesting
    // one -- the whole list, never spec.products.0.priceCents.
    expect(live.flashed.value.map((path) => path.join('.'))).toEqual([
      'metadata.resourceVersion',
      'spec.products',
    ])
  })

  it('names one field when one price is edited', async () => {
    const { live } = await fixture()
    live.setValue(['spec', 'products', 0, 'priceCents'], 275)
    expect(
      leafChanges(live.changes.value).map((change) => change.path.join('.')),
    ).toEqual(['spec.products.0.priceCents'])
  })

  it('uses one stream-seeded store and keeps later edits when a receipt arrives', async () => {
    const { live, event, host, requests } = await fixture()
    expect(requests).toHaveLength(0)
    live.setValue(['spec', 'shopName'], 'Submitted')
    let finish!: (response: Response) => void
    host.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    const save = live.save('Reason')
    await live.save('Double click')
    expect(requests).toHaveLength(1)
    expect(JSON.parse(String(requests[0]!.body))).toEqual({
      uid: 'coffee',
      resourceVersion: '1',
      patch: { spec: { shopName: 'Submitted' } },
    })
    live.setValue(['spec', 'bannerText'], 'Typed during save')
    await event({
      type: 'modified',
      object: object('2', 'Submitted'),
      redacted: [],
    })
    finish(
      json({
        saved: true,
        commitRequested: false,
        commitError: 'Commit service unavailable',
      }),
    )
    await save
    expect(live.draft.value?.spec.bannerText).toBe('Typed during save')
    expect(live.changes.value.map((change) => change.path)).toEqual([
      ['spec', 'bannerText'],
    ])
    expect(live.commitNotice.value).toBe('Commit service unavailable')
  })

  it('refreshes a stale version without inventing field conflicts or retrying the write', async () => {
    const { live, host, requests } = await fixture()
    live.setValue(['spec', 'shopName'], 'Mine')
    host
      .mockResolvedValueOnce(json({ error: 'stale' }, 409))
      .mockResolvedValueOnce(json(object('2')))
    await live.save('')
    expect(live.conflicts.value).toEqual([])
    expect(live.notice.value).toContain('review and save again')
    expect(live.draft.value?.spec.shopName).toBe('Mine')
    expect(requests.map((request) => request.method ?? 'GET')).toEqual([
      'PATCH',
      'GET',
    ])
    await live.save('')
    expect(JSON.parse(String(requests[2]!.body)).resourceVersion).toBe('2')
  })

  it('rejects a delayed conflict GET overtaken by the watch and requires a fresh read', async () => {
    const { live, event, host, requests } = await fixture()
    live.setValue(['spec', 'bannerText'], 'Mine')
    let finish!: (response: Response) => void
    host.mockResolvedValueOnce(json({}, 409)).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    const saving = live.save('')
    await vi.waitFor(() => expect(requests).toHaveLength(2))
    await event({
      type: 'modified',
      object: object('3', 'Newest'),
      redacted: [],
    })
    finish(json(object('2', 'Old')))
    await saving
    expect(live.server.value?.spec.shopName).toBe('Newest')
    expect(live.needsRead.value).toBe(true)
    host.mockResolvedValueOnce(json(object('3', 'Newest')))
    await live.save('')
    expect(requests.map((request) => request.method ?? 'GET')).toEqual([
      'PATCH',
      'GET',
      'GET',
    ])
    expect(live.needsRead.value).toBe(false)
  })

  it('reviews array conflicts as a whole and keeps the explicitly chosen local array', async () => {
    const { live, event } = await fixture()
    live.setValue(['spec', 'products', 0, 'name'], 'Local')
    const remote = object('2')
    remote.spec.products.unshift({
      sku: 'b',
      name: 'B',
      priceCents: 2,
      enabled: true,
    })
    await event({ type: 'modified', object: remote, redacted: [] })
    expect(live.conflicts.value.map((conflict) => conflict.path)).toEqual([
      ['spec', 'products'],
    ])
    expect(live.canSave.value).toBe(false)
    live.keepMine(['spec', 'products'])
    expect(live.conflicts.value).toEqual([])
    expect(
      live.draft.value?.spec.products.map((product) => product.name),
    ).toEqual(['Local'])
    expect(live.canSave.value).toBe(true)
  })

  it('keeps a deletion recovery copy without transferring it to a replacement UID', async () => {
    const { live, event, requests } = await fixture()
    live.setValue(['spec', 'shopName'], 'Recover me')
    await event({
      type: 'deleted',
      identity: {
        uid: 'coffee',
        apiVersion: object().apiVersion,
        kind: 'CoffeeConfig',
        name: 'demo',
        namespace: 'voter',
      },
    })
    await event({
      type: 'added',
      object: object('2', 'Replacement', 'new-uid'),
      redacted: [],
    })
    expect(live.draft.value).toBeNull()
    expect(live.recoveryDraft.value?.spec.shopName).toBe('Recover me')
    await live.save('')
    expect(requests).toHaveLength(0)
    scope.stop()
    expect(live.recoveryDraft.value).toBeNull()
  })

  it('recovers a closed transport with a fresh snapshot while keeping unsaved edits', async () => {
    const { live, event, fetch, disconnect } = await fixture()
    live.setValue(['spec', 'bannerText'], 'Typing offline')
    disconnect()
    await vi.waitFor(() => expect(live.synced.value).toBe(false))
    expect(live.canSave.value).toBe(false)
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2))
    await event({ type: 'reset' })
    await event({
      type: 'added',
      object: object('2', 'Updated while offline'),
      redacted: [],
    })
    await event({ type: 'synced' })
    await vi.waitFor(() => expect(live.synced.value).toBe(true))
    expect(live.draft.value?.spec.bannerText).toBe('Typing offline')
    expect(live.server.value?.spec.shopName).toBe('Updated while offline')
    expect(live.canSave.value).toBe(true)
  })

  it('keeps storefront resources read-only', async () => {
    const { live } = await fixture(false)
    expect(() => live.setValue(['spec', 'shopName'], 'No')).toThrow()
    expect(live.canSave.value).toBe(false)
  })
})
