// Following a save into Git, quietly.
//
// A save returns a RECEIPT: ConfigButler accepted the request, which is not the
// same fact as a commit existing. The editor used to state the receipt and stop
// -- "a Git commit has not yet been observed" -- and because nothing ever looked
// again, that sentence stayed on screen long after the commit had been pushed.
// It was never wrong, it was frozen, which is worse: the one screen in the demo
// whose job is to show that the write became a commit was the screen asserting
// it had not.
//
// So follow the object. The CommitRequest reports its own outcome in status, we
// already stream Kubernetes objects everywhere else, and the participant's token
// already carries get/list/watch on commitrequests -- this needs no new right.
import { effectScope, onScopeDispose, ref, watch, type EffectScope } from 'vue'

import { useLiveResources } from './liveResources'

/** idle: nothing to follow. The screen shows nothing at all in this state. */
export type CommitState = 'idle' | 'pending' | 'committed' | 'failed'

interface Condition {
  type?: string
  status?: string
  reason?: string
  message?: string
}

/**
 * Reads the outcome out of a CommitRequest's status.
 *
 * Ready/True is the commit; Stalled/True is ConfigButler giving up. Anything
 * else is still in flight, INCLUDING an object with no conditions yet, which is
 * what the first event after a create always looks like.
 */
export function readCommitOutcome(object: unknown): {
  state: CommitState
  sha: string
  detail: string
} {
  const status = (object as { status?: Record<string, unknown> })?.status
  const conditions = (status?.conditions as Condition[] | undefined) ?? []
  const of = (type: string) => conditions.find((c) => c?.type === type)
  const sha = typeof status?.sha === 'string' ? status.sha : ''

  const stalled = of('Stalled')
  if (stalled?.status === 'True') {
    return { state: 'failed', sha, detail: stalled.message ?? '' }
  }
  const ready = of('Ready')
  if (ready?.status === 'True') {
    return { state: 'committed', sha, detail: ready.message ?? '' }
  }
  return { state: 'pending', sha: '', detail: '' }
}

/** The first seven of a sha, which is what a commit is called out loud. */
export function shortSha(sha: string): string {
  return sha.slice(0, 7)
}

/**
 * Follows one CommitRequest at a time.
 *
 * Pinned by name, so the stream carries this participant's own receipt and not
 * the room's: the gateway turns a named scope into a metadata.name field
 * selector, and the subscriber is still access-reviewed as itself either way.
 */
export function useCommitStatus(namespace: string) {
  const state = ref<CommitState>('idle')
  const sha = ref('')
  const detail = ref('')
  let scope: EffectScope | undefined

  function stop() {
    scope?.stop()
    scope = undefined
  }

  /** Called with the name from a save receipt. Replaces whatever was followed
   *  before: an editor saved twice is waiting on its LATEST commit. */
  function follow(name: string) {
    stop()
    state.value = 'pending'
    sha.value = ''
    detail.value = ''
    if (!name) {
      state.value = 'idle'
      return
    }
    scope = effectScope()
    scope.run(() => {
      const live = useLiveResources({
        group: 'configbutler.ai',
        version: 'v1alpha3',
        resource: 'commitrequests',
        namespace,
        name,
      })
      watch(
        live.items,
        (items) => {
          const object = items[0]
          if (!object) return
          const outcome = readCommitOutcome(object)
          state.value = outcome.state
          sha.value = outcome.sha
          detail.value = outcome.detail
          // Nothing more will change once it has an outcome, and a stream left
          // open is a watch the API server keeps serving for no reason.
          if (outcome.state !== 'pending') stop()
        },
        { immediate: true },
      )
      watch(
        () => live.error.value,
        (message) => {
          // A refusal or a fault here says nothing about the commit -- the
          // write already succeeded and ConfigButler is working regardless. Go
          // quiet rather than claim a failure that did not happen.
          if (message && state.value === 'pending') state.value = 'idle'
        },
      )
    })
  }

  function clear() {
    stop()
    state.value = 'idle'
    sha.value = ''
    detail.value = ''
  }

  onScopeDispose(stop)
  return { state, sha, detail, follow, clear }
}
