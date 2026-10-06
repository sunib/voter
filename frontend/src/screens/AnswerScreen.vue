<script setup lang="ts">
import { computed, watch, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import AppShell from '../components/layout/AppShell.vue'
import { useLiveResources } from '../api/liveResources'
import Card from 'primevue/card'
import SessionStateBanner from '../components/session/SessionStateBanner.vue'
import QuestionRenderer from '../components/questions/QuestionRenderer.vue'
import SubmitBar from '../components/submission/SubmitBar.vue'

import type { QuizSession } from '../api/types'
import { useDraftSubmissionStore } from '../stores/draftSubmission'
import { createQuizSubmission, getQuizSession } from '../api/quiz'
import { canVote, currentSession, getSession } from '../api/session'
import { appConfig } from '../api/appConfig'
import { allows } from '../api/authz'
import { useAuthorization } from '../api/useAuthorization'
import { BALLOT_GROUPS_ANNOTATION } from '../api/roomGrants'

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

/** Whether this session may cast a ballot: a login through the deployment's
 *  participant connector (session.ts, canVote).
 *
 *  A ref set while the round loads, NOT a computed over currentSession():
 *  currentSession() reads a plain module variable, so a computed over it takes
 *  no reactive dependency and latches whatever happened to be cached the first
 *  time it was read -- which, before the session lands, is null. That is how
 *  this screen came to show "you cannot vote" to participants who could.
 *
 *  It defaults to true so a slow session never flashes a refusal at someone who
 *  is allowed; admission is the control, and this is only the courtesy. */
const mayVote = ref(true)
const submitError = ref<string | null>(null)
const loadError = ref<string | null>(null)

/** Whether RBAC lets this identity cast a ballot at all. Who may vote is a
 *  switch on the operator's page: a RoleBinding from the ballot Role to the
 *  whole room or to one answer's group of the Room's question. The page asks
 *  the API server (a SelfSubjectRulesReview) once, and again whenever the
 *  round's ballot-groups marker moves on the stream (watch below), so the
 *  refusal turns into the ballot, or back, without polling.
 *
 *  Undecided counts as allowed, as mayVote does: admission and RBAC are the
 *  control, this is the courtesy of saying so before anyone types. */
const { authz, refresh: refreshAuthz } = useAuthorization({ poll: false })
const mayCast = computed(
  () =>
    authz.value === null ||
    allows(authz.value, 'examples.configbutler.ai', 'quizsubmissions', 'create'),
)
/** The groups the token carries beside the whole room's, which is what a
 *  switch can open the round to. As Kubernetes names them: that is the point. */
const myGroups = computed(() => currentSession()?.groups ?? [])

// The round can close while somebody is still looking at it. Without this they
// find out by pressing Submit and being refused, which is a bad way to learn
// that the room has moved on.
//
// The COLLECTION, not this one round: the component is reused when the route
// parameter changes, so a watch pinned to a name would go stale, and the
// participants' round list is already sharing this exact scope.
const live = useLiveResources({
  group: 'examples.configbutler.ai',
  version: 'v1alpha1',
  resource: 'quizsessions',
  namespace: appConfig().namespace,
})

const streamedRound = computed(() => {
  if (!live.synced()) return undefined
  return live.items.value.find((o) => o.metadata?.name === props.session) as
    | QuizSession
    | undefined
})

// The operator page rewrites this marker on the round whenever who may vote
// changes. Its value is only a doorbell; the answer is the API server's.
watch(
  () => streamedRound.value?.metadata?.annotations?.[BALLOT_GROUPS_ANNOTATION],
  (marker, previous) => {
    if (marker !== previous) void refreshAuthz()
  },
)

// Only the STATE follows the stream. The questions deliberately do not: swapping
// them under someone mid-answer is the thing the round rules forbid, and
// admission refuses a ballot whose questions digest is not the round's.
// A closed round simply stops accepting; it does not rewrite what is on screen.
const state = computed(
  () => streamedRound.value?.spec?.state ?? round.value?.spec?.state,
)
const closedWhileAnswering = computed(
  () => round.value?.spec?.state === 'live' && state.value !== 'live',
)
const title = computed(() => round.value?.spec?.title ?? props.session)
const questions = computed(() => round.value?.spec?.questions ?? [])

watch(
  () => props.session,
  async () => {
    round.value = undefined
    voted.value = false
    loadError.value = null
    submitError.value = null
    try {
      const view = await getQuizSession(props.session)
      round.value = view.round
      voted.value = view.voted
      // Read here, where the router guard has already refreshed the session.
      mayVote.value = canVote(currentSession())
      draft.load(`${currentSession()?.subject}:${round.value?.metadata.uid}`)
    } catch (e: any) {
      const status = e?.status
      if ((status === 401 || status === 403) && (await isSignedOut())) {
        await router.replace({ name: 'login', query: { next: route.fullPath } })
        return
      }
      loadError.value = e?.message ?? 'Failed to load session'
    }
  },
  { immediate: true },
)

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
    await router.replace({
      name: 'vote-results',
      params: { session: props.session },
      query: { submitted: '1' },
    })
  } catch (e: any) {
    const status = e?.status
    if (e?.code === 'AlreadyVoted') {
      voted.value = true
      await router.replace({
        name: 'vote-results',
        params: { session: props.session },
        query: { submitted: 'already' },
      })
      return
    }
    if ((status === 401 || status === 403) && (await isSignedOut())) {
      await router.replace({ name: 'login', query: { next: route.fullPath } })
      return
    }
    // The switch may have closed while they were answering. If RBAC now says
    // no, the refusal card says it better than the API server's sentence.
    if (status === 403) {
      await refreshAuthz()
      if (!mayCast.value) return
    }
    submitError.value = e?.message ?? 'Submit failed'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <AppShell :title="title" width="narrow">
    <div class="space-y-4">
      <div class="flex items-center justify-between">
        <SessionStateBanner :state="state" />
        <div class="text-xs font-semibold text-[rgb(var(--muted))]">
          {{ questions.length }} questions
        </div>
      </div>

      <Card v-if="loadError" class="rounded-[var(--radius)]">
        <template #content>
          <div class="p-5">
            <div class="space-y-3">
              <h1 class="text-xl font-extrabold">Can’t load this session</h1>
              <p class="text-sm text-[rgb(var(--muted))]">{{ loadError }}</p>
            </div>
          </div>
        </template>
      </Card>

      <Card v-else-if="!round" class="rounded-[var(--radius)]">
        <template #content>
          <div class="p-5">
            <div class="space-y-2">
              <div class="text-xs font-bold tracking-[0.18em] text-[rgb(var(--muted))]">
                LOADING
              </div>
              <div class="text-lg font-extrabold">Fetching questions…</div>
            </div>
          </div>
        </template>
      </Card>

      <Card v-else-if="!mayVote" class="rounded-[var(--radius)]">
        <template #content>
          <div class="p-5">
            <div class="space-y-3">
              <h1 class="text-xl font-extrabold">
                Voting is for people who joined through Room Pass.
              </h1>
              <p class="text-sm text-[rgb(var(--muted))]">
                You are signed in as an operator, so you can open and close
                rounds and read the results — but a ballot has to belong to
                someone who came through the door. Scan the room's QR code to
                join as a participant.
              </p>
            </div>
          </div>
        </template>
      </Card>

      <Card
        v-else-if="!voted && state === 'live' && !mayCast"
        class="rounded-[var(--radius)]"
        data-testid="ballot-refused"
      >
        <template #content>
          <div class="p-5">
            <div class="space-y-3" role="alert">
              <h1 class="text-xl font-extrabold">
                This round isn’t open to you. Yet.
              </h1>
              <p class="text-sm text-[rgb(var(--muted))]">
                Kubernetes says you may not create a QuizSubmission: no
                RoleBinding gives your groups the right to vote. Only some
                groups may vote right now, and the presenter can open it up.
              </p>
              <p v-if="myGroups.length" class="text-sm">
                Your groups:
                <code
                  v-for="g in myGroups"
                  :key="g"
                  class="mr-1 rounded bg-[rgb(var(--ink))]/5 px-1"
                  >{{ g }}</code
                >
              </p>
              <p class="text-xs text-[rgb(var(--muted))]">
                Keep this page open. It opens on its own the moment you’re let in.
              </p>
            </div>
          </div>
        </template>
      </Card>

      <Card v-else-if="voted" class="rounded-[var(--radius)]">
        <template #content>
          <div class="p-5">
            <div class="space-y-3">
              <h1 class="text-xl font-extrabold">
                You have already voted in this round.
              </h1>
              <p class="text-sm text-[rgb(var(--muted))]">
                Your QuizSubmission is recorded and cannot be changed. A new
                round is needed to vote again.
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
                class="rounded-xl border border-[rgb(var(--line))]/10 bg-[rgb(var(--ink))]/5 p-4 text-sm text-[rgb(var(--muted))]"
              >
                No questions configured yet.
              </div>

              <div v-for="q in questions" :key="q.id" class="space-y-6">
                <QuestionRenderer
                  :question="q"
                  :model-value="draft.answers[q.id]"
                  @update:model-value="(v) => draft.setAnswer(q.id, v)"
                />
                <div class="h-px w-full bg-[rgb(var(--ink))]/10" />
              </div>
            </div>
          </div>
        </template>
      </Card>
    </div>

    <p
      v-if="closedWhileAnswering"
      role="status"
      class="mt-4 rounded-xl border border-[rgb(var(--line))]/10 bg-[rgb(var(--ink))]/5 p-4 text-sm"
    >
      <strong>This round has just been closed.</strong> The presenter closed it
      while you were answering, so it can no longer take your vote. Your answers
      are still here, and the results are already available.
    </p>
    <p v-if="!voted && mayCast" class="mt-4 text-sm text-[rgb(var(--muted))]">
      Submitting creates your QuizSubmission for this round. You can change your
      answers before submitting, but submitted answers cannot be edited.
    </p>
    <SubmitBar
      v-if="!voted && mayCast"
      :busy="busy"
      :disabled="!round || state !== 'live'"
      :error="submitError ?? undefined"
      @submit="submit"
    />
  </AppShell>
</template>
