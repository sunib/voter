<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

const route = useRoute()

const currentRouteName = computed(() => route.name)

function isActive(name: string): boolean {
  return currentRouteName.value === name
}
</script>

<template>
  <div class="admin-nav">
    <RouterLink
      :class="['button', isActive('admin') ? '' : 'button--secondary']"
      to="/admin"
    >
      Config
    </RouterLink>
    <RouterLink
      :class="['button', isActive('admin-orders') ? '' : 'button--secondary']"
      to="/admin/orders"
    >
      Orders
    </RouterLink>
  </div>
</template>
