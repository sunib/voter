<script setup lang="ts">
import Tag from 'primevue/tag'

defineProps<{ state?: 'draft' | 'live' | 'closed' }>()

const meta = (state?: 'draft' | 'live' | 'closed') => {
  if (state === 'live') return { label: 'LIVE', severity: 'success' as const }
  if (state === 'draft') return { label: 'NOT LIVE', severity: 'warn' as const }
  if (state === 'closed') return { label: 'CLOSED', severity: 'danger' as const }
  return { label: 'UNKNOWN', severity: 'secondary' as const }
}
</script>

<template>
  <Tag :severity="meta(state).severity" class="!rounded-full !text-xs !font-bold !tracking-[0.18em]">
    {{ meta(state).label }}
  </Tag>
</template>
