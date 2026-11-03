<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'

// "It worked" leaves nothing for anybody to do, so it may go by itself. A refusal
// does leave something to do and is never shown here: see SaveRefusal.vue.
const message = ref('')
let timer: ReturnType<typeof setTimeout> | undefined

function show(text: string) {
  message.value = text
  clearTimeout(timer)
  timer = setTimeout(() => {
    message.value = ''
  }, 4000)
}
onBeforeUnmount(() => clearTimeout(timer))
defineExpose({ show })
</script>

<template>
  <!-- In the body, so no panel around it can become what `fixed` is relative to. -->
  <Teleport to="body">
    <div class="saved-toast" role="status">
      <p v-if="message">{{ message }}</p>
    </div>
  </Teleport>
</template>
