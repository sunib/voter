// Read-only live views, for the parts of the demo that only need to watch.
//
// liveCoffeeConfig.ts is the editing binding: drafts, conflicts, save capture,
// reconciliation. None of that applies to a round list or a rotating join code,
// where the server is the only writer and the browser's job is to redraw. This
// is the small half of the same library -- one store, one subscription, copy the
// objects out on change.
import { onScopeDispose, ref, shallowRef } from 'vue'
import {
  LiveResourceStore,
  applyStreamEvent,
  connectResourceStream,
  readOnlyPolicy,
  resourceStreamURL,
  type ConnectionState,
  type ErrorCode,
  type KRMObject,
} from '@configbutler/krm-stream'

export interface LiveScope {
  group: string
  version: string
  resource: string
  namespace: string
  /** Omit for every object of this kind in the namespace. */
  name?: string
}

/**
 * Watches a scope through krm-foyer's /stream/v1 and keeps `items` current.
 * Kubernetes decides whether this identity may read it -- a refusal arrives as a terminal state, not an
 * exception, because "you may not watch this" is an answer the caller renders.
 */
export function useLiveResources(scope: LiveScope) {
  const store = new LiveResourceStore(readOnlyPolicy)
  const items = shallowRef<KRMObject[]>([])
  const state = shallowRef<Readonly<ConnectionState>>({
    status: 'connecting',
    retries: 0,
  })
  // The gateway reports a CODE, a message and whether it is terminal -- three
  // separate things, and collapsing them loses the only ones worth showing. The
  // first version of this kept the code alone, so a broken shared watch reached
  // the operator's screen as the single word "INTERNAL" underneath a heading
  // that told them they lacked permission.
  const error = ref('')
  const errorCode = ref<ErrorCode | ''>('')
  const terminal = ref(false)

  const url = resourceStreamURL('/stream/v1', scope)

  function refresh() {
    // Copy out on every change: the library prunes its own store on delete, so
    // holding its objects would leave the page rendering something that is gone.
    items.value = store.ids().map((id) => store.server(id))
  }

  const stopSubscription = store.subscribe(refresh)

  const handle = connectResourceStream(
    url,
    (event) => {
      applyStreamEvent(store, event)
    },
    {
      onError(code: ErrorCode, message: string, isTerminal: boolean) {
        errorCode.value = code
        error.value = message || code
        terminal.value = isTerminal
      },
    },
  )
  function showState(next: Readonly<ConnectionState>) {
    state.value = next
    if (next.status === 'live') {
      error.value = ''
    }
  }
  showState(handle.state)
  const stopState = handle.subscribe(showState)
  // Only a throw in the callbacks above rejects this. It ends the stream, so
  // report it as a terminal fault rather than as an unhandled rejection.
  handle.closed.catch((cause: unknown) => {
    errorCode.value = 'INTERNAL'
    error.value = (cause as Error).message
    terminal.value = true
  })

  onScopeDispose(() => {
    stopSubscription()
    stopState()
    handle.close()
  })

  return {
    items,
    state,
    error,
    errorCode,
    /** True once the stream has delivered a snapshot and is following changes. */
    synced: () => state.value.status === 'live',
    /** This identity may not read the scope. Distinct from a fault: it is the
     *  answer Kubernetes gave, and pages are expected to render it. */
    denied: () => errorCode.value === 'FORBIDDEN',
    /** The session, not the permission, is the problem. */
    expired: () => errorCode.value === 'UNAUTHENTICATED',
    /** Terminal and not one of the two above: something is broken, not refused. */
    faulted: () =>
      terminal.value &&
      errorCode.value !== '' &&
      errorCode.value !== 'FORBIDDEN' &&
      errorCode.value !== 'UNAUTHENTICATED',
  }
}
