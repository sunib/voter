import { onBeforeUnmount, ref, shallowRef, type Ref } from 'vue'

import {
  LiveResourceStore,
  connectWithEventSource,
  resourceStreamURL,
  type Path,
  type StreamHandle,
} from '@configbutler/krm-stream'

import type { CoffeeConfig } from './coffeeTypes'

// Live CoffeeConfig, shared by the storefront and the editor.
//
// This is the part of the demo that has to be seen: someone changes the menu on
// stage and it appears on every phone in the room, with nobody refreshing.
//
// The transport is native EventSource, deliberately. EventSource cannot send an
// Authorization header, which is exactly why the backend authenticates this
// stream with the same-origin HttpOnly session cookie the rest of the app
// already uses -- no token in JavaScript, nothing an XSS can read.
//
// LiveResourceStore is what makes the editor safe while the stream is running.
// It keeps the server's object and the local draft apart, so an incoming change
// is merged into what someone is typing rather than overwriting it, and a field
// they actually edited that the server also moved is reported as a conflict
// instead of being silently resolved. That machinery used to be hand-rolled
// here; it now belongs to a library that has conformance fixtures for it.

/** The scope this application streams. The backend allowlists exactly this and
 *  refuses anything else, so widening here alone changes nothing. */
const COFFEE_SCOPE = {
  group: 'examples.configbutler.ai',
  version: 'v1alpha1',
  resource: 'coffeeconfigs',
} as const

export type LiveCoffeeConfig = {
  /** The server's object with local edits merged over it: what a UI renders. */
  draft: Ref<CoffeeConfig | null>
  /** Server truth, with no local edits. */
  server: Ref<CoffeeConfig | null>
  /** True once the first snapshot has completed. */
  synced: Ref<boolean>
  /** Set when the stream failed terminally; the caller should fall back to a
   *  plain read rather than pretending to be live. */
  error: Ref<string>
  /** Paths the server moved since the last render — for a highlight. */
  flashed: Ref<Path[]>
  /** Paths where a local edit and a server change disagree. */
  conflicts: Ref<Path[]>

  setValue: (path: Path, value: unknown) => void
  isDirty: (path: Path) => boolean
  takeTheirs: (path: Path) => void
  /** An RFC 7386 merge patch of just the local edits, or null. */
  patch: () => Record<string, unknown> | null
  /** Adopt the object a save returned, so the field stops reading as dirty
   *  without waiting for the watch to echo. */
  adoptSaved: (object: CoffeeConfig) => void
  close: () => void
}

/**
 * Connect to the live stream for one CoffeeConfig.
 *
 * `namespace` and `name` address the object. Both come from the server (the
 * session reports the namespace), never from user input: the backend would
 * refuse an out-of-scope request anyway, but there is no reason to make one.
 */
export function useLiveCoffeeConfig(
  namespace: string,
  name: string,
): LiveCoffeeConfig {
  const store = new LiveResourceStore()

  const draft = shallowRef<CoffeeConfig | null>(null)
  const server = shallowRef<CoffeeConfig | null>(null)
  const synced = ref(false)
  const error = ref('')
  const flashed = shallowRef<Path[]>([])
  const conflicts = shallowRef<Path[]>([])

  // The stream addresses ONE object by name. A namespace-wide watch would hand
  // this browser every CoffeeConfig in the namespace, which is more than the
  // screen needs and more than it should receive.
  const url = resourceStreamURL('/public/stream', {
    ...COFFEE_SCOPE,
    namespace,
    name,
  })

  /** The single object's uid, once the snapshot has named it. */
  function currentId(): string | undefined {
    return store.ids()[0]
  }

  function refresh() {
    const id = currentId()
    if (id === undefined) {
      draft.value = null
      server.value = null
      return
    }
    draft.value = store.draft(id) as unknown as CoffeeConfig
    server.value = store.server(id) as unknown as CoffeeConfig
    conflicts.value = store.conflicts(id).map((c) => c.path)
  }

  let handle: StreamHandle | undefined = connectWithEventSource(url, store, {
    onChange: (change) => {
      flashed.value = change.flashed
      refresh()
    },
    onSynced: () => {
      synced.value = true
      refresh()
    },
    onError: (code, message, terminal) => {
      // A terminal error has already closed the connection. Retrying it is the
      // bug, not the fix — so record it and let the screen fall back.
      if (terminal) {
        error.value = `${code}: ${message}`
        synced.value = false
      }
    },
    onGap: () => {
      // A dropped frame means the store may be stale. EventSource reconnects on
      // its own and the next snapshot repairs it; say nothing to the user.
      synced.value = false
    },
  })

  const close = () => {
    handle?.close()
    handle = undefined
  }
  onBeforeUnmount(close)

  return {
    draft,
    server,
    synced,
    error,
    flashed,
    conflicts,
    setValue: (path, value) => {
      const id = currentId()
      if (id === undefined) return
      store.setValue(id, path, value)
      refresh()
    },
    isDirty: (path) => {
      const id = currentId()
      return id === undefined ? false : store.isDirty(id, path)
    },
    takeTheirs: (path) => {
      const id = currentId()
      if (id === undefined) return
      store.takeTheirs(id, path)
      refresh()
    },
    patch: () => {
      const id = currentId()
      return id === undefined ? null : store.patch(id)
    },
    adoptSaved: (object) => {
      store.adoptSaved(object as never)
      refresh()
    },
    close,
  }
}
