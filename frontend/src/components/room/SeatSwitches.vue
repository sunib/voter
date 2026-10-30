<script setup lang="ts">
// Whether the room may take seats, and whether it may change them: two
// switches, each one RoleBinding to a Role in Git, made and unmade with the
// operator's own token like the coffee switch. Two Roles rather than one so
// the map can close to newcomers without freezing the people already on it,
// and the other way round.
//
// No binding means no seats: the Roles are in Git, the bindings are not, so a
// rebuilt namespace starts with both switches off (docs/demo-runbook.md).
// Every phone watches the bindings and asks the API server again what it may
// do; nothing in Voter decides who sits.
import { ref } from 'vue'

import {
  getAudienceGrant,
  seatClaimSwitch,
  seatEditSwitch,
  setAudienceGrant,
  type AudienceSwitch,
} from '../../api/authz'
import { onGrantChange } from '../../api/grantChanges'

type Key = 'claim' | 'edit'

const switches: {
  key: Key
  which: AudienceSwitch
  label: string
  on: string
  off: string
}[] = [
  {
    key: 'claim',
    which: seatClaimSwitch(),
    label: 'Let the room take a seat',
    on: 'RoleBinding exists. Newcomers can claim a seat.',
    off: 'No RoleBinding. The map is closed to newcomers.',
  },
  {
    key: 'edit',
    which: seatEditSwitch(),
    label: 'Let the room edit their seat',
    on: 'RoleBinding exists. Colours and emojis can be edited.',
    off: 'No RoleBinding. Every seat stays as it is.',
  },
]

const granted = ref<Record<Key, boolean>>({ claim: false, edit: false })
const visible = ref(false)
const loaded = ref(false)
const busy = ref<Key | ''>('')
const error = ref('')

async function refresh() {
  try {
    const [claim, edit] = await Promise.all(
      switches.map((s) => getAudienceGrant(s.which)),
    )
    // A switch in flight is corrected by its own answer, not by a read that
    // may have started before it.
    if (busy.value === '')
      granted.value = { claim: claim.granted, edit: edit.granted }
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
        e instanceof Error ? e.message : 'Could not read the seat grants.'
    }
  } finally {
    loaded.value = true
  }
}

async function toggle(key: Key, next: boolean) {
  const which = switches.find((s) => s.key === key)!.which
  busy.value = key
  error.value = ''
  // Optimistic, then corrected by the answer, as the coffee switch.
  const previous = granted.value[key]
  granted.value = { ...granted.value, [key]: next }
  try {
    const state = await setAudienceGrant(next, which)
    granted.value = { ...granted.value, [key]: state.granted }
  } catch (e) {
    granted.value = { ...granted.value, [key]: previous }
    error.value =
      e instanceof Error ? e.message : 'Could not change the seat grant.'
  } finally {
    busy.value = ''
  }
}

void refresh()
onGrantChange(() => void refresh(), 5000)
</script>

<template>
  <section v-if="visible" class="panel" data-testid="seat-switches">
    <div class="section-heading">
      <h2 class="panel-title">Seats</h2>
      <span
        class="pill"
        :class="
          granted.claim && granted.edit
            ? 'pill--good'
            : granted.claim || granted.edit
              ? 'pill--neutral'
              : 'pill--warning'
        "
      >
        {{
          granted.claim && granted.edit
            ? 'open'
            : granted.claim
              ? 'claim only'
              : granted.edit
                ? 'edits only'
                : 'locked'
        }}
      </span>
    </div>
    <p class="hero-copy">
      Taking a seat is <code class="inline-code">create</code> on Seats and
      editing your seat is <code class="inline-code">patch</code>, each its own
      RoleBinding. Turn one off and every phone gets the API server's refusal,
      live, without anyone signing in again.
    </p>
    <p v-if="error" role="alert" class="error-copy">{{ error }}</p>
    <label v-for="s in switches" :key="s.key" class="grant-switch">
      <input
        type="checkbox"
        :aria-label="s.label"
        :checked="granted[s.key]"
        :disabled="busy !== '' || !loaded"
        @change="toggle(s.key, ($event.target as HTMLInputElement).checked)"
      />
      <span class="grant-switch__copy">
        <strong>{{ s.label }}</strong>
        <span class="grant-switch__hint">
          {{
            busy === s.key
              ? 'Asking Kubernetes…'
              : granted[s.key]
                ? s.on
                : s.off
          }}
        </span>
      </span>
    </label>
    <p class="metadata-copy">
      Not Flux resources, on purpose: Flux would recreate whatever a switch
      deletes. A rebuilt namespace starts with both off.
    </p>
  </section>
</template>
