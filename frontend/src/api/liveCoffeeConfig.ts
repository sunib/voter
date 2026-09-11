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
} from '@configbutler/krm-stream'
import {
  getAdminCoffeeConfig,
  patchAdminCoffeeConfig,
  type ApiError,
} from './coffee'
import type { CoffeeConfig } from './coffeeTypes'

function coffeeResource(object: unknown): object is KRMObject & CoffeeConfig {
  if (!object || typeof object !== 'object') return false
  const resource = object as KRMObject
  return (
    resource.apiVersion === 'examples.configbutler.ai/v1alpha1' &&
    resource.kind === 'CoffeeConfig' &&
    typeof resource.metadata?.uid === 'string' &&
    typeof resource.metadata.resourceVersion === 'string' &&
    !!resource.spec &&
    typeof resource.spec === 'object' &&
    !Array.isArray(resource.spec)
  )
}

/** Thin host binding: the library owns all draft, conflict and recovery mechanics. */
export function useLiveCoffeeConfig(
  namespace: string,
  name: string,
  editable = false,
) {
  const store = new LiveResourceStore(
    editable ? regionPolicy([['spec']]) : readOnlyPolicy,
  )
  const uid = ref<string>()
  const draft = shallowRef<CoffeeConfig | null>(null)
  const server = shallowRef<CoffeeConfig | null>(null)
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
  const recoveryDraft = shallowRef<CoffeeConfig | null>(null)
  const needsRead = ref(false)
  let disposed = false
  let handle: ManagedStreamHandle | undefined
  let recoveryTimer: ReturnType<typeof setTimeout> | undefined
  const url = resourceStreamURL('/public/stream', {
    group: 'examples.configbutler.ai',
    version: 'v1alpha1',
    resource: 'coffeeconfigs',
    namespace,
    name,
  })
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
      !coffeeResource(next) ||
      next.metadata.namespace !== namespace ||
      next.metadata.name !== name
    ) {
      error.value = 'The stream returned an invalid CoffeeConfig.'
      draft.value = null
      return
    }
    draft.value = next
    server.value = store.server(id) as KRMObject & CoffeeConfig
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
    const object = await getAdminCoffeeConfig()
    if (disposed) return false
    if (!coffeeResource(object) || object.metadata.uid !== id || !available()) {
      notice.value =
        'This configuration was removed or replaced. Open a new editor.'
      return false
    }
    // CoffeeConfig has no projected Secret payload. Omitted metadata retains
    // stream redaction protections; never invent revision counters for a GET.
    const accepted = reconcile(object)
    needsRead.value = !accepted || !synced.value
    notice.value = needsRead.value
      ? 'Live updates overtook the read. Refresh again after reconnecting.'
      : conflicts.value.length
        ? 'Another editor changed the same fields. Review the highlighted conflicts.'
        : 'Configuration refreshed. Your edits are intact; review and save again.'
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
      const receipt = await patchAdminCoffeeConfig(intent, { reason })
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
    takeTheirs,
    keepMine,
    save,
    refreshFromServer,
    reconnect,
    close,
  }
}
