<script setup lang="ts">
// The why, required on every claim and every change: it is what the room gets
// to read afterwards. Its problem shows only once the person has tried to send
// it, so an empty field is not an error before they have started.
import { computed } from 'vue'

import { WHY_MAX, whyText } from '../../api/seats'

const why = defineModel<string>({ required: true })
defineProps<{ label: string; placeholder: string; error?: string }>()

const used = computed(() => [...whyText(why.value)].length)
</script>

<template>
  <label class="field seat-why">
    <span class="field__heading">
      <span>{{ label }}</span>
      <span
        class="seat-why__count"
        :class="{ 'seat-why__count--over': used > WHY_MAX }"
        >{{ used }}/{{ WHY_MAX }}</span
      >
    </span>
    <input
      v-model="why"
      type="text"
      enterkeyhint="done"
      autocomplete="off"
      :placeholder="placeholder"
      :aria-invalid="error ? 'true' : undefined"
    />
    <span v-if="error" class="field__error" role="alert">{{ error }}</span>
  </label>
</template>
