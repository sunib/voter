<script setup lang="ts">
// Your seat, after you took it: where it is, and its colour and emoji, which
// you may change -- with a why, every time. The seat itself never moves.
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import AppShell from '../components/layout/AppShell.vue'
import SeatLookPicker from '../components/seats/SeatLookPicker.vue'
import SeatMap from '../components/seats/SeatMap.vue'
import SeatWhyField from '../components/seats/SeatWhyField.vue'
import { useSeatMap } from '../api/liveSeats'
import {
  changeSeat,
  checkWhy,
  findMySeat,
  seatErrorMessage,
  type Seat,
} from '../api/seats'
import { currentSession } from '../api/session'

const route = useRoute()
const router = useRouter()
const { plan, seats, error: loadError } = useSeatMap()

const displayName = currentSession()?.displayName ?? ''

// Read once to know which seat is yours; after that the stream keeps it
// current, so a change made on another phone shows here too.
const found = ref<Seat | null | undefined>(undefined)
const seat = computed<Seat | null | undefined>(() => {
  const name = found.value?.metadata.name
  return seats.value.find((s) => s.metadata.name === name) ?? found.value
})

const color = ref('')
const emoji = ref('')
const why = ref('')
const tried = ref(false)
const busy = ref(false)
const failure = ref('')
const done = ref('')

const claimed = computed(() => route.query.claimed)
const whyError = computed(() => (tried.value ? checkWhy(why.value) : ''))
const unchanged = computed(
  () =>
    seat.value != null &&
    color.value === seat.value.spec.color &&
    emoji.value === seat.value.spec.emoji,
)

// The pickers start at what the seat is now, and follow it until you touch
// them.
watch(
  () => seat.value?.spec,
  (spec, before) => {
    if (!spec) return
    const untouched =
      before === undefined ||
      (color.value === before.color && emoji.value === before.emoji)
    if (untouched) {
      color.value = spec.color
      emoji.value = spec.emoji
    }
  },
  { immediate: true },
)

async function change() {
  tried.value = true
  failure.value = ''
  done.value = ''
  const current = seat.value
  if (!current) return
  if (unchanged.value) {
    failure.value = 'Pick a new colour or emoji first.'
    return
  }
  if (checkWhy(why.value) !== '') return
  busy.value = true
  try {
    const receipt = await changeSeat(current, color.value, emoji.value, why.value)
    found.value = receipt.seat
    why.value = ''
    tried.value = false
    done.value = receipt.whyLost
      ? 'Changed. Your reason could not be passed on, though.'
      : 'Changed.'
  } catch (e) {
    failure.value = seatErrorMessage(e)
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  try {
    found.value = await findMySeat(displayName)
  } catch (e) {
    failure.value = seatErrorMessage(e)
    return
  }
  if (found.value === null) {
    await router.replace({ name: 'seat-claim' })
  }
})
</script>

<template>
  <AppShell title="Your seat">
    <section class="panel seat-page">
      <template v-if="seat">
        <p v-if="claimed" class="seat-page__welcome" role="status">
          You're in.
          <template v-if="claimed === 'why-lost'">
            Your reason could not be passed on, though.
          </template>
          <RouterLink class="text-link" to="/">Continue</RouterLink>
        </p>

        <h1>Row {{ seat.spec.row }}, seat {{ seat.spec.column }}</h1>
        <p class="hero-copy">
          This is your seat for the talk. You can change its colour and emoji.
        </p>

        <p v-if="loadError" role="alert" class="error-copy">{{ loadError }}</p>

        <SeatMap
          v-if="plan"
          :plan="plan"
          :seats="seats"
          :mine="seat.metadata.name"
        />
        <p
          v-if="
            plan &&
            (seat.spec.row > plan.spec.rows ||
              seat.spec.column > plan.spec.columns)
          "
          class="seat-page__hint"
        >
          The room got smaller, and your seat is outside it now. It is still
          yours.
        </p>

        <SeatLookPicker v-model:color="color" v-model:emoji="emoji" />

        <SeatWhyField
          v-model="why"
          label="Why the change?"
          placeholder="Wanted to stand out more"
          :error="whyError"
        />

        <p v-if="failure" role="alert" class="error-copy">{{ failure }}</p>
        <p v-if="done" role="status" class="seat-page__done">{{ done }}</p>

        <div class="seat-page__actions">
          <button class="button" :disabled="busy" @click="change">
            {{ busy ? 'Changing…' : 'Change' }}
          </button>
        </div>
      </template>
      <p v-else-if="failure" role="alert" class="error-copy">{{ failure }}</p>
      <p v-else class="seat-page__hint">Finding your seat…</p>
    </section>
  </AppShell>
</template>
