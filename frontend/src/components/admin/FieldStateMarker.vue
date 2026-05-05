<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

type FieldState = 'clean' | 'dirty' | 'conflict'

const props = defineProps<{
  state: FieldState
  serverValue: unknown
  previousServer?: unknown
}>()

const emit = defineEmits<{
  apply: []
}>()

const root = ref<HTMLElement | null>(null)
const isOpen = ref(false)
const isVisible = computed(() => props.state !== 'clean')
const statusLabel = computed(() =>
  props.state === 'conflict' ? 'Concurrent change' : 'Local edit',
)
const actionLabel = computed(() =>
  props.state === 'conflict' ? 'Take Theirs' : 'Revert',
)

function closePopover() {
  isOpen.value = false
}

function togglePopover() {
  isOpen.value = !isOpen.value
}

function handleDocumentPointerDown(event: PointerEvent) {
  if (!root.value) {
    return
  }
  if (event.target instanceof Node && !root.value.contains(event.target)) {
    closePopover()
  }
}

function handleEscape(event: KeyboardEvent) {
  if (event.key !== 'Escape') {
    return
  }
  closePopover()
}

function applyAndClose() {
  emit('apply')
  closePopover()
}

function formatValue(value: unknown): string {
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return '(empty)'
    }
    if (value.every((item) => typeof item !== 'object' || item === null)) {
      return value.map((item) => String(item)).join(', ')
    }
    return `${value.length} item${value.length === 1 ? '' : 's'}`
  }
  if (typeof value === 'boolean') {
    return value ? 'true' : 'false'
  }
  if (value && typeof value === 'object') {
    const keys = Object.keys(value)
    return keys.length === 0
      ? '(empty)'
      : `${keys.length} field${keys.length === 1 ? '' : 's'}`
  }
  if (value === null || value === undefined || value === '') {
    return '(empty)'
  }
  return String(value)
}

onMounted(() => {
  document.addEventListener('pointerdown', handleDocumentPointerDown)
})

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', handleDocumentPointerDown)
})
</script>

<template>
  <div
    ref="root"
    class="field-state"
    :class="[`field-state--${state}`, { 'field-state--open': isOpen }]"
    @keydown="handleEscape"
  >
    <span
      v-if="!isVisible"
      class="field-state__dot field-state__dot--hidden"
      aria-hidden="true"
    ></span>
    <button
      v-else
      type="button"
      class="field-state__dot"
      :aria-label="statusLabel"
      :aria-expanded="isOpen ? 'true' : 'false'"
      @click.stop.prevent="togglePopover"
    ></button>
    <div v-if="isVisible" class="field-state__popover" @click.stop>
      <p class="field-state__eyebrow">{{ statusLabel }}</p>
      <p
        v-if="state === 'conflict' && previousServer !== undefined"
        class="field-state__copy"
      >
        A newer saved value arrived while you were editing this field. Server
        moved from {{ formatValue(previousServer) }} to
        {{ formatValue(serverValue) }}.
      </p>
      <p v-else class="field-state__copy">
        Current server value: {{ formatValue(serverValue) }}.
      </p>
      <button
        type="button"
        class="button button--ghost field-state__action"
        @click.stop.prevent="applyAndClose"
      >
        {{ actionLabel }}
      </button>
    </div>
  </div>
</template>
