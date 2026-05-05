<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

const route = useRoute()

const currentRouteName = computed(() => route.name)
const tabs = [
  {
    name: 'order',
    to: '/',
    label: 'Order',
  },
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
  <nav class="admin-tabs" aria-label="Admin sections">
    <RouterLink
      v-for="tab in tabs"
      :key="tab.name"
      :class="['admin-tab', isActive(tab.name) ? 'admin-tab--active' : '']"
      :to="tab.to"
    >
      {{ tab.label }}
    </RouterLink>
  </nav>
</template>
