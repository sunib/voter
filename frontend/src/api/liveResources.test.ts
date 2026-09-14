import { afterEach, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import type { StreamEvent } from '@configbutler/krm-stream'
import { useLiveResources } from './liveResources'

// The collection watch behind both the operator page's round controls and the
// participants' round list. Its whole job is to keep `items` equal to what the
// server last said, including when the server says an object is gone -- a stale
// round on a phone is a participant answering a closed question.

const round = (name: string, state: string, uid = name) => ({
  apiVersion: 'examples.configbutler.ai/v1alpha1',
  kind: 'QuizSession',
  metadata: { uid, name, namespace: 'voter', resourceVersion: '1' },
  spec: { title: name, state },
})

let scope: EffectScope

async function fixture() {
  let controller: ReadableStreamDefaultController<Uint8Array>
  let seq = 0
  // The URL is part of the contract this test checks, so the mock has to
  // declare the parameter for it to appear in `mock.calls`.
  const fetch = vi.fn(
    async (_url: string) =>
      new Response(
        new ReadableStream<Uint8Array>({
          start(c) {
            seq = 0
            controller = c
          },
        }),
        { headers: { 'Content-Type': 'text/event-stream' } },
      ),
  )
  vi.stubGlobal('fetch', fetch)
  scope = effectScope()
  const live = scope.run(() =>
    useLiveResources({
      group: 'examples.configbutler.ai',
      version: 'v1alpha1',
      resource: 'quizsessions',
      namespace: 'voter',
    }),
  )!
  await vi.waitFor(() => expect(fetch).toHaveBeenCalled())
  const event = async (e: Omit<StreamEvent, 'seq'>) => {
    controller!.enqueue(
      new TextEncoder().encode(
        `data: ${JSON.stringify({ ...e, seq: ++seq })}\n\n`,
      ),
    )
    for (let i = 0; i < 8; i++) await Promise.resolve()
  }
  const [firstCall] = fetch.mock.calls
  if (!firstCall) throw new Error('the composable opened no stream')
  return { live, event, url: String(firstCall[0]) }
}

afterEach(() => {
  scope?.stop()
  vi.unstubAllGlobals()
})

it('asks for the collection, not a single object', async () => {
  const { url } = await fixture()
  expect(url).toContain('resource=quizsessions')
  expect(url).toContain('namespace=voter')
  // A name here would pin the watch to one round and silently stop new ones
  // from ever appearing on a participant's page.
  expect(url).not.toContain('name=')
})

it('follows rounds as they are added, changed and deleted', async () => {
  const { live, event } = await fixture()

  await event({ type: 'reset' })
  await event({
    type: 'added',
    object: round('warm-up', 'closed'),
    redacted: [],
  })
  await event({ type: 'added', object: round('main', 'closed'), redacted: [] })
  await event({ type: 'synced' })

  await vi.waitFor(() => expect(live.synced()).toBe(true))
  expect(live.items.value.map((o) => o.metadata!.name).sort()).toEqual([
    'main',
    'warm-up',
  ])

  // The operator opens a round: every watching page must see the new state.
  await event({
    type: 'modified',
    object: round('main', 'live'),
    redacted: [],
  })
  await vi.waitFor(() =>
    expect(
      live.items.value.find((o) => o.metadata!.name === 'main')?.spec,
    ).toMatchObject({ state: 'live' }),
  )

  // The library prunes deleted objects from its own store, so a page holding
  // that store's objects would keep rendering one that no longer exists.
  // `deleted` is the one event carrying a tombstone rather than an object:
  // there may not be a complete object left to send.
  await event({
    type: 'deleted',
    identity: {
      uid: 'warm-up',
      apiVersion: 'examples.configbutler.ai/v1alpha1',
      kind: 'QuizSession',
      name: 'warm-up',
      namespace: 'voter',
    },
  })
  await vi.waitFor(() =>
    expect(live.items.value.map((o) => o.metadata!.name)).toEqual(['main']),
  )
})

it('reports not-synced until the snapshot is complete', async () => {
  const { live, event } = await fixture()
  expect(live.synced()).toBe(false)
  await event({ type: 'reset' })
  await event({ type: 'added', object: round('main', 'live'), redacted: [] })
  // Still mid-snapshot: a page that trusted this would render a partial list as
  // if it were the whole room's.
  expect(live.synced()).toBe(false)
  await event({ type: 'synced' })
  await vi.waitFor(() => expect(live.synced()).toBe(true))
})

// The three answers a failing stream can give, and why they must stay apart.
// A participant refused the Room is the demo working; the application's own
// ServiceAccount lacking a watch is the demo broken. Both arrive here as a
// terminal stream, and the first version of this code reported them the same
// way -- which is how a cluster-admin was told they lacked permission.
it('separates a refusal from a fault, keeping the code and the message', async () => {
  const { live, event } = await fixture()
  await event({
    type: 'error',
    code: 'FORBIDDEN',
    message: 'rooms.roompass.configbutler.ai "demo" is forbidden',
    terminal: true,
  })
  await vi.waitFor(() => expect(live.errorCode.value).toBe('FORBIDDEN'))
  expect(live.denied()).toBe(true)
  expect(live.faulted()).toBe(false)
  expect(live.expired()).toBe(false)
  // The message is what names the object; the code alone is not actionable.
  expect(live.error.value).toContain('is forbidden')
})

it('reports a broken shared watch as a fault, not as a refusal', async () => {
  const { live, event } = await fixture()
  await event({
    type: 'error',
    code: 'INTERNAL',
    message: 'watch failed',
    terminal: true,
  })
  await vi.waitFor(() => expect(live.errorCode.value).toBe('INTERNAL'))
  expect(live.faulted()).toBe(true)
  expect(live.denied()).toBe(false)
  expect(live.expired()).toBe(false)
})

it('reports an expired credential as neither refused nor broken', async () => {
  const { live, event } = await fixture()
  await event({
    type: 'error',
    code: 'UNAUTHENTICATED',
    message: 'token expired',
    terminal: true,
  })
  await vi.waitFor(() => expect(live.errorCode.value).toBe('UNAUTHENTICATED'))
  expect(live.expired()).toBe(true)
  expect(live.denied()).toBe(false)
  expect(live.faulted()).toBe(false)
})
