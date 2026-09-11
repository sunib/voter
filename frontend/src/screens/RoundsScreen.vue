<script setup lang="ts">
import { onMounted, ref } from 'vue'
import AppShell from '../components/layout/AppShell.vue'
import { listRounds } from '../api/quiz'
import type { QuizSession } from '../api/types'
const rounds = ref<QuizSession[]>([])
const error = ref('')
const busy = ref(false)
async function refresh() {
  busy.value = true
  error.value = ''
  try { rounds.value = (await listRounds()).items.filter(r => r.spec.state !== 'draft').sort((a,b) => (a.metadata.name ?? '').localeCompare(b.metadata.name ?? '')) }
  catch (e) { error.value = e instanceof Error ? e.message : 'Could not load rounds.' }
  finally { busy.value = false }
}
onMounted(refresh)
</script>
<template>
  <AppShell title="Voting rounds">
    <h1 class="text-2xl font-bold">Voting rounds</h1>
    <p class="my-3">One vote per participant in each round. Answers and results are shared with the room.</p>
    <button class="underline" :disabled="busy" @click="refresh">{{ busy ? 'Loading…' : 'Refresh rounds' }}</button>
    <p v-if="error" role="alert" class="my-4">{{ error }}</p>
    <p v-else-if="!busy && !rounds.length" class="my-4">No rounds yet. The presenter will open one shortly.</p>
    <article v-for="round in rounds" :key="round.metadata.uid" class="my-4 rounded-xl border p-4 space-y-3">
      <h2 class="font-bold">{{ round.spec.title }}</h2>
      <p>{{ round.spec.state === 'live' ? 'Voting is open' : 'Voting is closed' }}</p>
      <div class="flex gap-4">
        <RouterLink v-if="round.spec.state === 'live'" :to="{ name: 'answer', params: { session: round.metadata.name } }" class="underline">Answer questions</RouterLink>
        <RouterLink :to="{ name: 'vote-results', params: { session: round.metadata.name } }" class="underline">View results</RouterLink>
      </div>
    </article>
  </AppShell>
</template>
