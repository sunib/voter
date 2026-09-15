// A live view of what this identity may do.
//
// The operator can widen the audience's grant mid-demo, and every phone in the
// room has to notice without anyone refreshing anything. So this polls.
//
// Polling rather than the SSE stream is deliberate, and it is not laziness. The
// stream's scope allowlist (voter/participant_stream.go) denies by default and
// lists exactly the objects a participant may watch; RBAC is not on it, and a
// participant has no permission to watch RoleBindings anyway. Widening either
// to make a permission table live would undo the thing the table is there to
// demonstrate. A SelfSubjectRulesReview is one cheap call, and at this interval
// the room sees the change land within a few seconds of the switch.

import { onScopeDispose, ref, shallowRef, type Ref, type ShallowRef } from 'vue'

import { getAuthorization, type Authorization } from './authz'

/** Slow enough not to matter for 300 phones, fast enough that the room sees the
 *  switch take effect while the operator is still talking about it. */
const POLL_INTERVAL_MS = 4000

export interface LiveAuthorization {
  authz: ShallowRef<Authorization | null>
  error: Ref<string>
  /** True until the first answer arrives, so a page can avoid announcing a
   *  refusal it has not actually been told about yet. */
  loading: Ref<boolean>
  refresh: () => Promise<void>
}

/**
 * Polls /auth/rules for as long as the calling scope lives.
 *
 * Errors are kept in `error` rather than thrown: losing the permission table is
 * not a reason to break the page it is on, and the most likely cause is an
 * expired session, which every other screen already reports its own way. The
 * last good answer is kept on screen when a refresh fails, because a table that
 * empties itself on one dropped request reads as "your permissions were taken
 * away" — the opposite of what happened.
 */
export function useAuthorization(): LiveAuthorization {
  const authz: ShallowRef<Authorization | null> = shallowRef(null)
  const error = ref('')
  const loading = ref(true)

  let timer: ReturnType<typeof setInterval> | undefined
  let disposed = false

  async function refresh(): Promise<void> {
    try {
      const next = await getAuthorization()
      if (disposed) return
      authz.value = next
      error.value = ''
    } catch (cause) {
      if (disposed) return
      error.value =
        cause instanceof Error ? cause.message : 'Could not read permissions.'
    } finally {
      if (!disposed) loading.value = false
    }
  }

  void refresh()
  timer = setInterval(() => void refresh(), POLL_INTERVAL_MS)

  onScopeDispose(() => {
    disposed = true
    if (timer !== undefined) clearInterval(timer)
  })

  return { authz, error, loading, refresh }
}
