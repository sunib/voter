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
  copy: {
    invalid: string
    replaced: string
    refreshed: string
    alreadyDone: string
    lostTheRace: string
  }
}

/** How many times one press of Save may reach the API server.
 *
 *  A save carries the resourceVersion the editor was holding, so two people
 *  saving the same object in the same second means one of them gets a 409. That
 *  is correct, and on stage it is also the ordinary case rather than the rare
 *  one: the whole room shares a single CoffeeConfig, and on 2026-09-17 twenty-six
 *  saves landed in six minutes with three pairs inside the same second.
 *
 *  When the losing edit does not touch what the winning one changed there is
 *  nothing for a person to decide, so the editor re-reads and sends again
 *  instead of asking. A genuine field-level conflict still stops and still shows
 *  its markers -- two people editing the same price is a thing this demo is
 *  about, and it stays visible. A bare collision is not that, and was only ever
 *  noise.
 *
 *  Bounded rather than open-ended: a room that never stops saving must not
 *  produce a browser that never stops trying. */
const saveAttempts = 3

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
  /** The CommitRequest a save just created, for whoever wants to follow it. */
  const commitRequest = ref('')
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
      for (let attempt = 1; ; attempt++) {
        // Re-captured every attempt, and safe to: captureSave reads the draft
        // and the store's current server version without consuming either, so a
        // retry sends the same edits against the version that just won.
        const intent = store.captureSave(uid.value!)
        if (!intent) {
          // Nothing left to send. On a retry that means the change arrived by
          // somebody else's hand while this editor was losing the race, which is
          // a result worth reporting rather than a silent no-op.
          if (attempt > 1) notice.value = copy.alreadyDone
          return
        }
        try {
          const receipt = await write(intent, reason)
          if (disposed) return
          // Only a FAILURE is worth a sentence here. Acceptance is not news,
          // and saying it froze the screen on a half-truth: the commit landed
          // seconds later and nothing came back to say so. The name is the
          // thread the screen pulls instead -- see api/commitStatus.ts.
          commitNotice.value = receipt.commitError ?? ''
          commitRequest.value = receipt.commitRequested
            ? (receipt.commitRequest ?? '')
            : ''
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
          return
        } catch (cause) {
          if (disposed) return
          if ((cause as ApiError).status !== 409) throw cause
          // Somebody saved first. Take their version and look at what they
          // touched. refreshFromServer reports false for the cases a retry must
          // not paper over -- the object was replaced, or the live stream
          // overtook the read -- and each of those leaves its own notice.
          const reconciled = await refreshFromServer()
          if (!reconciled || conflicts.value.length > 0) return
          if (attempt === saveAttempts) {
            error.value = copy.lostTheRace
            return
          }
        }
      }
    } catch (cause) {
      if (disposed) return
      error.value = (cause as Error).message
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
    commitRequest,
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
