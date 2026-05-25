<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

const route = useRoute()

const currentRouteName = computed(() => route.name)
const tabs = [
  {
    name: 'admin-orders',
    to: '/admin/orders',
    label: 'List',
  },
  {
    name: 'admin',
    to: '/admin',
    label: 'Config',
  },
  {
    name: 'admin-commits',
    to: '/admin/commits',
    label: 'ConfigHistory',
  },
] as const

function isActive(name: string): boolean {
  return currentRouteName.value === name
}
</script>

<template>
  <nav class="admin-nav" aria-label="Admin sections">
    <RouterLink to="/" class="admin-back" aria-label="Back to order">
      <svg
        class="admin-back__icon"
        viewBox="0 0 24 24"
        width="20"
        height="20"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M15 18l-6-6 6-6" />
      </svg>
    </RouterLink>

    <div class="admin-tabs">
      <RouterLink
        v-for="tab in tabs"
        :key="tab.name"
        :class="['admin-tab', isActive(tab.name) ? 'admin-tab--active' : '']"
        :to="tab.to"
      >
        {{ tab.label }}
      </RouterLink>
    </div>
  </nav>
</template>
