import { afterEach, describe, expect, it, vi } from 'vitest'
import { useTestAppConfig } from './testAppConfig'
import { effectScope, type EffectScope } from 'vue'
import type { StreamEvent } from '@configbutler/krm-stream'
import { leafChanges } from './fieldChanges'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { parse } from 'yaml'
import { coffeeConfigKeyedLists, useLiveCoffeeConfig } from './liveCoffeeConfig'

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
  // A save is a PATCH of the object, then a CommitRequest; this answers both
  // well enough (the CommitRequest reads its name from metadata).
  const host = vi.fn(async (_init: RequestInit) =>
    json({ metadata: { name: 'coffee-save-test' } }),
  )
  const fetch = vi.fn(async (url: string, init: RequestInit = {}) => {
    if (url.startsWith('/stream/v1'))
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
  await useTestAppConfig()
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

  // A screen marks FIELDS, and products are keyed by sku, so a remote price
  // change flashes at the price itself rather than at the whole list.
  it('flashes a remote change inside the product list at the field', async () => {
    const { live, event } = await fixture()
    const moved = object('2')
    moved.spec.products = [
      { sku: 'a', name: 'A', priceCents: 425, enabled: true },
    ]
    await event({ type: 'modified', object: moved, redacted: [] })

    expect(live.flashed.value.map((path) => path.join('.'))).toEqual([
      'metadata.resourceVersion',
      'spec.products.0.priceCents',
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
    // The CommitRequest after it is refused: the save still happened.
    host.mockResolvedValueOnce(json({ kind: 'Status', message: 'down' }, 503))
    const save = live.save('Reason')
    await live.save('Double click')
    expect(requests).toHaveLength(1)
    expect(JSON.parse(String(requests[0]!.body))).toEqual({
      spec: { shopName: 'Submitted' },
      metadata: { uid: 'coffee', resourceVersion: '1' },
    })
    live.setValue(['spec', 'bannerText'], 'Typed during save')
    await event({
      type: 'modified',
      object: object('2', 'Submitted'),
      redacted: [],
    })
    finish(json(object('2', 'Submitted')))
    await save
    expect(live.draft.value?.spec.bannerText).toBe('Typed during save')
    expect(live.changes.value.map((change) => change.path)).toEqual([
      ['spec', 'bannerText'],
    ])
    expect(live.commitNotice.value).toBe(
      'Your change was saved to Kubernetes, but asking ConfigButler to commit it failed.',
    )
  })

  // The whole room shares one CoffeeConfig, so a 409 is the ordinary outcome of
  // two people saving in the same second rather than a rare one. When the losing
  // edit does not touch what the winning one changed there is nothing for anybody
  // to decide, and asking was only ever noise -- see the 2026-09-17 post-mortem.
  it('re-sends a stale save by itself when the edits do not overlap', async () => {
    const { live, host, requests } = await fixture()
    live.setValue(['spec', 'shopName'], 'Mine')
    host
      .mockResolvedValueOnce(json({ error: 'stale' }, 409))
      .mockResolvedValueOnce(json(object('2')))
    await live.save('')
    expect(live.conflicts.value).toEqual([])
    expect(live.error.value).toBe('')
    expect(live.notice.value).toContain('Saved to Kubernetes')
    expect(live.draft.value?.spec.shopName).toBe('Mine')
    // The last write is the CommitRequest, asked for once, after the save
    // that landed.
    expect(requests.map((request) => request.method ?? 'GET')).toEqual([
      'PATCH',
      'GET',
      'PATCH',
      'POST',
    ])
    // The retry carries the same edits against the version that won, never a
    // newer version stapled onto the original patch.
    expect(JSON.parse(String(requests[2]!.body))).toEqual({
      spec: { shopName: 'Mine' },
      metadata: { uid: 'coffee', resourceVersion: '2' },
    })
  })

  // The other half of the same rule, and the one that keeps the demo's beat: two
  // people editing the SAME field still stops and still shows its markers.
  it('stops at a real conflict rather than retrying over somebody else', async () => {
    const { live, host, requests } = await fixture()
    live.setValue(['spec', 'shopName'], 'Mine')
    host
      .mockResolvedValueOnce(json({ error: 'stale' }, 409))
      .mockResolvedValueOnce(json(object('2', 'Theirs')))
    await live.save('')
    expect(live.conflicts.value.map((conflict) => conflict.path)).toEqual([
      ['spec', 'shopName'],
    ])
    expect(live.notice.value).toContain('conflict')
    expect(live.canSave.value).toBe(false)
    expect(requests.map((request) => request.method ?? 'GET')).toEqual([
      'PATCH',
      'GET',
    ])
  })

  // Somebody else typed the same thing first. There is no patch left to send, and
  // saying "saved" would claim a write that never happened.
  it('reports that a change had already been made when nothing is left to send', async () => {
    const { live, host, requests } = await fixture()
    live.setValue(['spec', 'shopName'], 'Mine')
    host
      .mockResolvedValueOnce(json({ error: 'stale' }, 409))
      .mockResolvedValueOnce(json(object('2', 'Mine')))
    await live.save('')
    expect(live.conflicts.value).toEqual([])
    expect(live.notice.value).toContain('nothing left to save')
    expect(requests.map((request) => request.method ?? 'GET')).toEqual([
      'PATCH',
      'GET',
    ])
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

  // A product added on the server ahead of the one being edited no longer
  // conflicts: products merge by sku, so the local edit stays on its product.
  it('keeps a local product edit across a product added on the server', async () => {
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
    expect(live.conflicts.value).toEqual([])
    expect(
      live.draft.value?.spec.products.map((product) => product.name),
    ).toEqual(['B', 'Local'])
    expect(live.canSave.value).toBe(true)
  })

  // The reported symptom from rehearsal on 2026-10-06: A and B each edit a
  // price, A saves, and B's whole product block turned red. Different products
  // must merge; the same price must still conflict, and only at that price.
  it('merges two people editing different prices', async () => {
    const { live, event } = await fixture()
    const menu = (rv: string, a: number, b: number) => {
      const next = object(rv)
      next.spec.products = [
        { sku: 'a', name: 'A', priceCents: a, enabled: true },
        { sku: 'b', name: 'B', priceCents: b, enabled: true },
      ]
      return next
    }
    await event({ type: 'modified', object: menu('2', 300, 350), redacted: [] })
    live.setValue(['spec', 'products', 1, 'priceCents'], 375)
    await event({ type: 'modified', object: menu('3', 275, 350), redacted: [] })

    expect(live.conflicts.value).toEqual([])
    expect(live.draft.value?.spec.products.map((p) => p.priceCents)).toEqual([
      275, 375,
    ])
    expect(live.canSave.value).toBe(true)
  })

  it('conflicts at the one price when two people edit the same one', async () => {
    const { live, event } = await fixture()
    const menu = (rv: string, a: number, b: number) => {
      const next = object(rv)
      next.spec.products = [
        { sku: 'a', name: 'A', priceCents: a, enabled: true },
        { sku: 'b', name: 'B', priceCents: b, enabled: true },
      ]
      return next
    }
    await event({ type: 'modified', object: menu('2', 300, 350), redacted: [] })
    live.setValue(['spec', 'products', 1, 'priceCents'], 375)
    await event({ type: 'modified', object: menu('3', 300, 400), redacted: [] })

    expect(live.conflicts.value.map((c) => c.path.join('.'))).toEqual([
      'spec.products.1.priceCents',
    ])
    live.keepMine(['spec', 'products', 1, 'priceCents'])
    expect(live.conflicts.value).toEqual([])
    expect(live.draft.value?.spec.products.map((p) => p.priceCents)).toEqual([
      300, 375,
    ])
  })

  // Vouchers are keyed by code, so the same goes for them.
  it('merges two people editing different vouchers', async () => {
    const { live, event } = await fixture()
    const voucher = (code: string, discountValue: number) => ({
      code,
      enabled: true,
      discountType: 'percentage',
      discountValue,
      maximumUsage: 0,
      appliesToProducts: [],
    })
    const menu = (rv: string, a: number, b: number) => {
      const next = object(rv)
      return {
        ...next,
        spec: { ...next.spec, vouchers: [voucher('A', a), voucher('B', b)] },
      }
    }
    await event({ type: 'modified', object: menu('2', 10, 20), redacted: [] })
    live.setValue(['spec', 'vouchers', 1, 'discountValue'], 25)
    await event({ type: 'modified', object: menu('3', 15, 20), redacted: [] })

    expect(live.conflicts.value).toEqual([])
    expect(live.draft.value?.spec.vouchers.map((v) => v.discountValue)).toEqual(
      [15, 25],
    )
  })

  // The same race, lost at the API server instead of on the stream: the 409
  // retry re-sends the whole list with the winner's price kept in it.
  it('re-sends a stale price edit with the other price that won', async () => {
    const { live, host, requests } = await fixture()
    live.setValue(['spec', 'products', 0, 'priceCents'], 375)
    const won = object('2')
    won.spec.products = [
      { sku: 'a', name: 'A', priceCents: 1, enabled: true },
      { sku: 'b', name: 'B', priceCents: 250, enabled: true },
    ]
    host
      .mockResolvedValueOnce(json({ error: 'stale' }, 409))
      .mockResolvedValueOnce(json(won))
    await live.save('')
    expect(live.conflicts.value).toEqual([])
    expect(JSON.parse(String(requests[2]!.body)).spec.products).toEqual([
      { sku: 'a', name: 'A', priceCents: 375, enabled: true },
      { sku: 'b', name: 'B', priceCents: 250, enabled: true },
    ])
  })

  // The keyed-list schema is a hand copy of the CRD's; fail when they drift.
  it('keys exactly the lists the CRD keys', () => {
    const crd = parse(
      readFileSync(
        fileURLToPath(
          new URL(
            '../../../voter/config/crd/coffeeconfigs.yaml',
            import.meta.url,
          ),
        ),
        'utf8',
      ),
    )
    const spec =
      crd.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties
    const keyed = Object.fromEntries(
      Object.entries(spec as Record<string, Record<string, unknown>>)
        .filter(([, field]) => field['x-kubernetes-list-type'] === 'map')
        .map(([name, field]) => [name, field['x-kubernetes-list-map-keys']]),
    )
    const ours = Object.fromEntries(
      Object.entries(coffeeConfigKeyedLists.properties!.spec!.properties!).map(
        ([name, field]) => [name, field['x-kubernetes-list-map-keys']],
      ),
    )
    expect(ours).toEqual(keyed)
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
