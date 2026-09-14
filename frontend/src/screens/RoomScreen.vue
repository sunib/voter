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
import { computed, ref, watch } from 'vue'
import { toString as qrToString } from 'qrcode/lib/browser'

import AppShell from '../components/layout/AppShell.vue'
import StreamDiagnostics from '../components/layout/StreamDiagnostics.vue'
import { useLiveResources } from '../api/liveResources'
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

    <section v-if="roomDenied" class="panel panel--danger">
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

    <section v-else class="panel">
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
