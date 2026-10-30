<script setup lang="ts">
// Taking your seat, once: where you sit, your colour, your emoji, and why.
//
// The seat is a suggestion, not a check: nobody verifies you sit there. The
// point is that the map on the projector looks like the room. Two phones
// claiming one seat is settled by the API server -- the second create is a 409
// -- and this page says so in plain words and lets you pick again.
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import AppShell from '../components/layout/AppShell.vue'
import SeatLookPicker from '../components/seats/SeatLookPicker.vue'
import SeatMap from '../components/seats/SeatMap.vue'
import SeatWhyField from '../components/seats/SeatWhyField.vue'
import { useSeatMap } from '../api/liveSeats'
import {
  SEAT_COLORS,
  SEAT_EMOJIS,
  checkWhy,
  claimSeat,
  findMySeat,
  pickRandom,
  seatErrorMessage,
  SEAT_CLAIM_CLOSED,
} from '../api/seats'
import { currentSession } from '../api/session'
import { currentUsername } from '../api/whoami'
import { markSeated } from '../seatOnboarding'
import { missingPermissions, SEAT_CLAIM_REQUIREMENTS } from '../api/authz'
import { useAuthorization } from '../api/useAuthorization'

const router = useRouter()
const { plan, seats, error: loadError, refresh } = useSeatMap()
// The operator can close the map from /room. Unlike the coffee editor, this
// page does not show the refusal: Claim rests and one quiet line says so,
// because the seats are not where the room learns what is underneath.
const { authz } = useAuthorization()
const closed = computed(
  () => missingPermissions(authz.value, SEAT_CLAIM_REQUIREMENTS).length > 0,
)

const selected = ref<{ row: number; column: number } | null>(null)
const color = ref<string>(pickRandom(SEAT_COLORS).hex)
const emoji = ref<string>(pickRandom(SEAT_EMOJIS))
const why = ref('')
// Who this is to Kubernetes: what a seat is claimed as, and how this page
// knows its own claim's echo from someone else's.
const username = ref('')

const tried = ref(false)
const busy = ref(false)
const failure = ref('')
// Said under the map, where the person is looking when their seat goes.
const taken = ref('')
const TAKEN = 'Someone just took that seat. Pick one close by.'

const whyError = computed(() => (tried.value ? checkWhy(why.value) : ''))
const seatError = computed(() =>
  tried.value && selected.value === null ? 'Tap your seat on the map first.' : '',
)

function select(row: number, column: number) {
  selected.value = { row, column }
  failure.value = ''
  taken.value = ''
}

// Someone else's claim reaching this phone while you were still choosing: say
// so now, rather than letting the claim find out.
watch(seats, (now) => {
  const place = selected.value
  if (
    place &&
    now.some(
      (s) =>
        s.spec.row === place.row &&
        s.spec.column === place.column &&
        // Your own claim's echo can arrive before the page moves on.
        s.spec.claimedBy?.username !== username.value,
    )
  ) {
    selected.value = null
    taken.value = TAKEN
  }
})

// A plan that shrank under a choice no longer has it.
watch(plan, (now) => {
  const place = selected.value
  if (
    place &&
    now &&
    (place.row > now.spec.rows || place.column > now.spec.columns)
  ) {
    selected.value = null
  }
})

async function claim() {
  tried.value = true
  failure.value = ''
  const place = selected.value
  if (place === null || checkWhy(why.value) !== '') return
  busy.value = true
  try {
    username.value = await currentUsername()
    // The CRD would refuse it too, but in words about a schema, not you.
    if (username.value === '') {
      failure.value =
        'Kubernetes did not say who you are, so a seat cannot be claimed as you. Sign in again.'
      return
    }
    const receipt = await claimSeat(
      place.row,
      place.column,
      {
        displayName: currentSession()?.displayName ?? '',
        username: username.value,
      },
      color.value,
      emoji.value,
      why.value,
    )
    markSeated()
    // Seated, so on to coffee. A reason that did not make it is the one thing
    // worth stopping for, and the seat page is where that is said.
    await router.replace(
      receipt.whyLost
        ? { name: 'seat-mine', query: { claimed: 'why-lost' } }
        : { name: 'order' },
    )
  } catch (e) {
    failure.value = seatErrorMessage(e)
    if ((e as { status?: number }).status === 409) {
      selected.value = null
      taken.value = TAKEN
      void refresh()
    }
  } finally {
    busy.value = false
  }
}

// Somebody who already has a seat has nothing to claim: this page is once.
onMounted(async () => {
  try {
    username.value = await currentUsername()
    if ((await findMySeat(username.value)) !== null) {
      markSeated()
      await router.replace({ name: 'seat-mine' })
    }
  } catch {
    // Claiming will say what is wrong, if anything is.
  }
})
</script>

<template>
  <AppShell title="Your seat">
    <section class="panel seat-page">
      <h1>Where are you sitting?</h1>
      <p class="hero-copy">
        Row 1 is closest to the stage.
      </p>

      <p v-if="loadError" role="alert" class="error-copy">{{ loadError }}</p>

      <div v-if="plan === null" class="empty-state">
        The seating plan isn't set up yet. It will appear here as soon as it
        is.
        <RouterLink class="text-link" to="/">Skip for now</RouterLink>
      </div>

      <template v-else-if="plan">
        <SeatMap
          :plan="plan"
          :seats="seats"
          :selected="selected"
          :preview="{ color, emoji }"
          selectable
          @select="select"
        />

        <template v-if="selected">
          <p class="seat-page__place">
            Row {{ selected.row }}, seat {{ selected.column }}
          </p>
          <p class="seat-page__hint">
            No problem if it's not exact, it is what it is now.
          </p>
        </template>
        <p v-else-if="taken" class="field__error seat-page__taken" role="alert">
          {{ taken }}
        </p>
        <p v-else-if="seatError" class="field__error" role="alert">
          {{ seatError }}
        </p>
        <p v-else class="seat-page__hint">
          Tap a free seat. Just do it approximately.
        </p>

        <SeatLookPicker v-model:color="color" v-model:emoji="emoji" />

        <SeatWhyField
          v-model="why"
          label="Why this seat, this colour and this emoji?"
          placeholder="Near the door, and foxes are clever"
          :error="whyError"
        />

        <p v-if="failure" role="alert" class="error-copy">{{ failure }}</p>
        <p v-else-if="closed" class="seat-page__hint" role="status">
          {{ SEAT_CLAIM_CLOSED }}
        </p>

        <div class="seat-page__actions">
          <button class="button" :disabled="busy || closed" @click="claim">
            {{ busy ? 'Claiming…' : 'Claim' }}
          </button>
        </div>
      </template>
    </section>
  </AppShell>
</template>
