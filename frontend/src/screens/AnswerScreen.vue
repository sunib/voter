<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import AppShell from '../components/layout/AppShell.vue'
import Card from 'primevue/card'
import SessionStateBanner from '../components/session/SessionStateBanner.vue'
import QuestionRenderer from '../components/questions/QuestionRenderer.vue'
import SubmitBar from '../components/submission/SubmitBar.vue'

import { useSessionStore } from '../stores/session'
import { useDraftSubmissionStore } from '../stores/draftSubmission'
import { createQuizSubmission } from '../api/kube'

const props = defineProps<{ session: string }>()

const router = useRouter()
const sessionStore = useSessionStore()
const draft = useDraftSubmissionStore()

const busy = ref(false)
const submitError = ref<string | null>(null)
const loadError = ref<string | null>(null)

const session = computed(() => sessionStore.byName[props.session]?.data)
const state = computed(() => session.value?.spec?.state)
const title = computed(() => session.value?.spec?.title ?? props.session)
const questions = computed(() => session.value?.spec?.questions ?? [])

onMounted(async () => {
  draft.load(props.session)
  try {
    await sessionStore.ensureLoaded(props.session)
    await sessionStore.fetchSessionInfo()
  } catch (e: any) {
    // If the device session is missing/expired, we expect 401/403.
    // Redirect back to join so forwardAuth can bootstrap again.
    const status = e?.status
    if (status === 401 || status === 403) {
      await router.replace({ name: 'join' })
      return
    }
    loadError.value = e?.message ?? 'Failed to load session'
  }
})

async function submit() {
  submitError.value = null

  // Basic MVP rule: if session is closed, block submit.
  if (state.value === 'closed') {
    submitError.value = 'This session is closed.'
    return
  }

  busy.value = true
  try {
    await createQuizSubmission({
      sessionName: props.session,
      answers: draft.toAnswerList(),
    })
    draft.clear()
    await router.replace({ name: 'thanks', params: { session: props.session } })
  } catch (e: any) {
    const status = e?.status
    if (status === 401 || status === 403) {
      await router.replace({ name: 'join' })
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
        <div class="text-xs font-semibold text-black/55">{{ questions.length }} questions</div>
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

      <Card v-else-if="!session" class="rounded-[var(--radius)]">
        <template #content>
          <div class="p-5">
            <div class="space-y-2">
              <div class="text-xs font-bold tracking-[0.18em] text-black/55">LOADING</div>
              <div class="text-lg font-extrabold">Fetching questions…</div>
            </div>
          </div>
        </template>
      </Card>

      <Card v-else class="rounded-[var(--radius)]">
        <template #content>
          <div class="p-5">
            <div class="space-y-10">
              <div v-if="questions.length === 0" class="rounded-xl border border-black/10 bg-black/5 p-4 text-sm text-black/70">
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

    <SubmitBar :busy="busy" :disabled="!session" :error="submitError ?? undefined" @submit="submit" />
  </AppShell>
</template>
