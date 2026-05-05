<script setup lang="ts">
import Button from 'primevue/button'
import Message from 'primevue/message'

defineProps<{
  disabled?: boolean
  busy?: boolean
  error?: string
}>()

const emit = defineEmits<{ submit: [] }>()
</script>

<template>
  <div class="sticky-surface sticky bottom-0 -mx-4 mt-8 px-4 py-4">
    <Message v-if="error" severity="error" class="mb-3 !rounded-xl">
      {{ error }}
    </Message>

    <Button
      type="button"
      class="w-full !rounded-xl !py-3 !font-bold !tracking-wide"
      severity="contrast"
      :loading="busy"
      :disabled="disabled"
      @click="emit('submit')"
    >
      Submit
    </Button>
  </div>
</template>
