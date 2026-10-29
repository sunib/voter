import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import type { StreamEvent } from '@configbutler/krm-stream'
import { DEBOUNCE_MS, onGrantChange } from './grantChanges'
import { useTestAppConfig } from './testAppConfig'

// When a page asks "what may I do?" again. Getting this wrong in one direction
// is a phone that keeps refusing a menu the operator just handed out; in the
// other, 200 phones asking every 4 seconds for an answer that did not change.

const binding = (name: string, resourceVersion = '1') => ({
  apiVersion: 'rbac.authorization.k8s.io/v1',
  kind: 'RoleBinding',
  metadata: { uid: name, name, namespace: 'voter', resourceVersion },
  roleRef: { apiGroup: 'rbac.authorization.k8s.io', kind: 'Role', name },
})
// A deletion carries who was deleted, not the object.
const gone = (name: string) => ({
  uid: name,
  apiVersion: 'rbac.authorization.k8s.io/v1',
  kind: 'RoleBinding',
  name,
  namespace: 'voter',
})

const scopes: EffectScope[] = []

async function fixture() {
  let controller: ReadableStreamDefaultController<Uint8Array> | undefined
  let seq = 0
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
  const subscribe = (pollMs = 60_000) => {
    const refresh = vi.fn()
    const scope = effectScope()
    scopes.push(scope)
    const { mode } = scope.run(() => onGrantChange(refresh, pollMs))!
    return { refresh, mode, stop: () => scope.stop() }
  }
  // Events can only be sent once the stream has been opened.
  const connected = () => vi.waitFor(() => expect(controller).toBeDefined())
  // Several events in one chunk, as a server's snapshot reaches a browser: no
  // chance for anything to run between them.
  const events = async (...es: Omit<StreamEvent, 'seq'>[]) => {
    controller!.enqueue(
      new TextEncoder().encode(
        es
          .map((e) => `data: ${JSON.stringify({ ...e, seq: ++seq })}\n\n`)
          .join(''),
      ),
    )
    for (let i = 0; i < 8; i++) await Promise.resolve()
  }
  const event = (e: Omit<StreamEvent, 'seq'>) => events(e)
  const snapshot = (...items: ReturnType<typeof binding>[]) =>
    events(
      { type: 'reset' },
      ...items.map((object) => ({ type: 'added' as const, object })),
      { type: 'synced' },
    )
  return {
    fetch,
    subscribe: (pollMs?: number) => {
      const subscriber = subscribe(pollMs)
      return connected().then(() => subscriber)
    },
    event,
    snapshot,
    drop: () => controller!.close(),
  }
}

// Long enough for a debounced refresh to have fired if one was coming.
const settle = () => new Promise((r) => setTimeout(r, DEBOUNCE_MS + 100))

beforeEach(async () => {
  await useTestAppConfig()
})

afterEach(() => {
  while (scopes.length) scopes.pop()!.stop()
  vi.unstubAllGlobals()
})

it('watches every RoleBinding in the namespace, not one by name', async () => {
  const { fetch, subscribe } = await fixture()
  await subscribe()
  await vi.waitFor(() => expect(fetch).toHaveBeenCalled())
  const url = String(fetch.mock.calls[0]![0])
  expect(url).toContain('group=rbac.authorization.k8s.io')
  expect(url).toContain('resource=rolebindings')
  expect(url).toContain('namespace=voter')
  // Binding names follow the door question's answers; a name here would miss
  // every ballot binding the operator makes.
  expect(url).not.toContain('name=')
})

it('does not ask again for the first snapshot: the page just asked', async () => {
  const { subscribe, snapshot } = await fixture()
  const { refresh, mode } = await subscribe()
  await snapshot(binding('voter-audience'))
  await settle()
  expect(refresh).not.toHaveBeenCalled()
  expect(mode.value).toBe('watch')
})

it('asks again once for a burst of binding changes', async () => {
  const { subscribe, snapshot, event } = await fixture()
  const { refresh } = await subscribe()
  await snapshot(binding('voter-audience'))

  // One switch on /room: a binding made, another removed.
  await event({ type: 'added', object: binding('voter-audience-coffee-admin') })
  await event({
    type: 'modified',
    object: binding('voter-audience', '2'),
  })
  await event({ type: 'deleted', identity: gone('voter-audience') })
  expect(refresh).not.toHaveBeenCalled()
  await vi.waitFor(() => expect(refresh).toHaveBeenCalledTimes(1))
  await settle()
  expect(refresh).toHaveBeenCalledTimes(1)
})

it('asks again after a reconnect, in case it missed a change', async () => {
  const { fetch, subscribe, snapshot, drop } = await fixture()
  const { refresh } = await subscribe()
  await snapshot(binding('voter-audience'))

  drop()
  await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2), {
    timeout: 5000,
  })
  await snapshot(binding('voter-audience'))
  await vi.waitFor(() => expect(refresh).toHaveBeenCalledTimes(1))
})

it('falls back to the caller’s interval when the stream is refused', async () => {
  const { subscribe, event } = await fixture()
  const { refresh, mode } = await subscribe(50)
  await event({
    type: 'error',
    code: 'FORBIDDEN',
    message: 'rolebindings.rbac.authorization.k8s.io is forbidden',
    terminal: true,
  })
  await vi.waitFor(() => expect(mode.value).toBe('poll'))
  await vi.waitFor(() => expect(refresh.mock.calls.length).toBeGreaterThan(2))
})

it('shares one stream across a page, and closes it with the last one', async () => {
  const { fetch, subscribe, snapshot, event } = await fixture()
  const table = await subscribe()
  const operator = await subscribe()
  await snapshot()
  expect(fetch).toHaveBeenCalledTimes(1)

  await event({ type: 'added', object: binding('voter-audience-coffee-admin') })
  await vi.waitFor(() => {
    expect(table.refresh).toHaveBeenCalledTimes(1)
    expect(operator.refresh).toHaveBeenCalledTimes(1)
  })

  table.stop()
  await event({
    type: 'deleted',
    identity: gone('voter-audience-coffee-admin'),
  })
  await vi.waitFor(() => expect(operator.refresh).toHaveBeenCalledTimes(2))
  expect(table.refresh).toHaveBeenCalledTimes(1)

  // A page opened after the last one closed gets a stream of its own.
  operator.stop()
  await subscribe()
  await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2))
})
