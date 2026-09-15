<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import BuildAgeBadge from './BuildAgeBadge.vue'
import SessionIdentityBadge from './SessionIdentityBadge.vue'
import type { ShellWidth } from './shellWidth'
import { shellWidthClass } from './shellWidth'

const props = withDefaults(
  defineProps<{ title?: string; width?: ShellWidth }>(),
  { title: undefined, width: 'default' },
)

const route = useRoute()

// Quizzes and coffee are the two halves of the demo, and neither is a
// sub-section of the other, so they get peer tabs rather than one being the
// home page and the other a link buried in a hero.
const tabs = [
  { key: 'quizzes', to: '/', label: 'Quizzes' },
  { key: 'coffee', to: '/coffee', label: 'Coffee' },
  { key: 'room', to: '/room', label: 'Room' },
] as const

// By route name, not by path prefix: /answer/:session and /thanks live at the
// root but belong to a half each, and RouterLink's own active matching would
// light up "Quizzes" for every route in the app because its path is "/".
const coffeeRoutes = new Set(['order', 'thanks', 'admin', 'admin-orders'])
const activeTab = computed(() => {
  const name = String(route.name ?? '')
  if (name === 'room') return 'room'
  return coffeeRoutes.has(name) ? 'coffee' : 'quizzes'
})

const widthClass = computed(() => shellWidthClass(props.width))
</script>

<template>
  <header class="top-bar sticky-surface">
    <!-- The track carries the width and is the query container; the grid inside
         it reacts to the track's width, not the viewport's, so the bar still
         stacks correctly when a screen asks for a narrow column. -->
    <div class="top-bar__track page-shell" :class="widthClass">
      <div class="top-bar__inner">
        <RouterLink to="/" class="top-bar__brand">
          <span class="top-bar__eyebrow">YAML Voter</span>
          <span class="top-bar__title">{{ title ?? 'Demo' }}</span>
        </RouterLink>

        <nav class="nav-tabs" aria-label="Demo sections">
          <RouterLink
            v-for="tab in tabs"
            :key="tab.key"
            :to="tab.to"
            class="nav-tab"
            :class="{ 'nav-tab--active': activeTab === tab.key }"
            :aria-current="activeTab === tab.key ? 'page' : undefined"
          >
            {{ tab.label }}
          </RouterLink>
        </nav>

        <div class="top-bar__badges">
          <SessionIdentityBadge />
          <BuildAgeBadge />
        </div>
      </div>
    </div>
  </header>
</template>
