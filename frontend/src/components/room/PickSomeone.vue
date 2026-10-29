<script setup lang="ts">
// One person on the coffee menu before the rest of the room. A random
// participant gets a RoleBinding of their own to the coffee-admin Role, naming
// their Kubernetes username; the room-wide switch is a different binding and
// keeps working as it did. Their next save is a commit in their name.
//
// Nothing here polls: the binding is read again when one changes, and the
// people to pick from are a live view of the room's Participants.
import { computed, ref } from 'vue'

import {
  grantPerson,
  listRoleBindings,
  personGrant,
  pickParticipant,
  revokePerson,
  useLiveParticipants,
  type PersonGrant,
} from '../../api/roomGrants'
import { onGrantChange } from '../../api/grantChanges'

const holder = ref<PersonGrant | null>(null)
const { live: room, participants } = useLiveParticipants()
const visible = ref(false)
const loaded = ref(false)
const busy = ref(false)
const actionError = ref('')
// Refused is not shown: the bindings' 403 already hides the control.
const error = computed(
  () => actionError.value || (room.faulted() ? room.error.value : ''),
)

async function refresh() {
  try {
    const bindings = await listRoleBindings()
    if (!busy.value) holder.value = personGrant(bindings)
    visible.value = true
    actionError.value = ''
  } catch (e) {
    const status = (e as { status?: number }).status
    if (status === 401 || status === 403) {
      visible.value = false
    } else {
      visible.value = true
      actionError.value =
        e instanceof Error ? e.message : 'Could not read the room.'
    }
  } finally {
    loaded.value = true
  }
}

async function pick() {
  const chosen = pickParticipant(participants.value, holder.value)
  if (chosen === null) {
    actionError.value = 'Nobody has joined the room yet.'
    return
  }
  busy.value = true
  actionError.value = ''
  try {
    await grantPerson(chosen)
    holder.value = personGrant(await listRoleBindings())
  } catch (e) {
    actionError.value =
      e instanceof Error ? e.message : 'Could not hand out the menu.'
  } finally {
    busy.value = false
  }
}

async function revoke() {
  busy.value = true
  actionError.value = ''
  try {
    await revokePerson()
    holder.value = null
  } catch (e) {
    actionError.value =
      e instanceof Error ? e.message : 'Could not take the menu back.'
  } finally {
    busy.value = false
  }
}

void refresh()
onGrantChange(() => void refresh(), 4000)
</script>

<template>
  <div v-if="visible" class="pick-someone" data-testid="pick-someone">
    <p class="eyebrow">One person first</p>
    <p v-if="holder" class="pick-someone__name" data-testid="picked-name">
      {{ holder.displayName }}
    </p>
    <p class="hero-copy">
      {{
        holder
          ? 'may edit the coffee menu: a RoleBinding naming their username, nobody else’s.'
          : 'Hand the menu to one participant, picked at random, before the whole room.'
      }}
    </p>
    <p v-if="holder" class="metadata-copy">
      <code class="inline-code">{{ holder.username }}</code>
    </p>
    <p v-if="error" role="alert" class="error-copy">{{ error }}</p>
    <div class="hero-actions">
      <button class="button" :disabled="busy || !loaded" @click="pick">
        {{
          busy
            ? 'Asking Kubernetes…'
            : holder
              ? 'Pick someone else'
              : 'Pick someone'
        }}
      </button>
      <button
        v-if="holder"
        class="button button--secondary"
        :disabled="busy"
        @click="revoke"
      >
        Take it back
      </button>
    </div>
  </div>
</template>

<style scoped>
.pick-someone {
  margin-top: 1.25rem;
  padding-top: 1rem;
  border-top: 1px solid rgb(var(--line) / 0.12);
}
.pick-someone__name {
  font-size: clamp(2rem, 6vw, 3.5rem);
  font-weight: 800;
  line-height: 1.1;
  margin: 0.25rem 0;
  word-break: break-word;
}
</style>
