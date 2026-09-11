<script setup lang="ts">
import { computed, watch, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import AppShell from '../components/layout/AppShell.vue'
import Card from 'primevue/card'
import SessionStateBanner from '../components/session/SessionStateBanner.vue'
import QuestionRenderer from '../components/questions/QuestionRenderer.vue'
import SubmitBar from '../components/submission/SubmitBar.vue'

import type { QuizSession } from '../api/types'
import { useDraftSubmissionStore } from '../stores/draftSubmission'
import { createQuizSubmission, getQuizSession } from '../api/quiz'
import { currentSession, getSession } from '../api/session'

async function isSignedOut(): Promise<boolean> {
  try {
    return (await getSession()) === null
  } catch (e: any) {
    return e?.status === 401
  }
}

const props = defineProps<{ session: string }>()

const route = useRoute()
const router = useRouter()
const round = ref<QuizSession>()
const draft = useDraftSubmissionStore()

const busy = ref(false)
const voted = ref(false)
const submitError = ref<string | null>(null)
const loadError = ref<string | null>(null)

const state = computed(() => round.value?.spec?.state)
const title = computed(() => round.value?.spec?.title ?? props.session)
const questions = computed(() => round.value?.spec?.questions ?? [])

watch(() => props.session, async () => {
  round.value = undefined
  voted.value = false
  loadError.value = null
  submitError.value = null
  try {
    const view = await getQuizSession(props.session)
    round.value = view.round
    voted.value = view.voted
    draft.load(`${currentSession()?.username}:${round.value?.metadata.uid}`)
  } catch (e: any) {
    const status = e?.status
    if ((status === 401 || status === 403) && (await isSignedOut())) {
      await router.replace({ name: 'login', query: { next: route.fullPath } })
      return
    }
    loadError.value = e?.message ?? 'Failed to load session'
  }
}, { immediate: true })

async function submit() {
  submitError.value = null

  if (busy.value || !round.value || voted.value) return
  if (state.value !== 'live') {
    submitError.value = 'This round is not open for voting.'
    return
  }

  busy.value = true
  try {
    await createQuizSubmission(round.value, draft.toAnswerList(questions.value))
    draft.clear()
    await router.replace({ name: 'vote-results', params: { session: props.session }, query: { submitted: '1' } })
  } catch (e: any) {
    const status = e?.status
    if (e?.code === 'AlreadyVoted') {
      voted.value = true
      await router.replace({ name: 'vote-results', params: { session: props.session }, query: { submitted: 'already' } })
      return
    }
    if ((status === 401 || status === 403) && (await isSignedOut())) {
      await router.replace({ name: 'login', query: { next: route.fullPath } })
      return
    }
    submitError.value = e?.message ?? 'Submit failed'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <AppShell :title="title">
    <div class="space-y-4">
      <div class="flex items-center justify-between">
        <SessionStateBanner :state="state" />
        <div class="text-xs font-semibold text-black/55">
          {{ questions.length }} questions
        </div>
      </div>

      <Card v-if="loadError" class="rounded-[var(--radius)]">
        <template #content>
          <div class="p-5">
            <div class="space-y-3">
              <h1 class="text-xl font-extrabold">Can’t load this session</h1>
              <p class="text-sm text-black/60">{{ loadError }}</p>
            </div>
          </div>
        </template>
      </Card>

      <Card v-else-if="!round" class="rounded-[var(--radius)]">
        <template #content>
          <div class="p-5">
            <div class="space-y-2">
              <div class="text-xs font-bold tracking-[0.18em] text-black/55">
                LOADING
              </div>
              <div class="text-lg font-extrabold">Fetching questions…</div>
            </div>
          </div>
        </template>
      </Card>

      <Card v-else-if="voted" class="rounded-[var(--radius)]">
        <template #content>
          <div class="p-5">
            <div class="space-y-3">
              <h1 class="text-xl font-extrabold">You have already voted in this round.</h1>
              <p class="text-sm text-black/60">
                Your QuizSubmission is recorded and cannot be changed. A new round is
                needed to vote again.
              </p>
              <RouterLink
                :to="{ name: 'vote-results', params: { session } }"
                class="font-semibold underline"
              >
                View results
              </RouterLink>
            </div>
          </div>
        </template>
      </Card>

      <Card v-else class="rounded-[var(--radius)]">
        <template #content>
          <div class="p-5">
            <div class="space-y-10">
              <div
                v-if="questions.length === 0"
                class="rounded-xl border border-black/10 bg-black/5 p-4 text-sm text-black/70"
              >
                No questions configured yet.
              </div>

              <div v-for="q in questions" :key="q.id" class="space-y-6">
                <QuestionRenderer
                  :question="q"
                  :model-value="draft.answers[q.id]"
                  @update:model-value="(v) => draft.setAnswer(q.id, v)"
                />
                <div class="h-px w-full bg-black/10" />
              </div>
            </div>
          </div>
        </template>
      </Card>
    </div>

    <p v-if="!voted" class="mt-4 text-sm text-black/60">
      Submitting creates your QuizSubmission for this round. You can change your
      answers before submitting, but submitted answers cannot be edited.
    </p>
    <SubmitBar
      v-if="!voted"
      :busy="busy"
      :disabled="!round || state !== 'live'"
      :error="submitError ?? undefined"
      @submit="submit"
    />
  </AppShell>
</template>
