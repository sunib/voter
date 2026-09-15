<script setup lang="ts">
// The operator's page. Everything here is guarded by Kubernetes, not by a check
// in this component.
//
// room-pass/cmd/room-qr argues that the join code belongs in a terminal tool
// rather than a web page, because it is operator-only credential material and a
// page would mean "building an operator login for that page and getting it
// exactly right". This page takes that bet, and the way it stays honest is that
// it never asks who you are: it opens the same watch anyone could ask for, with
// your own token, and renders whatever Kubernetes is willing to send. A
// participant's RBAC grants nothing on rooms, so a participant sees the refusal
// below and no code -- the demo is better when the refusal is real.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { toString as qrToString } from 'qrcode/lib/browser'

import AppShell from '../components/layout/AppShell.vue'
import StreamDiagnostics from '../components/layout/StreamDiagnostics.vue'
import { useLiveResources } from '../api/liveResources'
import {
  getAudienceGrant,
  missingPermissions,
  setAudienceGrant,
  ROOM_REQUIREMENTS,
} from '../api/authz'
import { useAuthorization } from '../api/useAuthorization'
import PermissionRequirements from '../components/PermissionRequirements.vue'
import { setRoundState } from '../api/quiz'
import { currentSession } from '../api/session'
import type { KRMObject } from '@configbutler/krm-stream'

const session = currentSession()
const namespace = session?.namespace ?? ''

const roomScope = {
  group: 'roompass.configbutler.ai',
  version: 'v1alpha1',
  resource: 'rooms',
  namespace,
  name: session?.roomName ?? 'demo',
}
const room = useLiveResources(roomScope)

const rounds = useLiveResources({
  group: 'examples.configbutler.ai',
  version: 'v1alpha1',
  resource: 'quizsessions',
  namespace,
})

type RoomObject = KRMObject & {
  spec?: { title?: string; enrollment?: string; endsAt?: string }
  status?: {
    joinCode?: { code?: string; expiresAt?: string }
    participantCount?: number
  }
}

type RoundObject = KRMObject & {
  spec?: { title?: string; state?: string }
}

const theRoom = computed<RoomObject | undefined>(
  () => room.items.value[0] as RoomObject | undefined,
)
const joinCode = computed(() => theRoom.value?.status?.joinCode?.code ?? '')

// Three different answers, and telling them apart is the whole point. A
// participant is REFUSED, which is the demo working. An expired session is
// neither refused nor broken. Anything else is a fault in the demo itself --
// and the first one in production was exactly that: the shared watch could not
// read the Room, and calling it a permission problem sent the operator looking
// for the wrong thing.
// What this identity is missing for the operator page, from the API server's
// own answer about its token. The stream's refusal below says THAT you were
// refused; this says which grant would have made the difference, which is the
// question anyone actually has.
const { authz, error: authzError, loading: authzLoading } = useAuthorization()
const missingForRoom = computed(() =>
  missingPermissions(authz.value, ROOM_REQUIREMENTS),
)

// Two very different situations wear the same panel, and the headline has to
// tell them apart. Without `get rooms` there is no operator page at all -- that
// is the participant, and the demo's own words for it are "You cannot run this
// room". With the room readable but a grant or two missing, the page mostly
// works and only some controls are absent; calling that "you cannot run this
// room" in front of an operator holding a working join code would be wrong.
const cannotReadRoom = computed(() =>
  missingForRoom.value.some((r) => r.resource === 'rooms' && r.verb === 'get'),
)
const permissionTitle = computed(() =>
  cannotReadRoom.value
    ? 'You cannot run this room'
    : 'Some of this room’s controls are not yours',
)

const roomDenied = computed(() => room.denied())
const roomExpired = computed(() => room.expired())
const roomFaulted = computed(() => room.faulted())
const roomUnavailable = computed(
  () => roomDenied.value || roomExpired.value || roomFaulted.value,
)

const sortedRounds = computed(() =>
  (rounds.items.value as RoundObject[])
    .filter((r) => r.spec?.state !== 'draft')
    .sort((a, b) =>
      (a.metadata?.name ?? '').localeCompare(b.metadata?.name ?? ''),
    ),
)

// The QR carries the same two parameters room-qr uses -- "code" so nobody types
// it, "return" so they land on the quizzes rather than the front page. Keeping
// the contract identical means the terminal tool and this page stay swappable.
const qrSvg = ref('')
const joinUrl = computed(() =>
  joinCode.value
    ? `${window.location.origin}/auth/login?code=${encodeURIComponent(joinCode.value)}&return=%2F`
    : '',
)

watch(
  joinUrl,
  async (url) => {
    if (!url) {
      qrSvg.value = ''
      return
    }
    qrSvg.value = await qrToString(url, {
      type: 'svg',
      margin: 1,
      errorCorrectionLevel: 'M',
    })
  },
  { immediate: true },
)

const busyRound = ref('')
const roundError = ref('')

async function changeState(name: string, state: 'live' | 'closed') {
  busyRound.value = name
  roundError.value = ''
  try {
    await setRoundState(name, state)
    // No local mutation and no refetch: the watch is the source of truth, and
    // seeing the button's effect arrive over the stream is the demo.
  } catch (e) {
    roundError.value =
      e instanceof Error ? e.message : 'Could not change the round.'
  } finally {
    busyRound.value = ''
  }
}

// --- what the audience may do ----------------------------------------------
//
// The switch creates or deletes ONE RoleBinding, with this operator's own
// token. There is no admin check in front of it: reading the grant needs
// permission on rolebindings, which a participant does not have, so a
// participant simply never sees the control -- Kubernetes decided that, not
// this component.
//
// It is polled as well as written, so the switch still tells the truth if the
// binding is changed from a terminal or by a second operator.
const grantGranted = ref(false)
const grantVisible = ref(false)
const grantBusy = ref(false)
const grantError = ref('')
const grantLoaded = ref(false)

async function refreshGrant() {
  try {
    const state = await getAudienceGrant()
    grantGranted.value = state.granted
    grantVisible.value = true
    grantError.value = ''
  } catch (e) {
    // 403 is the expected answer for a participant and is not an error worth
    // showing: they are not an operator, so the control is simply not theirs.
    // Anything else is a real fault and belongs on screen.
    const status = (e as { status?: number }).status
    if (status === 403 || status === 401) {
      grantVisible.value = false
    } else {
      grantVisible.value = true
      grantError.value =
        e instanceof Error ? e.message : 'Could not read the audience grant.'
    }
  } finally {
    grantLoaded.value = true
  }
}

async function toggleGrant(next: boolean) {
  grantBusy.value = true
  grantError.value = ''
  // Optimistic, then corrected by the answer. The operator is standing in
  // front of a room; the control must move when it is pressed.
  const previous = grantGranted.value
  grantGranted.value = next
  try {
    const state = await setAudienceGrant(next)
    grantGranted.value = state.granted
  } catch (e) {
    grantGranted.value = previous
    grantError.value =
      e instanceof Error ? e.message : 'Could not change the audience grant.'
  } finally {
    grantBusy.value = false
  }
}

void refreshGrant()
const grantTimer = setInterval(() => void refreshGrant(), 5000)
onBeforeUnmount(() => clearInterval(grantTimer))

function signInAsOperator() {
  // Allowlisted by OIDC_CONNECTOR_CHOICES, not free text; the default path
  // stays room-pass so a room full of strangers is never shown this door.
  window.location.assign('/auth/login?connector=github&return=%2Froom')
}
</script>

<template>
  <AppShell title="Room">
    <section class="hero-card">
      <p class="eyebrow">Operator</p>
      <h1 class="panel-title">{{ theRoom?.spec?.title ?? 'This room' }}</h1>
      <p class="hero-copy">
        Signed in as <strong>{{ session?.displayName }}</strong
        >. What you can do here is decided by Kubernetes RBAC, not by this page.
      </p>
    </section>

    <!-- Self-hiding: renders only when something this page uses is missing.
         It covers both shapes of problem, which the stream cannot tell apart --
         a participant who holds none of these, and an operator who can read the
         room but not hand out the audience grant. In the second case the join
         code and the rounds below keep working, and only the switch is gone. -->
    <PermissionRequirements
      :authz="authz"
      :loading="authzLoading"
      :error="authzError"
      :requirements="ROOM_REQUIREMENTS"
      :title="permissionTitle"
      highlight="rooms"
    >
      <div class="hero-actions">
        <button class="button" @click="signInAsOperator">
          Sign in with GitHub
        </button>
        <RouterLink class="text-link" to="/">Back to the quizzes</RouterLink>
      </div>
    </PermissionRequirements>

    <!-- Only when the rules review does NOT already explain it. A participant
         gets the itemised panel above instead of this one; keeping both would
         tell the same story twice, in red, both times. This still fires in the
         window before the first review answers, and if the stream and RBAC ever
         genuinely disagree -- which is worth seeing rather than hiding. -->
    <section
      v-if="roomDenied && !authzLoading && !missingForRoom.length"
      class="panel panel--danger"
    >
      <h2 class="panel-title">You cannot run this room</h2>
      <p class="hero-copy">
        Your identity may read and answer the quizzes, but not read this room.
        The join code is credential material, so Kubernetes refuses it — that
        refusal is the demo working, not a bug.
      </p>
      <p class="hero-copy">
        Room Pass gave you a participant identity. Running the room needs an
        operator one.
      </p>
      <div class="hero-actions">
        <button class="button" @click="signInAsOperator">
          Sign in with GitHub
        </button>
        <RouterLink class="text-link" to="/">Back to the quizzes</RouterLink>
      </div>
      <StreamDiagnostics :live="room" :scope="roomScope" :session="session" />
    </section>

    <section v-else-if="roomExpired" class="panel panel--danger">
      <h2 class="panel-title">Your session has expired</h2>
      <p class="hero-copy">
        Nothing is wrong with your permissions — the token this page was using
        has run out. Signing in again picks up where you left off.
      </p>
      <div class="hero-actions">
        <button class="button" @click="signInAsOperator">
          Sign in with GitHub
        </button>
        <RouterLink class="text-link" to="/">Back to the quizzes</RouterLink>
      </div>
      <StreamDiagnostics :live="room" :scope="roomScope" :session="session" />
    </section>

    <section v-else-if="roomFaulted" class="panel panel--danger">
      <h2 class="panel-title">The room could not be loaded</h2>
      <p class="hero-copy">
        This is not a permissions problem: Kubernetes did not refuse you, the
        stream failed. The most common cause is the application's own
        ServiceAccount lacking <code>list</code>/<code>watch</code> on this
        resource — the shared watch is opened as the application, and only then
        is each subscriber authorized as itself.
      </p>
      <div class="hero-actions">
        <RouterLink class="text-link" to="/">Back to the quizzes</RouterLink>
      </div>
      <StreamDiagnostics :live="room" :scope="roomScope" :session="session" />
    </section>

    <section v-else-if="!roomDenied" class="panel">
      <div class="section-heading">
        <h2 class="panel-title">Join code</h2>
        <span
          class="pill"
          :class="room.synced() ? 'pill--good' : 'pill--warning'"
        >
          {{ room.synced() ? 'live' : room.state.value.status }}
        </span>
      </div>
      <p class="hero-copy">
        Point a projector at this. The code rotates on its own and the QR
        follows it over the same watch — nothing here polls, and nobody has to
        refresh.
      </p>
      <div v-if="qrSvg" class="room-qr">
        <!-- eslint-disable-next-line vue/no-v-html -- qrcode renders its own SVG -->
        <div class="room-qr__code" v-html="qrSvg" />
        <div class="room-qr__meta">
          <p class="eyebrow">Code</p>
          <p class="room-qr__value">{{ joinCode }}</p>
          <p class="hero-copy">
            {{ theRoom?.status?.participantCount ?? 0 }} enrolled ·
            {{ theRoom?.spec?.enrollment ?? 'unknown' }}
          </p>
        </div>
      </div>
      <div v-else class="empty-state">Waiting for the room's join code…</div>
    </section>

    <!-- Demo 2's switch. One RoleBinding, created and deleted with your own
         token, and every phone in the room discovers the change by asking the
         API server what it may do -- not by being told by this application. -->
    <section v-if="!roomUnavailable && grantVisible" class="panel">
      <div class="section-heading">
        <h2 class="panel-title">What the audience may do</h2>
        <span
          class="pill"
          :class="grantGranted ? 'pill--good' : 'pill--neutral'"
        >
          {{ grantGranted ? 'menu editing granted' : 'read-only' }}
        </span>
      </div>
      <p class="hero-copy">
        The room can always read the coffee menu. This grants them
        <code class="inline-code">patch</code> on it as well, by creating a
        RoleBinding — so the admin page stops refusing them, live, without
        anyone signing in again.
      </p>
      <p v-if="grantError" role="alert" class="error-copy">{{ grantError }}</p>
      <label class="grant-switch">
        <input
          type="checkbox"
          :checked="grantGranted"
          :disabled="grantBusy || !grantLoaded"
          @change="toggleGrant(($event.target as HTMLInputElement).checked)"
        />
        <span class="grant-switch__copy">
          <strong>Let the room edit the coffee menu</strong>
          <span class="grant-switch__hint">
            {{
              grantBusy
                ? 'Asking Kubernetes…'
                : grantGranted
                  ? 'RoleBinding exists. Their admin page is editable now.'
                  : 'No RoleBinding. Their admin page explains the refusal.'
            }}
          </span>
        </span>
      </label>
      <p class="metadata-copy">
        Not a Flux resource, on purpose — Flux would recreate whatever this
        deletes. gitops-reverser mirrors it to the audit trail, so the grant
        arrives in Git as a commit in your name.
      </p>
    </section>

    <section v-if="!roomUnavailable" class="panel">
      <div class="section-heading">
        <h2 class="panel-title">Quizzes</h2>
        <span
          class="pill"
          :class="rounds.synced() ? 'pill--good' : 'pill--warning'"
        >
          {{ rounds.synced() ? 'live' : rounds.state.value.status }}
        </span>
      </div>
      <p class="hero-copy">
        Opening or closing a round here patches the QuizSession directly. Every
        participant's page is watching the same objects, so their screen changes
        as you click.
      </p>
      <p v-if="roundError" role="alert" class="error-copy">{{ roundError }}</p>
      <div v-if="!sortedRounds.length" class="empty-state">
        No rounds yet. They are created in Git.
      </div>
      <div v-else class="round-list">
        <article
          v-for="round in sortedRounds"
          :key="round.metadata?.uid"
          class="round-card"
          :class="{ 'round-card--open': round.spec?.state === 'live' }"
        >
          <div class="round-card__body">
            <h3>{{ round.spec?.title }}</h3>
            <span
              class="pill"
              :class="round.spec?.state === 'live' ? 'pill--good' : ''"
            >
              {{
                round.spec?.state === 'live'
                  ? 'Voting is open'
                  : 'Voting is closed'
              }}
            </span>
          </div>
          <div class="round-card__actions">
            <button
              v-if="round.spec?.state !== 'live'"
              class="button"
              :disabled="busyRound === round.metadata?.name"
              @click="changeState(round.metadata!.name!, 'live')"
            >
              {{ busyRound === round.metadata?.name ? 'Opening…' : 'Open' }}
            </button>
            <button
              v-else
              class="button button--secondary"
              :disabled="busyRound === round.metadata?.name"
              @click="changeState(round.metadata!.name!, 'closed')"
            >
              {{ busyRound === round.metadata?.name ? 'Closing…' : 'Close' }}
            </button>
            <RouterLink
              class="text-link"
              :to="{
                name: 'vote-results',
                params: { session: round.metadata?.name },
              }"
            >
              Results
            </RouterLink>
          </div>
        </article>
      </div>
    </section>
  </AppShell>
</template>
