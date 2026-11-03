<script setup lang="ts">
// A save that did not happen, next to the button that tried. It goes when the
// person changes what was refused, or saves again, and never on a timer: the form
// looks the same whether it was saved or not, so this is the only thing saying so.
defineProps<{
  headline: string
  message: string
  /** Kubernetes' whole answer, word for word (kubernetesAnswer in api/http.ts). */
  answer?: string
}>()
</script>

<template>
  <div class="error-copy save-refusal" role="alert">
    <strong>{{ headline }}</strong>
    <p>{{ message }}</p>
    <details v-if="answer" class="raw-answer">
      <summary>More...</summary>
      <pre>{{ answer }}</pre>
    </details>
  </div>
</template>
