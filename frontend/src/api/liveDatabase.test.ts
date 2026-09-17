import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import type { StreamEvent } from '@configbutler/krm-stream'

import { useLiveDatabase } from './liveDatabase'

// The Database editor and the coffee menu editor run the same engine
// (liveEditableResource.ts). These tests are here so that stays true: they pin
// the behaviour a room would notice -- a concurrent edit becoming a conflict,
// and a save carrying the note that explains it.

const object = (rv = '1', size = 'small', uid = 'db') => ({
  apiVersion: 'platform.configbutler.ai/v1alpha1',
  kind: 'Database',
  metadata: { uid, name: 'checkout', namespace: 'voter', resourceVersion: rv },
  spec: {
    engine: 'postgresql',
    tier: 'standard',
    size,
    service: 'checkout',
    owner: {
      team: 'payments-core',
      costCentre: 'CC-finance-07',
      contact: 'pc@example.com',
    },
  },
})

let scope: EffectScope
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

async function fixture() {
  let controller: ReadableStreamDefaultController<Uint8Array>
  let seq = 0
  const requests: { url: string; init: RequestInit }[] = []
  const host = vi.fn(async () => json({ saved: true }))
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
    requests.push({ url, init })
    return host()
  })
  vi.stubGlobal('fetch', fetch)
  scope = effectScope()
  const live = scope.run(() => useLiveDatabase('voter', 'checkout'))!
  await vi.waitFor(() => expect(fetch).toHaveBeenCalled())
  const event = async (next: Omit<StreamEvent, 'seq'>) => {
    controller!.enqueue(
      new TextEncoder().encode(
        `data: ${JSON.stringify({ ...next, seq: ++seq })}\n\n`,
      ),
    )
    // Let the SSE parser and the synchronous store subscriptions run.
    for (let i = 0; i < 8; i++) await Promise.resolve()
  }
  await event({ type: 'reset' })
  await event({ type: 'added', object: object(), redacted: [] })
  await event({ type: 'synced' })
  await vi.waitFor(() => expect(live.synced.value).toBe(true))
  return { live, event, requests, fetch }
}

afterEach(() => {
  scope?.stop()
  vi.unstubAllGlobals()
})

describe('the Database editor', () => {
  it('subscribes to the one object it is editing, and nothing wider', async () => {
    const { fetch } = await fixture()
    const url = String(fetch.mock.calls[0]?.[0])
    expect(url).toContain('resource=databases')
    expect(url).toContain('group=platform.configbutler.ai')
    expect(url).toContain('namespace=voter')
    expect(url).toContain('name=checkout')
  })

  it('reports an edit at the field a person changed', async () => {
    const { live } = await fixture()
    live.setValue(['spec', 'size'], 'large')
    expect(live.changes.value.map((change) => change.path)).toEqual([
      ['spec', 'size'],
    ])
    expect(live.draft.value?.spec.size).toBe('large')
  })

  // Two people on the same request during the talk. The second one's typing is
  // never silently overwritten -- it becomes a conflict they are asked about.
  it('turns a concurrent change to the same field into a conflict', async () => {
    const { live, event } = await fixture()
    live.setValue(['spec', 'size'], 'large')

    await event({
      type: 'modified',
      object: object('2', 'xlarge'),
      redacted: [],
    })

    expect(live.conflicts.value.map((conflict) => conflict.path)).toEqual([
      ['spec', 'size'],
    ])
    expect(live.conflicts.value[0]?.theirs).toBe('xlarge')
    // Still theirs to choose. Saving is refused until they do.
    expect(live.draft.value?.spec.size).toBe('large')
    expect(live.canSave.value).toBe(false)
  })

  it('leaves an untouched field following the server', async () => {
    const { live, event } = await fixture()
    live.setValue(['spec', 'size'], 'large')

    await event({
      type: 'modified',
      object: object('2', 'small'),
      redacted: [],
    })
    // The server moved nothing this editor touched, so no conflict, and the
    // edit survives.
    expect(live.conflicts.value).toEqual([])
    expect(live.draft.value?.spec.size).toBe('large')
  })

  // The note is the point of the page. It travels as a header, exactly as the
  // coffee editor sends it, and the patch carries uid and resourceVersion so a
  // stale editor is refused rather than clobbering somebody.
  it('sends the note and the version it was editing', async () => {
    const { live, requests } = await fixture()
    live.setValue(['spec', 'size'], 'large')
    await live.save('Growth forecast doubled.')

    const save = requests.find((request) => request.init.method === 'PATCH')
    expect(save?.url).toBe('/public/databases/checkout')
    const headers = new Headers(save!.init.headers)
    expect(headers.get('x-change-reason')).toBe('Growth forecast doubled.')
    const body = JSON.parse(String(save!.init.body))
    expect(body.uid).toBe('db')
    expect(body.resourceVersion).toBe('1')
    expect(body.patch).toEqual({ spec: { size: 'large' } })
  })

  // Only `spec` is editable, and the store does not quietly drop a write
  // outside it -- it throws. Worth pinning: it means a page that tries to edit
  // status fails loudly in development rather than sending a patch the backend
  // then refuses in front of a room.
  it('refuses a write outside spec rather than dropping it', async () => {
    const { live } = await fixture()
    expect(() => live.setValue(['status', 'phase'], 'Ready')).toThrow(
      /read-only/,
    )
    expect(() => live.setValue(['metadata', 'name'], 'somebody-elses')).toThrow(
      /read-only/,
    )
    expect(live.changes.value).toEqual([])
  })
})
