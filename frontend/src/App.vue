<script setup lang="ts">
import BuildAgeBadge from './components/layout/BuildAgeBadge.vue'
import SessionIdentityBadge from './components/layout/SessionIdentityBadge.vue'
</script>

<template>
  <div class="app-badges">
    <SessionIdentityBadge />
    <BuildAgeBadge />
  </div>
  <RouterView />
</template>
