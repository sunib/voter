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
  try {
    results.value = await getRoundResults(props.session)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load results.'
  } finally {
    busy.value = false
  }
}
watch(
  () => props.session,
  () => {
    results.value = undefined
    void refresh()
  },
  { immediate: true },
)
</script>
<template>
  <AppShell title="Results" width="narrow">
    <section v-if="route.query.submitted" class="panel" role="status">
      <p v-if="route.query.submitted === '1'" class="results-notice">
        Your vote is recorded. Thank you!
      </p>
      <p v-else-if="route.query.submitted === 'already'" class="results-notice">
        You have already voted in this round.
      </p>
    </section>

    <section class="panel">
      <p class="eyebrow">Voting results</p>
      <h1 class="panel-title">
        {{ results?.round.spec.title ?? 'Voting results' }}
      </h1>
      <div class="results-meta">
        <span v-if="results" class="pill pill--good" data-testid="vote-total"
          >{{ results.total }} votes recorded</span
        >
        <button class="text-link" :disabled="busy" @click="refresh">
          {{ busy ? 'Loading…' : 'Refresh results' }}
        </button>
      </div>
      <p v-if="error" role="alert" class="error-copy">
        {{ error }} Results may be out of date.
      </p>
    </section>

    <section
      v-for="result in results?.questions"
      :key="result.question.id"
      class="panel"
    >
      <h2 class="panel-title">{{ result.question.title }}</h2>
      <!-- Still a <progress>: it is the element that carries the value to
           assistive tech, and the room reads these off a projector. -->
      <div v-if="result.question.choices" class="result-bars">
        <div
          v-for="choice in result.question.choices"
          :key="choice"
          class="result-bar"
        >
          <div class="result-bar__row">
            <span>{{ choice }}</span
            ><strong>{{ result.choices[choice] ?? 0 }}</strong>
          </div>
          <progress
            class="result-bar__meter"
            :value="result.choices[choice] ?? 0"
            :max="Math.max(result.count, 1)"
            :aria-label="choice"
          />
        </div>
      </div>
      <div v-else-if="result.question.type === 'freeText'" class="result-texts">
        <p
          v-for="(text, index) in result.text"
          :key="index"
          class="result-text"
        >
          {{ text }}
        </p>
      </div>
      <p v-else class="result-average">
        Average:
        {{ result.count ? (result.sum / result.count).toFixed(1) : '—' }}
      </p>
      <p class="result-count">{{ result.count }} answers</p>
    </section>

    <p class="results-back">
      <RouterLink to="/" class="text-link">Back to all quizzes</RouterLink>
    </p>
  </AppShell>
</template>
