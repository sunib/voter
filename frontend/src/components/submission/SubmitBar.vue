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
  <div class="sticky bottom-0 -mx-4 mt-8 border-t border-black/10 bg-[rgb(var(--bg))]/90 px-4 py-4 backdrop-blur">
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
