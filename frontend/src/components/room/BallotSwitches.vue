<script setup lang="ts">
// Who may vote: one switch for the whole room and one per answer of the Room's
// question. Each switch is a RoleBinding from the ballot Role to that group,
// made and unmade with the operator's own token, like the coffee switch.
//
// A participant's vote page asks the API server whether it may create a
// QuizSubmission, so flipping a switch here turns their refusal into a ballot
// without anyone reloading. Nothing in Voter decides who votes.
import { computed, onBeforeUnmount, ref } from 'vue'

import {
  ballotGroups,
  listRoleBindings,
  setBallotGrant,
  type Participant,
} from '../../api/roomGrants'
import { listObjects, PARTICIPANTS } from '../../api/kube'

const props = defineProps<{
  audienceGroup?: string
  answers?: { label: string; group: string }[]
}>()

interface Switch {
  group: string
  label: string
  hint: string
}

const switches = computed<Switch[]>(() => {
  const out: Switch[] = []
  if (props.audienceGroup) {
    out.push({
      group: props.audienceGroup,
      label: 'Everyone in the room',
      hint: 'whoever answered what',
    })
  }
  for (const answer of props.answers ?? []) {
    out.push({ group: answer.group, label: answer.label, hint: '' })
  }
  return out
})

const open = ref<Set<string>>(new Set())
const counts = ref<Map<string, number>>(new Map())
const total = ref(0)
const visible = ref(false)
const loaded = ref(false)
const busy = ref('')
const error = ref('')

async function refresh() {
  try {
    const [bindings, participants] = await Promise.all([
      listRoleBindings(),
      listObjects<Participant>(PARTICIPANTS),
    ])
    if (busy.value === '') open.value = ballotGroups(bindings)
    const tally = new Map<string, number>()
    let active = 0
    for (const p of participants) {
      if (p.spec.revoked) continue
      active++
      for (const g of p.spec.groups ?? []) tally.set(g, (tally.get(g) ?? 0) + 1)
    }
    counts.value = tally
    total.value = active
    visible.value = true
    error.value = ''
  } catch (e) {
    // As the coffee switch: a participant is refused, and the control is
    // simply not theirs. Anything else is a fault worth seeing.
    const status = (e as { status?: number }).status
    if (status === 401 || status === 403) {
      visible.value = false
    } else {
      visible.value = true
      error.value =
        e instanceof Error ? e.message : 'Could not read who may vote.'
    }
  } finally {
    loaded.value = true
  }
}

function countFor(s: Switch): number {
  return s.group === props.audienceGroup
    ? total.value
    : (counts.value.get(s.group) ?? 0)
}

async function toggle(group: string, next: boolean) {
  busy.value = group
  error.value = ''
  const previous = new Set(open.value)
  const optimistic = new Set(open.value)
  if (next) optimistic.add(group)
  else optimistic.delete(group)
  open.value = optimistic
  try {
    await setBallotGrant(group, next)
  } catch (e) {
    open.value = previous
    error.value =
      e instanceof Error ? e.message : 'Could not change who may vote.'
  } finally {
    busy.value = ''
    void refresh()
  }
}

const summary = computed(() => {
  if (props.audienceGroup && open.value.has(props.audienceGroup)) {
    return { text: 'everyone votes', tone: 'pill--good' }
  }
  if (open.value.size === 0)
    return { text: 'nobody votes', tone: 'pill--warning' }
  return {
    text: `${open.value.size} group${open.value.size === 1 ? '' : 's'}`,
    tone: 'pill--neutral',
  }
})

void refresh()
const timer = setInterval(() => void refresh(), 4000)
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <section v-if="visible" class="panel" data-testid="ballot-switches">
    <div class="section-heading">
      <h2 class="panel-title">Who may vote</h2>
      <span class="pill" :class="summary.tone">{{ summary.text }}</span>
    </div>
    <p class="hero-copy">
      A ballot is a QuizSubmission the participant creates with their own token.
      Each switch binds the ballot Role to one group. Everyone else gets a
      polite refusal, and it turns into the ballot the moment you let them in.
    </p>
    <p v-if="error" role="alert" class="error-copy">{{ error }}</p>
    <label v-for="s in switches" :key="s.group" class="grant-switch">
      <input
        type="checkbox"
        :aria-label="`Let ${s.label} vote`"
        :checked="open.has(s.group)"
        :disabled="busy !== '' || !loaded"
        @change="toggle(s.group, ($event.target as HTMLInputElement).checked)"
      />
      <span class="grant-switch__copy">
        <strong>{{ s.label }}</strong>
        <span class="grant-switch__hint">
          {{ countFor(s) }} {{ countFor(s) === 1 ? 'person' : 'people' }}
          <template v-if="s.hint"> · {{ s.hint }}</template>
          · <code class="inline-code">{{ s.group }}</code>
          {{ busy === s.group ? '· asking Kubernetes…' : '' }}
        </span>
      </span>
    </label>
    <p v-if="!answers?.length" class="metadata-copy">
      The Room asks no question at the door, so there are no groups to tell
      apart.
    </p>
    <p class="metadata-copy">
      Not Flux resources, on purpose: Flux would recreate whatever a switch
      deletes. gitops-reverser mirrors them to the audit trail.
    </p>
  </section>
</template>
