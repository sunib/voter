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
  connectManagedResourceStream,
  readOnlyPolicy,
  resourceStreamURL,
  type ConnectionState,
  type KRMObject,
  type ManagedStreamHandle,
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
 * Watches a scope and keeps `items` current. The backend's allowlist decides
 * which scopes are servable at all, and Kubernetes decides whether this
 * identity may read them -- a refusal arrives as a terminal state, not an
 * exception, because "you may not watch this" is an answer the caller renders.
 */
export function useLiveResources(scope: LiveScope) {
  const store = new LiveResourceStore(readOnlyPolicy)
  const items = shallowRef<KRMObject[]>([])
  const state = shallowRef<Readonly<ConnectionState>>({
    status: 'connecting',
    retries: 0,
  })
  const error = ref('')

  const url = resourceStreamURL('/public/stream', scope)

  function refresh() {
    // Copy out on every change: the library prunes its own store on delete, so
    // holding its objects would leave the page rendering something that is gone.
    items.value = store.ids().map((id) => store.server(id))
  }

  const stopSubscription = store.subscribe(refresh)

  let handle: ManagedStreamHandle | null = null
  handle = connectManagedResourceStream(url, store, {
    onStateChange(next) {
      state.value = next
      if (next.status === 'live') {
        error.value = ''
      }
    },
    onError(e: unknown) {
      error.value = e instanceof Error ? e.message : String(e)
    },
  })

  onScopeDispose(() => {
    stopSubscription()
    handle?.close()
  })

  return {
    items,
    state,
    error,
    /** True once the stream has delivered a snapshot and is following changes. */
    synced: () => state.value.status === 'live',
  }
}
