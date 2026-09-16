<script setup lang="ts">
// The results, updating themselves.
//
// The number on screen is a FIELD ON THE ROUND now, written by Voter's tally
// controller, and it arrives on the `quizsessions` stream this page opens
// anyway -- no new subscription, no new request, no new scope, and no new grant
// for participants, who could already read rounds. See
// docs/live-results-design.md.
//
// The REST endpoint stays: it is the first paint, and the fallback for a
// dropped stream or a deployment whose controller is not running. That is why
// the refresh button is still here, shown only when there is nothing live to
// show -- a page that is following the room should not invite the presenter to
// press anything.
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppShell from '../components/layout/AppShell.vue'
import { useLiveResources } from '../api/liveResources'
import { currentSession } from '../api/session'
import {
  getRoundResults,
  resultsFromRest,
  resultsFromStatus,
  type RoundResults,
} from '../api/quiz'
import type { QuizSession } from '../api/types'
const props = defineProps<{ session: string }>()
const route = useRoute()
const rest = ref<RoundResults>()
const busy = ref(false)
const error = ref('')

// The COLLECTION, exactly as AnswerScreen and HomeScreen watch it: the
// component is reused when the route parameter changes, so a watch pinned to
// one name would go stale, and all three pages then share one subscription.
const live = useLiveResources({
  group: 'examples.configbutler.ai',
  version: 'v1alpha1',
  resource: 'quizsessions',
  namespace: currentSession()?.namespace ?? '',
})

const streamedRound = computed<QuizSession | undefined>(() => {
  if (!live.synced()) return undefined
  return live.items.value.find(
    (o) => o.metadata?.name === props.session,
  ) as unknown as QuizSession | undefined
})

const liveResults = computed(() => resultsFromStatus(streamedRound.value))
const results = computed(() => liveResults.value ?? resultsFromRest(rest.value))
const title = computed(
  () =>
    streamedRound.value?.spec?.title ??
    rest.value?.round.spec.title ??
    'Voting results',
)
// "as of 13:22:41". A stale tally must not look live, and a controller that has
// stopped looks exactly like a room that has stopped voting.
const asOf = computed(() => {
  const at = results.value?.asOf
  if (!at) return ''
  const when = new Date(at)
  return Number.isNaN(when.getTime()) ? '' : when.toLocaleTimeString()
})
// filed and counted differ only when a ballot failed validation -- a
// hand-written one naming a question that is not there. Silence would drop the
// vote invisibly.
const refused = computed(() =>
  results.value ? Math.max(results.value.filed - results.value.total, 0) : 0,
)

async function refresh() {
  busy.value = true
  error.value = ''
  try {
    rest.value = await getRoundResults(props.session)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load results.'
  } finally {
    busy.value = false
  }
}
watch(
  () => props.session,
  () => {
    rest.value = undefined
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
      <h1 class="panel-title">{{ title }}</h1>
      <div class="results-meta">
        <span v-if="results" class="pill pill--good" data-testid="vote-total"
          >{{ results.total }} votes recorded</span
        >
        <span v-if="asOf" class="pill" data-testid="vote-as-of"
          >as of {{ asOf }}</span
        >
        <button
          v-if="!liveResults"
          class="text-link"
          :disabled="busy"
          @click="refresh"
        >
          {{ busy ? 'Loading…' : 'Refresh results' }}
        </button>
      </div>
      <p v-if="refused" class="result-count" data-testid="vote-refused">
        {{ refused }} more {{ refused === 1 ? 'ballot was' : 'ballots were' }}
        filed but did not pass validation.
      </p>
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
        <!-- Status carries a bounded sample, never the whole list: 300 answers
             of up to 2000 characters do not fit an object that is rewritten on
             every vote. Say so rather than quietly showing 25 of 300. -->
        <p v-if="result.textTotal > result.text.length" class="result-count">
          Showing the most recent {{ result.text.length }} of
          {{ result.textTotal }} answers.
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
