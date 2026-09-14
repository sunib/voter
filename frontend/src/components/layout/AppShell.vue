<script setup lang="ts">
import { computed } from 'vue'

import TopBar from './TopBar.vue'
import type { ShellWidth } from './shellWidth'
import { shellWidthClass } from './shellWidth'

const props = withDefaults(
  defineProps<{ title?: string; width?: ShellWidth }>(),
  { title: undefined, width: 'default' },
)

const widthClass = computed(() => shellWidthClass(props.width))
</script>

<template>
  <div class="min-h-screen">
    <TopBar :title="title" :width="width" />
    <main class="page-shell" :class="widthClass">
      <slot />
    </main>
  </div>
</template>
