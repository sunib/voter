<script setup lang="ts">
// A database request that does not exist yet.
//
// The same form as the editor, and deliberately not the same plumbing. There is
// no object to stream, no server value to conflict with and nothing to revert
// to -- so this page holds a plain local spec and POSTs it. Dressing that up as
// a live draft would mean inventing a resourceVersion for an object the cluster
// has never heard of.
//
// One action, like every other page here: Create.
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import AppShell from '../components/layout/AppShell.vue'
import DatabaseFields from '../components/databases/DatabaseFields.vue'
import PermissionRequirements from '../components/PermissionRequirements.vue'
import { DATABASE_REQUIREMENTS } from '../api/authz'
import { useAuthorization } from '../api/useAuthorization'
import { DATABASE_FIELDS } from '../api/databaseForm'
import { createDatabase } from '../api/databases'
import {
  emptyDatabaseSpec,
  intentSentence,
  type DatabaseSpec,
} from '../api/databaseTypes'

const router = useRouter()
const { authz, error: authzError, loading: authzLoading } = useAuthorization()

// An ordinary reactive object, edited in place. `object` below wraps it in the
// same `{ spec }` shape the form reads on the editor screen, so one component
// serves both without knowing which page it is on.
const spec = ref<DatabaseSpec>(emptyDatabaseSpec())
const name = ref('')
const changeReason = ref('')
const saving = ref(false)
const error = ref('')
const missing = ref<Record<string, true>>({})
const nameMissing = ref('')

const object = computed(() => ({ spec: spec.value }))

const NAME_PATTERN = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/

/** Suggested, not imposed: the moment somebody types a name of their own, this
 *  stops following them. A generated name that silently changes under an edit
 *  is the kind of surprise that ends up on a slide. */
const suggestedName = computed(() => {
  const service = spec.value.service?.trim()
  const engine = spec.value.engine
  return service && engine ? `${service}-${engine}` : ''
})

function useSuggestedName() {
  name.value = suggestedName.value
}

function update(path: string, value: unknown) {
  // `spec.owner.team` -> walk down, creating the objects on the way. The form
  // only ever addresses fixed Database fields, so there is no arbitrary key
  // here and no prototype to pollute.
  const segments = path.split('.').slice(1)
  const last = segments.pop()!
  let cursor = spec.value as unknown as Record<string, unknown>
  for (const segment of segments) {
    if (
      typeof cursor[segment] !== 'object' ||
      cursor[segment] === null ||
      Array.isArray(cursor[segment])
    ) {
      cursor[segment] = {}
    }
    cursor = cursor[segment] as Record<string, unknown>
  }
  if (value === undefined) delete cursor[last]
  else cursor[last] = value
  // Clearing the marker as they type, rather than only on the next submit:
  // being told a field is missing while you are filling it in is noise.
  if (missing.value[path]) {
    const next = { ...missing.value }
    delete next[path]
    missing.value = next
  }
}

function valueAt(path: string): unknown {
  let cursor: unknown = object.value
  for (const segment of path.split('.')) {
    if (cursor === null || typeof cursor !== 'object') return undefined
    cursor = (cursor as Record<string, unknown>)[segment]
  }
  return cursor
}

/** The CRD's required fields, checked here so the page can point at the empty
 *  one instead of showing an apiserver message about a field path. It is not
 *  the validation: everything else -- the enums, the patterns, the email
 *  format, the lengths -- is still Kubernetes' answer and is rendered verbatim
 *  when it comes back. */
function findMissing(): Record<string, true> {
  const gaps: Record<string, true> = {}
  for (const field of DATABASE_FIELDS) {
    if (!field.required) continue
    const value = valueAt(field.path)
    if (value === undefined || value === null || value === '') {
      gaps[field.path] = true
    }
  }
  return gaps
}

async function create() {
  error.value = ''
  nameMissing.value = ''
  missing.value = findMissing()

  const trimmed = name.value.trim()
  if (!NAME_PATTERN.test(trimmed)) {
    nameMissing.value = trimmed
      ? 'Lowercase letters, digits and dashes only, starting and ending with a letter or digit.'
      : 'Give this request a name.'
  }
  if (nameMissing.value || Object.keys(missing.value).length > 0) return

  saving.value = true
  try {
    const created = await createDatabase(
      trimmed,
      spec.value,
      changeReason.value,
    )
    await router.push({
      name: 'database-edit',
      params: { name: created.name },
    })
  } catch (cause) {
    error.value =
      cause instanceof Error ? cause.message : 'Could not file the request.'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <AppShell title="New database request" width="wide">
    <section class="hero-card hero-card--compact">
      <p class="eyebrow">Platform</p>
      <h1>New database request</h1>
      <p class="hero-copy">{{ intentSentence(spec) }}</p>
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
      title="You may not file a database request"
      highlight="databases"
    />

    <section v-if="error" class="panel panel--danger" role="alert">
      <h2>Kubernetes refused this request</h2>
      <p>{{ error }}</p>
      <p class="metadata-copy">
        The message above is the API server's, not the application's. The CRD
        owns the rules; this page only asks.
      </p>
    </section>

    <section class="admin-grid">
      <div class="database-form">
        <article class="panel">
          <div class="section-heading section-heading--tight">
            <div class="section-heading__title">
              <h2>Name</h2>
            </div>
          </div>
          <p class="metadata-copy">
            The object's name in Kubernetes. It cannot be changed afterwards.
          </p>
          <div class="form-grid">
            <label
              class="field"
              :class="{ 'field--missing': Boolean(nameMissing) }"
            >
              <div class="field__heading">
                <span>Name <span class="field__required">*</span></span>
              </div>
              <input
                v-model="name"
                type="text"
                placeholder="checkout-api-postgresql"
              />
              <span v-if="nameMissing" class="field__error">{{
                nameMissing
              }}</span>
              <span
                v-else-if="suggestedName && name !== suggestedName"
                class="field__help"
              >
                Suggested:
                <button
                  type="button"
                  class="text-link"
                  @click="useSuggestedName"
                >
                  {{ suggestedName }}
                </button>
              </span>
            </label>
          </div>
        </article>

        <DatabaseFields :object="object" :missing="missing" @update="update" />
      </div>

      <div class="admin-sidebar admin-sidebar-stack">
        <article class="panel">
          <section class="save-summary">
            <div class="save-summary__header">
              <div class="save-summary__copy">
                <p class="eyebrow">Review and file</p>
                <h3>New request</h3>
                <p class="metadata-copy">
                  This creates a <code>Database</code> object with your own
                  identity. Nothing provisions it; the declared intent is the
                  deliverable.
                </p>
              </div>
            </div>

            <div class="save-summary__meta metadata-copy">
              <label class="field save-summary__reason">
                <span>Why do you need this?</span>
                <textarea
                  v-model="changeReason"
                  rows="3"
                  placeholder="A short note the platform team will read when they pick this up."
                />
              </label>
            </div>

            <div class="save-summary__footer">
              <button
                class="button save-summary__button"
                :disabled="saving"
                @click="create"
              >
                {{ saving ? 'Filing…' : 'File this request' }}
              </button>
            </div>
          </section>
        </article>
      </div>
    </section>
  </AppShell>
</template>
