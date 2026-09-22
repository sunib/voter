<script setup lang="ts">
// One database request, live.
//
// Exactly the coffee menu editor's machinery — same store, same dirty and
// conflict markers, same "why are you making this change?" note, same single
// Save. What differs is the object and the form, and both of those are data:
// api/liveDatabase.ts and api/databaseForm.ts. If this screen and the menu
// editor ever behave differently under a concurrent edit, that is a bug in one
// of them, not a design decision.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { isPrefix, type Path } from '@configbutler/krm-stream'

import AppShell from '../components/layout/AppShell.vue'
import DatabaseFields from '../components/databases/DatabaseFields.vue'
import PermissionRequirements from '../components/PermissionRequirements.vue'
import { DATABASE_REQUIREMENTS } from '../api/authz'
import { useAuthorization } from '../api/useAuthorization'
import { fieldLabel } from '../api/databaseForm'
import { INTENT_ANNOTATION, intentSentence } from '../api/databaseTypes'
import { leafChanges } from '../api/fieldChanges'
import { formatConflictValue } from '../adminFormatters'
import { useLiveDatabase } from '../api/liveDatabase'
import { currentSession } from '../api/session'
import { shortSha, useCommitStatus } from '../api/commitStatus'

const props = defineProps<{ name: string }>()

type FieldState = 'clean' | 'dirty' | 'conflict'
const session = currentSession()!
const { authz, error: authzError, loading: authzLoading } = useAuthorization()

const live = useLiveDatabase(session.namespace, props.name)
const {
  draft,
  server,
  saving,
  error: loadError,
  commitNotice,
  commitRequest,
  notice,
  canSave,
  state,
  recoveryDraft,
} = live

// Follows the receipt the save just handed back until ConfigButler reports the
// commit pushed, so the line under the form is the state of Git rather than the
// state of Git at the instant of the save.
const commit = useCommitStatus(session.namespace)
const { state: commitState, sha: commitSha, detail: commitDetail } = commit
const failedCommitCopy =
  'Your change is saved in Kubernetes. ConfigButler stopped trying to commit it.'
watch(commitRequest, (name) => {
  if (name) commit.follow(name)
})

const loading = computed(
  () => !draft.value && ['connecting', 'syncing'].includes(state.value.status),
)
const changeReason = ref('')
const flashedPaths = ref<Record<string, true>>({})
const flashTimers = new Map<string, ReturnType<typeof setTimeout>>()

// Form paths address fixed Database fields only. Same conversion, and the same
// caveat, as the coffee editor: arbitrary KRM keys never pass through here.
const toPath = (path: string): Path =>
  path.split('.').map((part) => (/^\d+$/.test(part) ? Number(part) : part))

// The library reports an edit inside a container at the container, because that
// is what a merge patch replaces. This screen counts FIELDS, so it reopens them
// first -- see api/fieldChanges.ts.
const fieldChanges = computed(() => leafChanges(live.changes.value))
const dirtyFieldCount = computed(() => fieldChanges.value.length)
const conflictCount = computed(() => live.conflicts.value.length)

const summaryEntries = computed(() =>
  fieldChanges.value.map((change) => {
    const path = change.path.join('.')
    return {
      path,
      label: fieldLabel(path),
      state: fieldState(path),
      draftValue: change.new,
      serverValue: change.old,
    }
  }),
)

const saveButtonLabel = computed(() =>
  saving.value
    ? 'Saving…'
    : live.needsRead.value
      ? 'Refresh before saving'
      : dirtyFieldCount.value
        ? `Save ${dirtyFieldCount.value} change${dirtyFieldCount.value === 1 ? '' : 's'}`
        : 'No changes to save',
)

const connectionMessage = computed(() => {
  if (loadError.value) return loadError.value
  if (state.value.status === 'live')
    return draft.value
      ? 'Live updates connected.'
      : 'This request was removed. Go back to the list.'
  if (state.value.status === 'terminal')
    return 'Live access ended. Sign in again or check your permissions.'
  if (state.value.status === 'exhausted')
    return 'Could not reconnect. Your unsaved changes are retained; retry when ready.'
  return 'Connecting to live updates. Your unsaved changes are retained; saving is paused.'
})

/** The note already on the object, from whoever last saved it. Shown rather
 *  than prefilled into the box: it is somebody else's sentence about a change
 *  that already happened, not a draft of yours. */
const recordedIntent = computed(
  () => server.value?.metadata?.annotations?.[INTENT_ANNOTATION] ?? '',
)

function fieldState(path: string): FieldState {
  if (
    live.conflicts.value.some((conflict) =>
      isPrefix(conflict.path, toPath(path)),
    )
  )
    return 'conflict'
  if (fieldChanges.value.some((change) => isPrefix(change.path, toPath(path))))
    return 'dirty'
  return 'clean'
}

function serverValueFor(path: string): unknown {
  const conflict = live.conflicts.value.find(
    (candidate) => candidate.path.join('.') === path,
  )
  if (conflict) return conflict.theirs
  return serverValue(path)
}

function serverValue(path: string): unknown {
  let cursor: unknown = server.value
  for (const segment of toPath(path)) {
    if (cursor === null || typeof cursor !== 'object') return undefined
    cursor = (cursor as Record<string | number, unknown>)[segment]
  }
  return cursor
}

function updateField(path: string, value: unknown) {
  // `undefined` is how the form says "this optional field is now empty", and a
  // merge patch spells that null -- which the store writes for us on removeKey.
  // Setting the string "" instead would write an empty string the CRD's own
  // patterns then refuse.
  if (value === undefined) live.removeKey(toPath(path))
  else live.setValue(toPath(path), value)
}

function revert(path: string) {
  live.takeTheirs(toPath(path))
}

function keepMine(path: string) {
  live.keepMine(toPath(path))
}

async function save() {
  await live.save(changeReason.value)
}

async function refreshFromServer() {
  try {
    await live.refreshFromServer()
  } catch (cause) {
    loadError.value = (cause as Error).message
  }
}

function flashField(path: string) {
  if (!path) return
  flashedPaths.value = { ...flashedPaths.value, [path]: true }
  const existing = flashTimers.get(path)
  if (existing) clearTimeout(existing)
  flashTimers.set(
    path,
    setTimeout(() => {
      const next = { ...flashedPaths.value }
      delete next[path]
      flashedPaths.value = next
      flashTimers.delete(path)
    }, 1600),
  )
}

watch(live.flashed, (paths) => {
  for (const path of paths) flashField(path.join('.'))
})

onBeforeUnmount(() => {
  for (const timer of flashTimers.values()) clearTimeout(timer)
  flashTimers.clear()
})
</script>

<template>
  <AppShell title="Database request" width="wide">
    <section class="hero-card hero-card--compact">
      <p class="eyebrow">Platform</p>
      <h1>{{ name }}</h1>
      <p v-if="draft" class="hero-copy">{{ intentSentence(draft.spec) }}</p>
      <div class="hero-actions">
        <RouterLink class="text-link" :to="{ name: 'databases' }">
          ← All requests
        </RouterLink>
      </div>
    </section>

    <PermissionRequirements
      :authz="authz"
      :loading="authzLoading"
      :error="authzError"
      :requirements="DATABASE_REQUIREMENTS"
      title="You may not change this request"
      highlight="databases"
    />

    <section class="panel" role="status">
      <p>{{ connectionMessage }}</p>
      <p v-if="notice">{{ notice }}</p>
      <a
        v-if="state.status === 'terminal'"
        class="button"
        href="/auth/login?return=%2Fdatabases"
        >Sign in again</a
      >
      <button
        v-if="state.status === 'exhausted'"
        class="button"
        @click="live.reconnect"
      >
        Retry connection
      </button>
      <details
        v-if="recoveryDraft && (!draft || state.status === 'terminal')"
        class="recovery-details"
      >
        <summary>Copy your unsaved request before leaving this page</summary>
        <textarea
          readonly
          rows="12"
          :value="JSON.stringify(recoveryDraft.spec, null, 2)"
          aria-label="Unsaved request recovery"
        />
      </details>
    </section>

    <section v-if="loading" class="panel">
      <h2>Loading…</h2>
    </section>

    <!-- Outside the draft branch on purpose: when the OBJECT fails to load
         there is no draft, and an error nested inside that branch renders
         nothing at all. The coffee editor learned this the expensive way. -->
    <section v-else-if="loadError && !draft" class="panel panel--danger">
      <h2>Could not open this request</h2>
      <p>{{ loadError }}</p>
      <p class="metadata-copy">
        If this says Unauthorized or Forbidden, that is Kubernetes answering
        with your identity, not the application.
      </p>
    </section>

    <template v-else-if="draft">
      <section v-if="loadError" class="panel panel--danger" role="alert">
        <h2>Save failed</h2>
        <p>{{ loadError }}</p>
      </section>
      <!-- A commit that did NOT happen is news and keeps the panel. A commit
           that did is a footnote, and reads as one: see .commit-trail. -->
      <section
        v-if="commitNotice || commitState === 'failed'"
        class="panel panel--warning"
      >
        <h2>Git commit status</h2>
        <p>{{ commitNotice || commitDetail || failedCommitCopy }}</p>
      </section>
      <p v-else-if="commitState === 'pending'" class="commit-trail">
        Committing to Git…
      </p>
      <p v-else-if="commitState === 'committed'" class="commit-trail">
        Committed to Git<template v-if="commitSha">
          · {{ shortSha(commitSha) }}</template
        >
      </p>

      <section class="admin-grid">
        <div class="database-form">
          <section v-if="recordedIntent" class="panel">
            <p class="eyebrow">Last recorded intent</p>
            <p class="hero-copy">“{{ recordedIntent }}”</p>
          </section>

          <DatabaseFields
            :object="draft"
            :field-state="fieldState"
            :server-value-for="serverValueFor"
            :flashed="flashedPaths"
            @update="updateField"
            @revert="revert"
          />
        </div>

        <div class="admin-sidebar admin-sidebar-stack">
          <article class="panel">
            <section class="save-summary">
              <div class="save-summary__header">
                <div class="save-summary__copy">
                  <p class="eyebrow">Review and save</p>
                  <h3>
                    {{
                      dirtyFieldCount === 0
                        ? 'No pending changes'
                        : `${dirtyFieldCount} change(s) ready to save`
                    }}
                  </h3>
                  <p class="metadata-copy">
                    <template v-if="dirtyFieldCount === 0"
                      >The form matches the request as the cluster has
                      it.</template
                    >
                    <template v-else-if="conflictCount === 0">
                      Yellow dots are your edits. Saving writes them to
                      Kubernetes with your own identity.
                    </template>
                    <template v-else>
                      Red dots mean somebody else changed the same field while
                      you were typing. Choose for each one before saving.
                    </template>
                  </p>
                </div>
              </div>

              <ul v-if="summaryEntries.length" class="save-summary__list">
                <li
                  v-for="entry in summaryEntries"
                  :key="entry.path"
                  class="save-summary__item"
                  :class="
                    entry.state === 'conflict'
                      ? 'save-summary__item--conflict'
                      : 'save-summary__item--dirty'
                  "
                >
                  <div class="save-summary__item-copy">
                    <div class="save-summary__item-row">
                      <strong>{{ entry.label }}</strong>
                      <span
                        class="pill"
                        :class="
                          entry.state === 'conflict'
                            ? 'pill--danger'
                            : 'pill--warning'
                        "
                      >
                        {{
                          entry.state === 'conflict'
                            ? 'Concurrent change'
                            : 'Edited'
                        }}
                      </span>
                    </div>
                    <p class="metadata-copy">
                      <span class="save-summary__change">
                        <s>{{ formatConflictValue(entry.serverValue) }}</s>
                        <span aria-hidden="true">→</span>
                        <span>{{ formatConflictValue(entry.draftValue) }}</span>
                      </span>
                    </p>
                  </div>
                  <button
                    class="button button--ghost"
                    @click="revert(entry.path)"
                  >
                    {{ entry.state === 'conflict' ? 'Take theirs' : 'Revert' }}
                  </button>
                  <button
                    v-if="entry.state === 'conflict'"
                    class="button button--ghost"
                    @click="keepMine(entry.path)"
                  >
                    Keep mine
                  </button>
                </li>
              </ul>

              <div class="save-summary__meta metadata-copy">
                <label
                  v-if="dirtyFieldCount > 0"
                  class="field save-summary__reason"
                >
                  <span>Why are you making this change?</span>
                  <textarea
                    v-model="changeReason"
                    rows="3"
                    placeholder="A short note so the next person understands what changed and why."
                  />
                </label>
              </div>

              <div class="save-summary__footer">
                <button
                  class="button save-summary__button"
                  :disabled="!canSave || dirtyFieldCount === 0"
                  @click="save"
                >
                  {{ saveButtonLabel }}
                </button>
                <button
                  class="button button--ghost"
                  :disabled="saving"
                  @click="refreshFromServer"
                >
                  Refresh from cluster
                </button>
              </div>
            </section>
          </article>
        </div>
      </section>
    </template>
  </AppShell>
</template>
