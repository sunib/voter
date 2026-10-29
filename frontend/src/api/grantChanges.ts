// When to ask "what may I do?" again.
//
// Every live change to what the room may do is a RoleBinding in the app's
// namespace that the operator page creates or deletes, and an admission policy
// (voter/config/admission/rolebinding-roleref-policy.yaml) keeps every one of
// them pointing at a Role in that namespace. So the bindings are the whole
// story, and a page can watch them instead of asking every few seconds
// (docs/runbook-watchable-permissions.md).
//
// One stream per page, however many components ask: the permission table, the
// operator's switches and PickSomeone all share it, and krm-foyer shares it
// again across the room. The answer itself still comes from the caller's own
// request (a rules review, a GET of a binding); this only says when to ask.
//
// The stream is an optimisation, never a dependency. Refused, faulted or
// retrying too often, a page falls back to the interval it had before.
import {
  effectScope,
  onScopeDispose,
  ref,
  watch,
  type EffectScope,
  type Ref,
} from 'vue'

import { appConfig } from './appConfig'
import { useLiveResources } from './liveResources'

/** `watch`: asked again when a binding changes, and on a slow backup timer.
 *  `poll`: the stream is not available, so asked on the caller's interval. */
export type GrantMode = 'watch' | 'poll'

/** Role edits arrive through Git and nobody watches Roles; a ClusterRoleBinding
 *  added by hand, or a stream that dropped an event, is caught here too. */
export const BACKUP_POLL_MS = 120_000
/** One switch can touch several bindings; the room asks once. */
export const DEBOUNCE_MS = 300
/** Retries before a page stops waiting for the stream and polls instead. It
 *  goes back to watching as soon as the stream is live again. */
export const RETRIES_BEFORE_POLLING = 3

interface SharedWatch {
  scope: EffectScope
  mode: Ref<GrantMode>
  listeners: Set<() => void>
}

let shared: SharedWatch | undefined
let holders = 0

function openSharedWatch(): SharedWatch {
  const scope = effectScope(true)
  const listeners = new Set<() => void>()
  const mode = ref<GrantMode>('watch')
  scope.run(() => {
    const live = useLiveResources({
      group: 'rbac.authorization.k8s.io',
      version: 'v1',
      resource: 'rolebindings',
      namespace: appConfig().namespace,
    })

    let debounce: ReturnType<typeof setTimeout> | undefined
    const changed = () => {
      clearTimeout(debounce)
      debounce = setTimeout(() => {
        for (const listener of listeners) listener()
      }, DEBOUNCE_MS)
    }
    onScopeDispose(() => clearTimeout(debounce))

    // The first snapshot is what the callers' own first request already
    // answered. Every snapshot after it follows a reconnect, and the stream
    // may have missed events in between, so that one counts as a change.
    //
    // Both watchers run synchronously. A snapshot arrives in one chunk, and a
    // batched watcher would see its items only after the stream had gone live,
    // and count the snapshot itself as a change.
    let wasLive = false
    const now = { flush: 'sync' } as const
    watch(
      live.state,
      (next) => {
        if (next.status === 'live') {
          if (wasLive) changed()
          wasLive = true
          mode.value = 'watch'
          return
        }
        if (
          next.status === 'terminal' ||
          next.status === 'exhausted' ||
          next.status === 'closed' ||
          next.retries >= RETRIES_BEFORE_POLLING
        ) {
          mode.value = 'poll'
        }
      },
      now,
    )
    watch(
      live.items,
      () => {
        if (live.synced()) changed()
      },
      now,
    )
  })
  return { scope, mode, listeners }
}

/**
 * Calls `refresh` when a RoleBinding in the app's namespace may have changed,
 * for as long as the calling scope lives. Without the stream, calls it every
 * `pollMs` instead, as the caller did before there was one.
 */
export function onGrantChange(
  refresh: () => void,
  pollMs: number,
): { mode: Readonly<Ref<GrantMode>> } {
  shared ??= openSharedWatch()
  holders++
  const { mode, listeners } = shared
  listeners.add(refresh)

  let timer: ReturnType<typeof setInterval> | undefined
  const stopMode = watch(
    mode,
    (next) => {
      clearInterval(timer)
      timer = setInterval(refresh, next === 'watch' ? BACKUP_POLL_MS : pollMs)
    },
    { immediate: true },
  )

  onScopeDispose(() => {
    stopMode()
    clearInterval(timer)
    listeners.delete(refresh)
    if (--holders === 0) {
      shared?.scope.stop()
      shared = undefined
    }
  })

  return { mode }
}
