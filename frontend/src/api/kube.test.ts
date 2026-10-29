import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { writeWithCommit } from './kube'
import { useTestAppConfig } from './testAppConfig'

// The CommitRequest goes first and the write waits commitLeadMillis behind it,
// so the request is registered before the window the write opens (kube.ts).
describe('writeWithCommit', () => {
  let posted: string[]

  beforeEach(async () => {
    await useTestAppConfig({ gitTargetName: 'coffee', commitLeadMillis: 500 })
    posted = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        posted.push(url)
        return url.includes('/commitrequests') && url.includes('fail')
          ? new Response('{}', { status: 500 })
          : new Response(JSON.stringify({ metadata: { name: 'made' } }), {
              headers: { 'content-type': 'application/json' },
            })
      }),
    )
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('asks first, and writes only after the lead', async () => {
    const write = vi.fn(async () => 'written')
    const saving = writeWithCommit('coffee', 'coffee-save-', 'why', write)
    await vi.advanceTimersByTimeAsync(499)
    expect(posted).toHaveLength(1)
    expect(write).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    await expect(saving).resolves.toEqual({
      result: 'written',
      receipt: { commitRequested: true, commitRequest: 'made' },
    })
  })

  // Nothing waits for a request that does not exist.
  it('writes at once when no target is named', async () => {
    const write = vi.fn(async () => 'written')
    await expect(writeWithCommit('', 'x-', 'why', write)).resolves.toMatchObject({
      result: 'written',
    })
    expect(posted).toHaveLength(0)
  })

  it('writes at once when the request was refused, and says so', async () => {
    await useTestAppConfig({ commitLeadMillis: 500, commitRequestNamespace: 'fail' })
    const write = vi.fn(async () => 'written')
    const { receipt } = await writeWithCommit('coffee', 'x-', 'why', write)
    expect(write).toHaveBeenCalled()
    expect(receipt.commitRequested).toBe(false)
  })
})
