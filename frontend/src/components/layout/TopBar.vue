<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'

import { shopOnly } from '../../shopOnly'
import BuildAgeBadge from './BuildAgeBadge.vue'
import SessionIdentityBadge from './SessionIdentityBadge.vue'
import type { ShellWidth } from './shellWidth'
import { shellWidthClass } from './shellWidth'

const props = withDefaults(
  defineProps<{ title?: string; width?: ShellWidth }>(),
  { title: undefined, width: 'default' },
)

const route = useRoute()

// What this talk is about gets a tab: your seat, the coffee bar, and the
// presenter's room. The demos that are not on today's programme -- quizzes
// and databases -- still work, one tap further away under "Not today", so a
// phone's bar holds only what the room is asked to use.
const allTabs = [
  { key: 'seat', to: '/seat/mine', label: 'Seat' },
  { key: 'coffee', to: '/coffee', label: 'Coffee' },
  { key: 'room', to: '/room', label: 'Room' },
] as const
const notToday = [
  { key: 'quizzes', to: '/', label: 'Quizzes' },
  { key: 'databases', to: '/databases', label: 'Databases' },
] as const
// Production is the coffee bar alone, and says so where the demo says its name.
const production = shopOnly()
const tabs = production ? allTabs.filter((tab) => tab.key === 'coffee') : allTabs

// By route name, not by path prefix: /answer/:session and /thanks live at the
// root but belong to a half each, and RouterLink's own active matching would
// light up "Quizzes" for every route in the app because its path is "/".
const coffeeRoutes = new Set(['order', 'thanks', 'admin', 'admin-orders'])
const databaseRoutes = new Set(['databases', 'database-new', 'database-edit'])
const activeTab = computed(() => {
  const name = String(route.name ?? '')
  if (name === 'room') return 'room'
  if (name === 'seat-claim' || name === 'seat-mine') return 'seat'
  if (databaseRoutes.has(name)) return 'databases'
  return coffeeRoutes.has(name) ? 'coffee' : 'quizzes'
})

const notTodayActive = computed(() =>
  notToday.some((item) => item.key === activeTab.value),
)

// A native <details>, so the menu opens without script on any phone. It closes
// on a tap anywhere else, and after you pick something.
const menu = ref<HTMLDetailsElement | null>(null)
function closeOnOutside(event: PointerEvent) {
  if (menu.value?.open && !menu.value.contains(event.target as Node)) {
    menu.value.open = false
  }
}
document.addEventListener('pointerdown', closeOnOutside)
onBeforeUnmount(() =>
  document.removeEventListener('pointerdown', closeOnOutside),
)
watch(
  () => route.fullPath,
  () => {
    if (menu.value) menu.value.open = false
  },
)

const widthClass = computed(() => shellWidthClass(props.width))
</script>

<template>
  <header class="top-bar sticky-surface">
    <!-- The track carries the width and is the query container; the grid inside
         it reacts to the track's width, not the viewport's, so the bar still
         stacks correctly when a screen asks for a narrow column. -->
    <div class="top-bar__track page-shell" :class="widthClass">
      <div class="top-bar__inner">
        <RouterLink :to="production ? '/coffee' : '/'" class="top-bar__brand">
          <span class="top-bar__eyebrow">{{ production ? 'Production' : 'YAML Voter' }}</span>
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
          <details v-if="!production" ref="menu" class="nav-more">
            <summary
              class="nav-tab"
              :class="{ 'nav-tab--active': notTodayActive }"
            >
              Not today
            </summary>
            <div class="nav-more__menu">
              <RouterLink
                v-for="item in notToday"
                :key="item.key"
                :to="item.to"
                class="nav-more__item"
                :aria-current="activeTab === item.key ? 'page' : undefined"
              >
                {{ item.label }}
              </RouterLink>
            </div>
          </details>
        </nav>

        <div class="top-bar__badges">
          <SessionIdentityBadge />
          <BuildAgeBadge />
        </div>
      </div>
    </div>
  </header>
</template>
