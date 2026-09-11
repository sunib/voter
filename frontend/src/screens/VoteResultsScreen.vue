<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppShell from '../components/layout/AppShell.vue'
import { getRoundResults, type RoundResults } from '../api/quiz'
const props = defineProps<{ session: string }>()
const route = useRoute()
const results = ref<RoundResults>()
const busy = ref(false)
const error = ref('')
async function refresh() {
  busy.value = true
  error.value = ''
  try { results.value = await getRoundResults(props.session) }
  catch (e) { error.value = e instanceof Error ? e.message : 'Could not load results.' }
  finally { busy.value = false }
}
watch(() => props.session, () => { results.value = undefined; void refresh() }, { immediate: true })
</script>
<template>
  <AppShell title="Voting results">
    <p v-if="route.query.submitted === '1'" role="status" class="mb-4 font-bold">Your vote is recorded. Thank you!</p>
    <p v-else-if="route.query.submitted === 'already'" role="status" class="mb-4 font-bold">You have already voted in this round.</p>
    <h1 class="text-2xl font-bold">{{ results?.round.spec.title ?? 'Voting results' }}</h1>
    <p v-if="results" class="my-4" data-testid="vote-total">{{ results.total }} votes recorded</p>
    <button :disabled="busy" class="underline" @click="refresh">{{ busy ? 'Loading…' : 'Refresh results' }}</button>
    <p v-if="error" role="alert">{{ error }} Results may be out of date.</p>
    <article v-for="result in results?.questions" :key="result.question.id" class="my-5 rounded-xl border p-4 space-y-3">
      <h2 class="font-bold">{{ result.question.title }}</h2>
      <template v-if="result.question.choices">
        <div v-for="choice in result.question.choices" :key="choice">
          <div class="flex justify-between gap-3"><span>{{ choice }}</span><strong>{{ result.choices[choice] ?? 0 }}</strong></div>
          <progress class="w-full" :value="result.choices[choice] ?? 0" :max="Math.max(result.count, 1)" :aria-label="choice" />
        </div>
      </template>
      <template v-else-if="result.question.type === 'freeText'">
        <p v-for="(text, index) in result.text" :key="index" class="whitespace-pre-wrap break-words border-b py-2">{{ text }}</p>
      </template>
      <p v-else>Average: {{ result.count ? (result.sum / result.count).toFixed(1) : '—' }}</p>
      <p class="text-sm">{{ result.count }} answers</p>
    </article>
    <RouterLink to="/vote" class="underline">Back to rounds</RouterLink>
  </AppShell>
</template>
