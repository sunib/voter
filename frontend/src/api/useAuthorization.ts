// A live view of what this identity may do.
//
// The operator can widen the audience's grant mid-demo, and every phone in the
// room has to notice without anyone refreshing anything. The answer is a
// SelfSubjectRulesReview, asked again whenever a RoleBinding in the namespace
// changes (grantChanges.ts): Kubernetes' own RBAC, not a copy kept in sync.
// Where that stream is unavailable, it is asked every few seconds instead.

import { onScopeDispose, ref, shallowRef, type Ref, type ShallowRef } from 'vue'

import { getAuthorization, type Authorization } from './authz'
import { onGrantChange, type GrantMode } from './grantChanges'

/** Without the stream: slow enough not to matter for 300 phones, fast enough
 *  that the room sees the switch take effect while the operator is still
 *  talking about it. */
const POLL_INTERVAL_MS = 4000

export interface LiveAuthorization {
  authz: ShallowRef<Authorization | null>
  error: Ref<string>
  /** True until the first answer arrives, so a page can avoid announcing a
   *  refusal it has not actually been told about yet. */
  loading: Ref<boolean>
  refresh: () => Promise<void>
  /** How a change reaches this page; `off` when the caller asks for itself. */
  mode: Readonly<Ref<GrantMode | 'off'>>
}

/**
 * Keeps /auth/rules' answer current for as long as the calling scope lives.
 *
 * Errors are kept in `error` rather than thrown: losing the permission table is
 * not a reason to break the page it is on, and the most likely cause is an
 * expired session, which every other screen already reports its own way. The
 * last good answer is kept on screen when a refresh fails, because a table that
 * empties itself on one dropped request reads as "your permissions were taken
 * away" — the opposite of what happened.
 */
export function useAuthorization(
  options: { poll?: boolean } = {},
): LiveAuthorization {
  const authz: ShallowRef<Authorization | null> = shallowRef(null)
  const error = ref('')
  const loading = ref(true)

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
  // A page that knows when its answer may have changed asks then instead: the
  // vote page re-asks when the round's ballot-groups marker moves.
  const mode =
    (options.poll ?? true)
      ? onGrantChange(() => void refresh(), POLL_INTERVAL_MS).mode
      : ref<'off'>('off')

  onScopeDispose(() => {
    disposed = true
  })

  return { authz, error, loading, refresh, mode }
}
