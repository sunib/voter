<script setup lang="ts">
// The results, updating themselves.
//
// The number on screen is a FIELD ON THE ROUND now, written by Voter's tally
// controller, and it arrives on the `quizsessions` stream this page opens
// anyway -- no new subscription, no new request, no new scope, and no new grant
// for participants, who could already read rounds. See
// docs/live-results-design.md.
//
// There is no other source: Voter's REST results endpoint went with the move
// to krm-foyer. A round with no tally yet says it is waiting, rather than
// showing zeroes over real votes, and the stream's own state says when it is
// not following the room.
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import AppShell from '../components/layout/AppShell.vue'
import { useLiveResources } from '../api/liveResources'
import { appConfig } from '../api/appConfig'
import { resultsFromStatus } from '../api/quiz'
import type { QuizSession } from '../api/types'
const props = defineProps<{ session: string }>()
const route = useRoute()

// The COLLECTION, exactly as AnswerScreen and HomeScreen watch it: the
// component is reused when the route parameter changes, so a watch pinned to
// one name would go stale, and all three pages then share one subscription.
const live = useLiveResources({
  group: 'examples.configbutler.ai',
  version: 'v1alpha1',
  resource: 'quizsessions',
  namespace: appConfig().namespace,
})

const streamedRound = computed<QuizSession | undefined>(() => {
  if (!live.synced()) return undefined
  return live.items.value.find(
    (o) => o.metadata?.name === props.session,
  ) as unknown as QuizSession | undefined
})

const results = computed(() => resultsFromStatus(streamedRound.value))
const title = computed(() => streamedRound.value?.spec?.title ?? 'Voting results')
// Synced, the round is there, and it has never been tallied: the controller
// has not got to it yet, or is not running.
const waiting = computed(
  () => live.synced() && streamedRound.value !== undefined && !results.value,
)
const missing = computed(() => live.synced() && streamedRound.value === undefined)
// Just voted, and the tally has not caught up: the count lives in the round's
// status, which the reconciler writes about a second after a ballot lands.
// "0 votes recorded" under "Your vote is recorded" would contradict itself.
const counting = computed(
  () => route.query.submitted === '1' && results.value?.total === 0,
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
        <span v-if="counting" class="pill" data-testid="vote-counting"
          >Counting your vote…</span
        >
        <span
          v-else-if="results"
          class="pill pill--good"
          data-testid="vote-total"
          >{{ results.total }} votes recorded</span
        >
        <span v-if="asOf" class="pill" data-testid="vote-as-of"
          >as of {{ asOf }}</span
        >
      </div>
      <p v-if="waiting" class="result-count" data-testid="vote-waiting">
        Waiting for the first count of this round.
      </p>
      <p v-if="missing" role="alert" class="error-copy">
        There is no round called {{ session }}.
      </p>
      <p v-if="live.error.value" role="alert" class="error-copy">
        {{ live.error.value }} Results may be out of date.
      </p>
      <p v-if="refused" class="result-count" data-testid="vote-refused">
        {{ refused }} more {{ refused === 1 ? 'ballot was' : 'ballots were' }}
        filed but did not pass validation.
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
