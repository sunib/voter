<script setup lang="ts">
// A key in the menu editor. Nobody can read a key back, this page included, so
// the dots are a placeholder over an empty input and there is nothing behind
// them. The eye only ever shows what this person is typing right now.
import { ref, watch } from 'vue'

const props = defineProps<{
  id: string
  /** What was typed; undefined until something is. */
  value?: string
  /** From the CoffeeConfig's status.keys; undefined when nothing has said. */
  stored?: boolean
  disabled: boolean
}>()
const emit = defineEmits<{ update: [value: string]; revert: [] }>()

const revealed = ref(false)
watch(
  () => props.value,
  (value) => {
    if (!value) revealed.value = false
  },
)
</script>

<template>
  <div class="field" :class="{ 'field--dirty': value !== undefined }">
    <div class="field__heading">
      <label :for="id"><span>API key</span></label>
      <div class="key-field__state">
        <template v-if="value !== undefined">
          <button type="button" class="text-link" @click="emit('revert')">
            Revert
          </button>
          <span class="pill pill--warning">New key</span>
        </template>
        <span v-else-if="stored === true" class="pill pill--good">Set</span>
        <span v-else-if="stored === false" class="pill pill--neutral"
          >Not set</span
        >
      </div>
    </div>
    <div class="key-field__control">
      <input
        :id="id"
        :value="value ?? ''"
        :type="revealed ? 'text' : 'password'"
        :class="{ 'key-field__input--set': stored }"
        :placeholder="
          stored ? '••••••••••••' : disabled ? '' : 'Type a new key'
        "
        :disabled="disabled"
        autocomplete="new-password"
        spellcheck="false"
        data-1p-ignore
        data-lpignore="true"
        @input="emit('update', ($event.target as HTMLInputElement).value)"
      />
      <button
        type="button"
        class="key-field__eye"
        :disabled="!value"
        :aria-pressed="revealed"
        :aria-label="revealed ? 'Hide what you typed' : 'Show what you typed'"
        :title="
          value
            ? revealed
              ? 'Hide what you typed'
              : 'Show what you typed'
            : 'A saved key is not shown to anyone'
        "
        @click="revealed = !revealed"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7-10-7-10-7Z" />
          <circle cx="12" cy="12" r="3" />
          <path v-if="!revealed" d="M4 4l16 16" />
        </svg>
      </button>
    </div>
    <p v-if="value !== undefined" class="metadata-copy key-field__hint">
      Value is encrypted after you save.
    </p>
    <p v-else-if="stored" class="metadata-copy key-field__hint">
      <template v-if="disabled">Can't decrypt.</template>
      <template v-else
        >Can't decrypt, type value to encrypt a replacing API key.</template
      >
    </p>
  </div>
</template>
