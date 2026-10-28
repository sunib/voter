<script setup lang="ts">
// Which build of this app is running, and what it is built on.
//
// It used to be a "builds" badge in every page's top bar. It is useful to the
// person running the demo and to the curious, and to nobody else, so it lives
// under "Not today" now, with links to the code of every part.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import AppShell from '../components/layout/AppShell.vue'
import { getPublicBuildInfo } from '../api/coffee'
import {
  buildInfo,
  createBuildInfoSummary,
  getBuildAgeLabel,
  type BuildInfoSummary,
} from '../buildInfo'

const VOTER = 'https://github.com/sunib/voter'

/** What I built for this demo. */
const mine = [
  {
    name: 'Voter',
    url: VOTER,
    what: 'This app: the seat map, the coffee bar, the quizzes.',
  },
  {
    name: 'Room Pass',
    url: 'https://github.com/sunib/room-pass',
    what: 'The door: the join code and the name you chose.',
  },
  {
    name: 'krm-foyer',
    url: 'https://github.com/ConfigButler/krm-foyer',
    what: 'A Kubernetes API proxy for the browser. It keeps your session and sends every request with your own token.',
  },
  {
    name: 'krm-stream',
    url: 'https://github.com/ConfigButler/krm-stream',
    what: 'Live updates: what changes elsewhere shows up here.',
  },
  {
    name: 'gitops-reverser',
    url: 'https://github.com/ConfigButler/gitops-reverser',
    what: 'Writes down every change, in the name of who made it.',
  },
] as const

/** The open source it all stands on. */
const underneath = [
  {
    name: 'Kubernetes',
    url: 'https://github.com/kubernetes/kubernetes',
    what: 'Where everything is stored and checked.',
  },
  {
    name: 'Talos Linux',
    url: 'https://github.com/siderolabs/talos',
    what: 'The operating system of the cluster.',
  },
  {
    name: 'Flux',
    url: 'https://github.com/fluxcd/flux2',
    what: 'Keeps the cluster in line with its configuration.',
  },
  { name: 'Dex', url: 'https://github.com/dexidp/dex', what: 'Signs you in.' },
  {
    name: 'Traefik',
    url: 'https://github.com/traefik/traefik',
    what: 'Routes every request to the right app.',
  },
  {
    name: 'Vue',
    url: 'https://github.com/vuejs/core',
    what: 'What this page is built with.',
  },
] as const

const groups = [
  {
    title: 'Made by me',
    intro: 'Open source, built for demos like this one.',
    parts: mine,
  },
  {
    title: 'Open source it runs on',
    intro: 'Doing the heavy lifting behind the scenes.',
    parts: underneath,
  },
]

const now = ref(Date.now())
const backend = ref<BuildInfoSummary | null>(null)
const backendState = ref<'loading' | 'loaded' | 'failed'>('loading')

/** A commit becomes a link only when there is one to link to. */
function commitURL(summary: BuildInfoSummary): string | undefined {
  return summary.gitCommit === 'unknown'
    ? undefined
    : `${VOTER}/commit/${summary.gitCommit}`
}

const builds = computed(() => [
  { label: 'Frontend', summary: buildInfo },
  { label: 'Backend', summary: backend.value },
])

let timer: ReturnType<typeof window.setInterval> | undefined
onMounted(async () => {
  timer = window.setInterval(() => {
    now.value = Date.now()
  }, 60000)
  try {
    backend.value = createBuildInfoSummary(await getPublicBuildInfo())
    backendState.value = 'loaded'
  } catch {
    backendState.value = 'failed'
  }
})
onBeforeUnmount(() => {
  if (timer !== undefined) window.clearInterval(timer)
})
</script>

<template>
  <AppShell title="Build info">
    <section class="panel">
      <h1>Build info</h1>
      <p class="hero-copy">Which build of this app is running.</p>

      <dl class="tech-facts">
        <div v-for="row in builds" :key="row.label" class="tech-facts__row">
          <dt>{{ row.label }}</dt>
          <dd v-if="row.summary">
            <a
              v-if="commitURL(row.summary)"
              class="text-link"
              :href="commitURL(row.summary)"
              target="_blank"
              rel="noopener"
              >{{ row.summary.commitWithDirty }}</a
            >
            <template v-else>{{ row.summary.commitWithDirty }}</template>
            · built {{ getBuildAgeLabel(row.summary.buildTimestamp, now) }} ago
            <span class="build-info__date">{{ row.summary.buildDate }}</span>
          </dd>
          <dd v-else-if="backendState === 'loading'">Loading…</dd>
          <dd v-else>Unavailable</dd>
        </div>
      </dl>
    </section>

    <section v-for="group in groups" :key="group.title" class="panel">
      <h2>{{ group.title }}</h2>
      <p class="hero-copy">{{ group.intro }}</p>
      <ul class="build-info__parts">
        <li v-for="part in group.parts" :key="part.name">
          <a
            class="text-link"
            :href="part.url"
            target="_blank"
            rel="noopener"
            >{{ part.name }}</a
          >
          <span>{{ part.what }}</span>
        </li>
      </ul>
    </section>
  </AppShell>
</template>
