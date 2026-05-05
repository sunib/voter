<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { getPublicBuildInfo } from '../../api/coffee'
import {
  buildInfo,
  createBuildInfoSummary,
  getBuildAgeLabel,
  type BuildInfoSummary,
} from '../../buildInfo'

const now = ref(Date.now())
const backendBuildInfo = ref<BuildInfoSummary | null>(null)
const backendLoading = ref(true)
const backendLoadFailed = ref(false)

let buildAgeTimer: ReturnType<typeof window.setInterval> | undefined

const frontendBuildAge = computed(() =>
  getBuildAgeLabel(buildInfo.buildTimestamp, now.value),
)
const backendBuildAge = computed(() =>
  backendBuildInfo.value
    ? getBuildAgeLabel(backendBuildInfo.value.buildTimestamp, now.value)
    : 'unknown',
)

const badgeLabel = computed(() => {
  if (backendBuildInfo.value) {
    return 'builds'
  }
  if (backendLoadFailed.value) {
    return `build-age: ${frontendBuildAge.value}`
  }
  return 'builds'
})

async function loadBackendBuildInfo() {
  backendLoading.value = true
  try {
    const payload = await getPublicBuildInfo()
    backendBuildInfo.value = createBuildInfoSummary(payload)
    backendLoadFailed.value = false
  } catch (error) {
    backendBuildInfo.value = null
    backendLoadFailed.value = true
  } finally {
    backendLoading.value = false
  }
}

onMounted(() => {
  void loadBackendBuildInfo()

  buildAgeTimer = window.setInterval(() => {
    now.value = Date.now()
  }, 60000)
})

onBeforeUnmount(() => {
  if (buildAgeTimer !== undefined) {
    window.clearInterval(buildAgeTimer)
  }
})
</script>

<template>
  <div class="pointer-events-auto group relative">
    <button
      type="button"
      class="inline-flex rounded-full border border-black/8 bg-[rgb(var(--card))]/82 px-2 py-1 text-[10px] font-medium tracking-[0.08em] text-black/38 shadow-[0_6px_18px_rgba(17,12,8,0.06)] backdrop-blur transition-colors hover:border-black/15 hover:bg-[rgb(var(--card))]/94 hover:text-black/58 focus:border-black/15 focus:bg-[rgb(var(--card))]/94 focus:text-black/58 focus:outline-none"
      aria-label="Build metadata"
    >
      {{ badgeLabel }}
    </button>

    <div
      class="invisible absolute right-0 top-full mt-2 w-64 rounded-2xl border border-black/10 bg-[rgb(var(--card))]/96 p-3 text-left text-[11px] text-[rgb(var(--ink))] opacity-0 shadow-[0_18px_40px_rgba(17,12,8,0.12)] backdrop-blur transition-all duration-150 group-hover:visible group-hover:opacity-100 group-focus-within:visible group-focus-within:opacity-100"
    >
      <div class="space-y-2">
        <div>
          <div class="flex items-center justify-between gap-3 text-black/65">
            <span class="font-semibold tracking-[0.08em]">frontend-build</span>
            <span>{{ frontendBuildAge }}</span>
          </div>
          <div class="mt-0.5 text-black/48">
            {{ buildInfo.commitWithDirty }} • {{ buildInfo.buildDate }}
          </div>
        </div>

        <div v-if="backendBuildInfo">
          <div class="flex items-center justify-between gap-3 text-black/65">
            <span class="font-semibold tracking-[0.08em]">backend-build</span>
            <span>{{ backendBuildAge }}</span>
          </div>
          <div class="mt-0.5 text-black/48">
            {{ backendBuildInfo.commitWithDirty }} •
            {{ backendBuildInfo.buildDate }}
          </div>
        </div>

        <div v-else-if="backendLoading" class="text-black/48">
          backend-build loading…
        </div>

        <div v-else class="text-black/48">backend-build unavailable</div>
      </div>
    </div>
  </div>
</template>
