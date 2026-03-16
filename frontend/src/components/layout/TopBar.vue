<script setup lang="ts">
import { computed } from 'vue'
import SessionStateBanner from '../session/SessionStateBanner.vue'
import { useSessionStore } from '../../stores/session'
import { buildInfo } from '../../buildInfo'

const props = defineProps<{ title?: string }>()
const sessionStore = useSessionStore()

const sessionState = computed(() => sessionStore.currentInfo?.data?.state)
const sessionTitle = computed(() => sessionStore.currentInfo?.data?.title)
const displayTitle = computed(() => sessionTitle.value ?? props.title ?? 'Attendee')
</script>

<template>
  <header class="sticky top-0 z-10 border-b border-black/10 bg-[rgb(var(--bg))]/90 backdrop-blur">
    <div class="mx-auto flex w-full max-w-md items-center justify-between gap-3 px-4 py-3">
      <div class="min-w-0">
        <div class="text-xs font-bold tracking-[0.18em] text-black/50">PRESENT.YAML</div>
        <div class="truncate text-base font-semibold">{{ displayTitle }}</div>
        <div class="mt-0.5 text-[10px] leading-tight text-black/40">
          <span>commit {{ buildInfo.commitWithDirty }}</span>
          <span class="mx-1">•</span>
          <span>built {{ buildInfo.buildDate }}</span>
        </div>
      </div>

      <div class="flex items-center gap-2">
        <SessionStateBanner :state="sessionState" />
        <div class="h-2.5 w-2.5 rounded-full bg-[rgb(var(--accent))] shadow-[0_0_0_4px_rgba(0,214,255,0.12)]" />
      </div>
    </div>
  </header>
</template>
