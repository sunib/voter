import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { POLL_MS, readRevision, startMenuRevisionPoll } from './menuRevision'

// A scripted run: each poll takes the next answer, and the last one repeats.
function fixture(answers: (string | undefined)[], initiallyShown = 'A') {
  let at = 0
  const state = { shown: initiallyShown as string | undefined, hidden: false }
  const read = vi.fn(async () => answers[Math.min(at++, answers.length - 1)])
  const refresh = vi.fn(async () => {})
  const stop = startMenuRevisionPoll({
    read,
    refresh,
    shown: () => state.shown,
    hidden: () => state.hidden,
  })
  return { read, refresh, state, stop }
}

const polls = (count: number) => vi.advanceTimersByTimeAsync(count * POLL_MS)

describe('startMenuRevisionPoll', () => {
  let running: (() => void) | undefined

  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    running?.()
    vi.useRealTimers()
  })

  it('does nothing while the answer matches what is on screen', async () => {
    const run = fixture(['A'])
    running = run.stop
    await polls(10)
    expect(run.refresh).not.toHaveBeenCalled()
  })

  it('sits out both pods answering, then refreshes on three in a row', async () => {
    const run = fixture(['A', 'B', 'A', 'B', 'A', 'B', 'B', 'B'])
    running = run.stop
    await polls(6)
    expect(run.refresh).not.toHaveBeenCalled()
    await polls(1)
    expect(run.refresh).toHaveBeenCalledTimes(1)
  })

  it('tries again when the refetch reached the old pod, and stops once it did not', async () => {
    const run = fixture(['B'])
    running = run.stop
    await polls(2)
    expect(run.refresh).toHaveBeenCalledTimes(1)
    // The stamp still says A, so it must not settle.
    await polls(3)
    expect(run.refresh).toHaveBeenCalledTimes(2)
    run.state.shown = 'B'
    await polls(10)
    expect(run.refresh).toHaveBeenCalledTimes(2)
  })

  it('treats a rollback as a change like any other', async () => {
    const run = fixture(['A'], 'B')
    running = run.stop
    await polls(2)
    expect(run.refresh).toHaveBeenCalledTimes(1)
  })

  it('does not count a poll without an answer', async () => {
    const run = fixture(['B', 'B', undefined, undefined, undefined])
    running = run.stop
    await polls(10)
    expect(run.refresh).not.toHaveBeenCalled()
  })

  it('asks nothing while the page is hidden', async () => {
    const run = fixture(['A'])
    running = run.stop
    run.state.hidden = true
    await polls(1)
    const before = run.read.mock.calls.length
    await polls(10)
    expect(run.read).toHaveBeenCalledTimes(before)
    run.state.hidden = false
    await polls(1)
    expect(run.read.mock.calls.length).toBeGreaterThan(before)
  })

  it('stops when told to', async () => {
    const run = fixture(['A'])
    await polls(1)
    run.stop()
    const before = run.read.mock.calls.length
    await polls(10)
    expect(run.read).toHaveBeenCalledTimes(before)
  })
})

describe('readRevision', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  const answering = (response: Response | Error) =>
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        if (response instanceof Error) throw response
        return response
      }),
    )

  it('reads the revision', async () => {
    answering(new Response(JSON.stringify({ revision: 'abc' })))
    expect(await readRevision()).toBe('abc')
  })

  it('accepts the empty revision of a menu from Kubernetes', async () => {
    answering(new Response(JSON.stringify({ revision: '' })))
    expect(await readRevision()).toBe('')
  })

  it('has no answer from a pod older than this feature: HTML with a 200', async () => {
    answering(new Response('<!doctype html><title>Coffee</title>'))
    expect(await readRevision()).toBeUndefined()
  })

  it('has no answer from a 502', async () => {
    answering(new Response('Bad Gateway', { status: 502 }))
    expect(await readRevision()).toBeUndefined()
  })

  it('has no answer from a network failure', async () => {
    answering(new TypeError('failed to fetch'))
    expect(await readRevision()).toBeUndefined()
  })
})
