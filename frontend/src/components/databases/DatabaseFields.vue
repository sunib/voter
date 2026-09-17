<script setup lang="ts">
// The Database form, rendered from api/databaseForm.ts.
//
// One component for both pages. The editor hands it a live draft plus the
// dirty/conflict machinery; the new-request page hands it a plain object and
// nothing else, and the markers simply never appear. That is why the marker
// plumbing arrives as optional functions rather than being reached for
// directly: there is no live store behind a request that does not exist yet.

import { computed } from 'vue'
import { get, type Path } from '@configbutler/krm-stream'

import { DATABASE_FORM, type FieldSpec } from '../../api/databaseForm'
import FieldStateMarker from '../admin/FieldStateMarker.vue'

type FieldState = 'clean' | 'dirty' | 'conflict'

const props = withDefaults(
  defineProps<{
    /** The object being edited: a live draft, or a local one. */
    object: unknown
    /** Fields the CRD requires that are still empty, by path. The new-request
     *  page fills this in after a failed submit; the editor leaves it empty and
     *  lets Kubernetes answer. */
    missing?: Record<string, true>
    fieldState?: (path: string) => FieldState
    serverValueFor?: (path: string) => unknown
    flashed?: Record<string, true>
  }>(),
  {
    missing: () => ({}),
    fieldState: undefined,
    serverValueFor: undefined,
    flashed: () => ({}),
  },
)

const emit = defineEmits<{
  update: [path: string, value: unknown]
  revert: [path: string]
}>()

const groups = computed(() => DATABASE_FORM)

// Form paths address fixed Database fields only; arbitrary KRM keys never pass
// through this conversion. Same rule as the coffee editor.
const toPath = (path: string): Path =>
  path.split('.').map((part) => (/^\d+$/.test(part) ? Number(part) : part))

function stateOf(path: string): FieldState {
  return props.fieldState?.(path) ?? 'clean'
}

function classesFor(field: FieldSpec) {
  const state = stateOf(field.path)
  return {
    field: true,
    'field--wide': Boolean(field.wide) || field.kind === 'textarea',
    'field--checkbox': field.kind === 'checkbox',
    'field--dirty': state === 'dirty',
    'field--conflict': state === 'conflict',
    'field--flash': Boolean(props.flashed[field.path]),
    'field--missing': Boolean(props.missing[field.path]),
  }
}

function valueOf(path: string): unknown {
  return get(props.object, toPath(path))
}

function textOf(path: string): string {
  const value = valueOf(path)
  return typeof value === 'string' ? value : ''
}

function numberOf(path: string): string {
  const value = valueOf(path)
  return typeof value === 'number' ? String(value) : ''
}

function boolOf(path: string): boolean {
  return Boolean(valueOf(path))
}

function listOf(path: string): string {
  const value = valueOf(path)
  return Array.isArray(value) ? value.map(String).join(', ') : ''
}

function setText(field: FieldSpec, raw: string) {
  // An empty optional field is ABSENT, not "". A merge patch carrying an empty
  // string writes one, and the CRD's patterns refuse it -- so a person who
  // typed a version and then cleared it could not save at all.
  emit('update', field.path, raw === '' && !field.required ? undefined : raw)
}

function setNumber(field: FieldSpec, raw: string) {
  if (raw.trim() === '') {
    emit('update', field.path, undefined)
    return
  }
  const parsed = Number(raw)
  emit('update', field.path, Number.isFinite(parsed) ? parsed : undefined)
}

function setList(field: FieldSpec, raw: string) {
  const items = raw
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
  emit('update', field.path, items.length ? items : undefined)
}

function optionLabel(field: FieldSpec, option: string): string {
  const hint = field.hints?.[option]
  return hint ? `${option} — ${hint}` : option
}

/** The marker is only meaningful where there is a server to disagree with. */
const showsMarkers = computed(() => props.fieldState !== undefined)
</script>

<template>
  <article v-for="group in groups" :key="group.title" class="panel">
    <div class="section-heading section-heading--tight">
      <div class="section-heading__title">
        <h2>{{ group.title }}</h2>
      </div>
    </div>
    <p v-if="group.blurb" class="metadata-copy">{{ group.blurb }}</p>

    <div class="form-grid">
      <label
        v-for="field in group.fields"
        :key="field.path"
        :class="classesFor(field)"
      >
        <div v-if="field.kind !== 'checkbox'" class="field__heading">
          <span>
            {{ field.label }}
            <span v-if="field.required" class="field__required" title="Required"
              >*</span
            >
          </span>
          <FieldStateMarker
            v-if="showsMarkers"
            :state="stateOf(field.path)"
            :server-value="serverValueFor?.(field.path)"
            @apply="emit('revert', field.path)"
          />
        </div>

        <select
          v-if="field.kind === 'select'"
          :value="textOf(field.path)"
          @change="setText(field, ($event.target as HTMLSelectElement).value)"
        >
          <!-- A required enum starts empty rather than on whichever value
               happens to be first: a cost centre nobody chose is worse than a
               refused save. -->
          <option v-if="field.required" value="" disabled>Choose one…</option>
          <option v-for="option in field.options" :key="option" :value="option">
            {{ optionLabel(field, option) }}
          </option>
        </select>

        <textarea
          v-else-if="field.kind === 'textarea'"
          :value="textOf(field.path)"
          :placeholder="field.placeholder"
          rows="3"
          @input="setText(field, ($event.target as HTMLTextAreaElement).value)"
        />

        <input
          v-else-if="field.kind === 'number'"
          :value="numberOf(field.path)"
          type="number"
          inputmode="numeric"
          :min="field.min"
          :max="field.max"
          :placeholder="field.placeholder"
          @input="setNumber(field, ($event.target as HTMLInputElement).value)"
        />

        <template v-else-if="field.kind === 'checkbox'">
          <input
            :checked="boolOf(field.path)"
            type="checkbox"
            @change="
              emit(
                'update',
                field.path,
                ($event.target as HTMLInputElement).checked,
              )
            "
          />
          <div class="field__checkbox-copy">
            <span>{{ field.label }}</span>
            <FieldStateMarker
              v-if="showsMarkers"
              :state="stateOf(field.path)"
              :server-value="serverValueFor?.(field.path)"
              @apply="emit('revert', field.path)"
            />
          </div>
        </template>

        <input
          v-else-if="field.kind === 'list'"
          :value="listOf(field.path)"
          type="text"
          :placeholder="field.placeholder"
          @input="setList(field, ($event.target as HTMLInputElement).value)"
        />

        <input
          v-else
          :value="textOf(field.path)"
          type="text"
          :placeholder="field.placeholder"
          @input="setText(field, ($event.target as HTMLInputElement).value)"
        />

        <span v-if="field.help" class="field__help">{{ field.help }}</span>
        <span v-if="missing[field.path]" class="field__error"
          >This one is required.</span
        >
      </label>
    </div>
  </article>
</template>
