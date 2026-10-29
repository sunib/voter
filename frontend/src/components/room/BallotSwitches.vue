<script setup lang="ts">
// Who may vote: one switch for the whole room and one per answer of the Room's
// question. Each switch is a RoleBinding from the ballot Role to that group,
// made and unmade with the operator's own token, like the coffee switch.
//
// After every change the page writes which groups may vote onto each round
// (roomGrants.ts, announceBallotGroups). Every phone streams the rounds, so a
// participant's vote page sees the marker move, asks the API server once
// whether it may now create a QuizSubmission, and its refusal turns into a
// ballot -- or back -- without polling. Nothing in Voter decides who votes.
//
// Nor does this page poll: the bindings are read again when one changes, and
// the head counts are a live view of the room's Participants.
import { computed, ref } from 'vue'

import {
  announceBallotGroups,
  ballotGroups,
  listRoleBindings,
  setBallotGrant,
  useLiveParticipants,
} from '../../api/roomGrants'
import { onGrantChange } from '../../api/grantChanges'

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
const visible = ref(false)
const loaded = ref(false)
const busy = ref('')
const actionError = ref('')

const { live: room, participants } = useLiveParticipants()
// Refused is not shown: the bindings' 403 already hides the control.
const error = computed(
  () => actionError.value || (room.faulted() ? room.error.value : ''),
)
const active = computed(() => participants.value.filter((p) => !p.spec.revoked))
const total = computed(() => active.value.length)
const counts = computed(() => {
  const tally = new Map<string, number>()
  for (const p of active.value) {
    for (const g of p.spec.groups ?? []) tally.set(g, (tally.get(g) ?? 0) + 1)
  }
  return tally
})

async function refresh() {
  try {
    const bindings = await listRoleBindings()
    if (busy.value === '') {
      const groups = ballotGroups(bindings)
      open.value = groups
      // Ring every phone's doorbell when the truth moved, however it moved:
      // a switch here, or a binding made from a terminal.
      await announceBallotGroups(groups)
    }
    visible.value = true
    actionError.value = ''
  } catch (e) {
    // As the coffee switch: a participant is refused, and the control is
    // simply not theirs. Anything else is a fault worth seeing.
    const status = (e as { status?: number }).status
    if (status === 401 || status === 403) {
      visible.value = false
    } else {
      visible.value = true
      actionError.value =
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
  actionError.value = ''
  const previous = new Set(open.value)
  const optimistic = new Set(open.value)
  if (next) optimistic.add(group)
  else optimistic.delete(group)
  open.value = optimistic
  try {
    await setBallotGrant(group, next)
  } catch (e) {
    open.value = previous
    actionError.value =
      e instanceof Error ? e.message : 'Could not change who may vote.'
  } finally {
    busy.value = ''
    // Not left to the binding's own event: the room is waiting for this.
    await refresh()
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
onGrantChange(() => void refresh(), 4000)
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
