<script setup lang="ts">
import { computed, ref } from 'vue'
import { briefRefusal } from '../api/http'

// A save that did not happen, next to the button that tried. It goes when the
// person changes what was refused, or saves again, and never on a timer: the form
// looks the same whether it was saved or not, so this is the only thing saying so.
//
// Short by default, because it sits in a narrow sidebar: a title and the sentence.
// Whatever that leaves out is in the JSON, which is Kubernetes' answer word for word.
const props = defineProps<{
  headline: string
  message: string
  /** Kubernetes' whole answer (kubernetesAnswer in api/http.ts). */
  answer?: string
}>()
const brief = computed(() => briefRefusal(props.message))
const open = ref(false)
const copied = ref(false)
function copy() {
  void navigator.clipboard.writeText(props.answer ?? '').then(
    () => {
      copied.value = true
      setTimeout(() => {
        copied.value = false
      }, 1500)
    },
    () => {},
  )
}
</script>

<template>
  <div class="save-refusal" role="alert">
    <div class="save-refusal__head">
      <strong>{{ headline }}</strong>
      <button
        v-if="answer"
        type="button"
        class="save-refusal__toggle"
        :aria-expanded="open"
        @click="open = !open"
      >
        {{ open ? 'Hide JSON' : 'Show JSON' }}
      </button>
    </div>
    <p>{{ brief }}</p>
    <div v-if="answer && open" class="save-refusal__answer">
      <button type="button" class="save-refusal__toggle" @click="copy">
        {{ copied ? 'Copied' : 'Copy' }}
      </button>
      <pre>{{ answer }}</pre>
    </div>
  </div>
</template>
