import { computed, onScopeDispose, ref, shallowRef } from 'vue'
import {
  LiveResourceStore,
  connectManagedResourceStream,
  regionPolicy,
  readOnlyPolicy,
  resourceStreamURL,
  get,
  type Change,
  type Conflict,
  type ConnectionState,
  type KRMObject,
  type ManagedStreamHandle,
  type Path,
  type SaveRequest,
} from '@configbutler/krm-stream'
import type { ApiError } from './coffee'
import type { LiveScope } from './liveResources'

/** What a save reports back. `commitRequested` means the request was accepted,
 *  never that a Git commit has been observed. */
export type SaveReceipt = {
  saved: boolean
  commitRequested?: boolean
  commitRequest?: string
  commitError?: string
}

export interface EditableResourceOptions<T> {
  /** The single object this editor follows. */
  scope: LiveScope & { name: string }
  /** False gives a viewer with no draft and no save button. */
  editable?: boolean
  /** A plain read of the same object, used to reconcile after a save or a 409. */
  read: () => Promise<unknown>
  /** Send the captured intent. Only called when `editable`. */
  write: (intent: SaveRequest, reason: string) => Promise<SaveReceipt>
  /** Narrow a streamed object to the kind this editor expects. */
  isExpectedKind: (object: unknown) => object is KRMObject & T
  /** The lines a person reads when something happens to the object underneath
   *  them. In the screen's own words -- a CoffeeConfig is a "configuration" and
   *  a Database request is not, and the generic phrasing that covers both reads
   *  as though it were written for neither. */
  copy: { invalid: string; replaced: string; refreshed: string }
}

/**
 * One live, editable Kubernetes object: draft, conflicts, save capture,
 * reconciliation and recovery.
 *
 * Thin host binding by design — the krm-stream library owns all of those
 * mechanics, and everything here is either Vue plumbing or the one thing the
 * library cannot know, which is what a valid object of this kind looks like and
 * how to read and write it.
 *
 * `liveResources.ts` is the read-only half of the same idea, for pages that
 * only watch. Use that one where there is no draft to keep.
 */
export function useLiveEditableResource<T>(
  options: EditableResourceOptions<T>,
) {
  const { scope, read, write, isExpectedKind, copy } = options
  const editable = options.editable ?? false
  const store = new LiveResourceStore(
    editable ? regionPolicy([['spec']]) : readOnlyPolicy,
  )
  const uid = ref<string>()
  const draft = shallowRef<T | null>(null)
  const server = shallowRef<T | null>(null)
  const changes = shallowRef<Change[]>([])
  const conflicts = shallowRef<Conflict[]>([])
  const redactions = shallowRef<{ path: Path; rev: number }[]>([])
  const flashed = shallowRef<Path[]>([])
  const state = shallowRef<Readonly<ConnectionState>>({
    status: 'connecting',
    retries: 0,
  })
  const error = ref('')
  const notice = ref('')
  const commitNotice = ref('')
  const saving = ref(false)
  const recoveryDraft = shallowRef<T | null>(null)
  const needsRead = ref(false)
  let disposed = false
  let handle: ManagedStreamHandle | undefined
  let recoveryTimer: ReturnType<typeof setTimeout> | undefined
  const url = resourceStreamURL('/public/stream', scope)
  const available = () => !!uid.value && store.ids().includes(uid.value)
  const synced = computed(() => state.value.status === 'live')
  const canSave = computed(
    () =>
      editable &&
      synced.value &&
      !!draft.value &&
      !saving.value &&
      conflicts.value.length === 0,
  )

  function refresh() {
    // Editors keep their first UID; a replacement never inherits unsaved input.
    uid.value ??= store.ids()[0]
    const id = uid.value
    if (!id || !available()) {
      draft.value = null
      server.value = null
      changes.value = []
      conflicts.value = []
      redactions.value = []
      return
    }
    const next = store.draft(id)
    if (
      !isExpectedKind(next) ||
      next.metadata.namespace !== scope.namespace ||
      next.metadata.name !== scope.name
    ) {
      error.value = copy.invalid
      draft.value = null
      return
    }
    draft.value = next
    server.value = store.server(id) as KRMObject & T
    changes.value = store.changes(id)
    conflicts.value = store.conflicts(id)
    redactions.value = store.redactions(id)
    // Copy-out only, captured before deletion prunes the library store.
    recoveryDraft.value = changes.value.length ? next : null
  }
  const stop = store.subscribe(refresh)
  function connect() {
    handle = connectManagedResourceStream(url, store, {
      onStateChange(next) {
        state.value = next
        if (
          next.status === 'live' &&
          /^(UNAUTHENTICATED|FORBIDDEN|UPSTREAM_UNAVAILABLE|INTERNAL):/.test(
            error.value,
          )
        )
          error.value = ''
      },
      onChange(change) {
        flashed.value = change.flashed
      },
      onError(code, message, terminal) {
        if (terminal) error.value = `${code}: ${message}`
      },
    })
  }
  connect()
  async function reconnect() {
    handle?.close()
    await handle?.closed
    if (!disposed) connect()
  }
  async function refreshFromServer(): Promise<boolean> {
    if (!available()) {
      await reconnect()
      return false
    }
    const id = uid.value!
    needsRead.value = true
    const reconcile = store.captureReconciliation(id)
    const object = await read()
    if (disposed) return false
    if (!isExpectedKind(object) || object.metadata.uid !== id || !available()) {
      notice.value = copy.replaced
      return false
    }
    // Neither kind here carries a projected Secret payload. Omitted metadata
    // retains stream redaction protections; never invent revision counters for
    // a GET.
    const accepted = reconcile(object)
    needsRead.value = !accepted || !synced.value
    notice.value = needsRead.value
      ? 'Live updates overtook the read. Refresh again after reconnecting.'
      : conflicts.value.length
        ? 'Another editor changed the same fields. Review the highlighted conflicts.'
        : copy.refreshed
    return !needsRead.value
  }
  async function save(reason: string) {
    if (!canSave.value || !available()) return
    saving.value = true
    error.value = ''
    clearTimeout(recoveryTimer)
    try {
      if (needsRead.value) {
        await refreshFromServer()
        return
      }
      const intent = store.captureSave(uid.value!)
      if (!intent) return
      const receipt = await write(intent, reason)
      if (disposed) return
      commitNotice.value =
        receipt.commitError ??
        (receipt.commitRequested
          ? 'Commit request accepted. A Git commit has not yet been observed.'
          : '')
      notice.value =
        'Saved to Kubernetes. Waiting for live synchronization; later edits remain unsaved.'
      // One guarded read recovers a missing echo without adopting a save object.
      recoveryTimer = setTimeout(() => {
        void refreshFromServer()
          .then((accepted) => {
            if (accepted)
              notice.value =
                'Saved to Kubernetes. Live view synchronized; any remaining changes are unsaved.'
          })
          .catch((cause: unknown) => {
            error.value = (cause as Error).message
          })
      }, 1500)
    } catch (cause) {
      if (disposed) return
      if ((cause as ApiError).status === 409) {
        try {
          await refreshFromServer()
        } catch (readError) {
          error.value = (readError as Error).message
        }
      } else error.value = (cause as Error).message
    } finally {
      saving.value = false
    }
  }
  function setValue(path: Path, value: unknown) {
    if (available()) store.setValue(uid.value!, path, value)
  }
  function removeKey(path: Path) {
    if (available()) store.removeKey(uid.value!, path)
  }
  function takeTheirs(path: Path) {
    if (available()) store.takeTheirs(uid.value!, path)
  }
  function keepMine(path: Path) {
    if (!available()) return
    const chosen = get(store.draft(uid.value!), path)
    store.takeTheirs(uid.value!, path)
    if (chosen === undefined) store.removeKey(uid.value!, path)
    else store.setValue(uid.value!, path, chosen)
  }
  function close() {
    disposed = true
    clearTimeout(recoveryTimer)
    handle?.close()
    stop()
    recoveryDraft.value = null
  }
  onScopeDispose(close)
  return {
    draft,
    server,
    changes,
    conflicts,
    redactions,
    flashed,
    state,
    synced,
    error,
    notice,
    commitNotice,
    saving,
    canSave,
    recoveryDraft,
    needsRead,
    setValue,
    removeKey,
    takeTheirs,
    keepMine,
    save,
    refreshFromServer,
    reconnect,
    close,
  }
}
