// Noticing a new menu when it comes from a folder.
//
// A new menu is a new pod, so this asks which menu the pods hold, once a second,
// at an address that does not go through krm-foyer. During a rolling update both
// pods answer, which is what the two rules here are for
// (docs/voter-settings-mode-plan.md):
//
//   - It compares each answer with the revision stamped on what is on screen, and
//     not with the answer before it. The refetch can reach the old pod while the
//     poll reached the new one, and then the stamp still differs and it tries
//     again. Acting once on seeing a change would leave a phone on the old menu.
//   - It waits for three identical answers in a row, which sits out the overlap.
//
// The revision is a hash compared with !=, so a rollback is just another change.

import { onMounted, onUnmounted } from 'vue'

export const POLL_MS = 1000
export const AGREE = 3
const POLL_TIMEOUT_MS = 2000

/** The revision the answering pod holds, or undefined for no usable answer. A pod
 *  older than this feature answers with the SPA's HTML and a 200, so a body that
 *  is not the expected JSON counts as no answer. */
export async function readRevision(): Promise<string | undefined> {
  try {
    const res = await fetch('/revision.json', {
      cache: 'no-store',
      signal: AbortSignal.timeout(POLL_TIMEOUT_MS),
    })
    if (!res.ok) return undefined
    const body = (await res.json()) as { revision?: unknown }
    return typeof body.revision === 'string' ? body.revision : undefined
  } catch {
    return undefined
  }
}

export type MenuRevisionPoll = {
  read: () => Promise<string | undefined>
  /** The revision stamped on what is on screen. */
  shown: () => string | undefined
  refresh: () => Promise<void>
  hidden: () => boolean
}

/** Starts polling, and returns how to stop. The next poll is scheduled after each
 *  answer, so slow answers never pile up. */
export function startMenuRevisionPoll(poll: MenuRevisionPoll): () => void {
  let last: string | undefined
  let agreed = 0
  let stopped = false
  let timer: ReturnType<typeof setTimeout> | undefined

  async function tick() {
    if (!poll.hidden()) {
      const seen = await poll.read()
      if (seen !== undefined) {
        agreed = seen === last ? agreed + 1 : 1
        last = seen
        if (agreed >= AGREE && seen !== poll.shown()) {
          // Starting the count again means a refetch that keeps failing is tried
          // every three seconds and not every second.
          agreed = 0
          await poll.refresh()
        }
      }
    }
    if (!stopped) timer = setTimeout(() => void tick(), POLL_MS)
  }

  void tick()
  return () => {
    stopped = true
    clearTimeout(timer)
  }
}

export function useMenuRevision(
  shown: MenuRevisionPoll['shown'],
  refresh: MenuRevisionPoll['refresh'],
): void {
  let stop: (() => void) | undefined
  onMounted(() => {
    stop = startMenuRevisionPoll({
      read: readRevision,
      shown,
      refresh,
      hidden: () => document.hidden,
    })
  })
  onUnmounted(() => stop?.())
}
