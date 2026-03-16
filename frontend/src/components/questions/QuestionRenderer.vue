<script setup lang="ts">
import type { QuizSessionSpec } from '../../api/types'

import Button from 'primevue/button'
import Checkbox from 'primevue/checkbox'
import InputNumber from 'primevue/inputnumber'
import Message from 'primevue/message'
import Slider from 'primevue/slider'
import Tag from 'primevue/tag'
import Textarea from 'primevue/textarea'
import { computed } from 'vue'

const props = defineProps<{
  question: NonNullable<QuizSessionSpec['questions']>[number]
  modelValue: unknown
}>()

const emit = defineEmits<{ 'update:modelValue': [value: unknown] }>()

const multiValue = computed<string[]>({
  get: () => (Array.isArray(props.modelValue) ? (props.modelValue as string[]) : []),
  set: (v) => emit('update:modelValue', v),
})

const numberValue = computed<number | null>({
  get: () => (typeof props.modelValue === 'number' && Number.isFinite(props.modelValue) ? (props.modelValue as number) : null),
  set: (v) => emit('update:modelValue', typeof v === 'number' ? v : undefined),
})

const scaleIsSet = computed(() => typeof props.modelValue === 'number' && Number.isFinite(props.modelValue))

// UX: a scale question can be "unset".
// We keep the slider positioned at a neutral midpoint until the user interacts,
// while the displayed value stays blank until set.
const scaleUiValue = computed<number>({
  get: () => (scaleIsSet.value ? (props.modelValue as number) : 5),
  set: (v) => emit('update:modelValue', v),
})
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-4">
      <div class="min-w-0">
        <h2 class="text-xl font-extrabold">{{ question.title }}</h2>
        <p v-if="question.type === 'scale0to10'" class="mt-1 text-sm text-black/60">0 = low, 10 = high</p>
      </div>

      <Tag v-if="question.required" severity="warn" class="!rounded-full !text-[11px] !font-bold !tracking-[0.18em]">
        REQUIRED
      </Tag>
    </div>

    <!-- singleChoice -->
    <div v-if="question.type === 'singleChoice'" class="grid gap-2">
      <Button
        v-for="c in question.choices ?? []"
        :key="c"
        type="button"
        :outlined="modelValue !== c"
        :severity="modelValue === c ? 'info' : 'secondary'"
        class="!justify-between !rounded-xl !py-3 !text-left !font-semibold"
        @click="emit('update:modelValue', c)"
      >
        <span class="min-w-0 truncate">{{ c }}</span>
        <i v-if="modelValue === c" class="pi pi-check" />
      </Button>
    </div>

    <!-- multiChoice -->
    <div v-else-if="question.type === 'multiChoice'" class="grid gap-3">
      <label
        v-for="c in question.choices ?? []"
        :key="c"
        class="flex cursor-pointer items-center justify-between gap-3 rounded-xl border border-black/10 bg-white px-4 py-3 transition hover:border-black/20"
      >
        <div class="flex min-w-0 items-center gap-3">
          <Checkbox v-model="multiValue" :value="c" />
          <span class="min-w-0 truncate font-semibold">{{ c }}</span>
        </div>
        <Tag
          :severity="Array.isArray(modelValue) && modelValue.includes(c) ? 'info' : 'secondary'"
          class="!rounded-full !text-[11px] !font-bold !tracking-[0.18em]"
        >
          {{ Array.isArray(modelValue) && modelValue.includes(c) ? 'ON' : 'OFF' }}
        </Tag>
      </label>
    </div>

    <!-- scale0to10 -->
    <div v-else-if="question.type === 'scale0to10'" class="space-y-3">
      <div class="flex items-center justify-between">
        <div class="text-xs font-bold tracking-[0.18em] text-black/55">SCORE</div>
        <Tag
          :severity="scaleIsSet ? 'info' : 'secondary'"
          class="!rounded-full !text-[11px] !font-bold !tracking-[0.18em]"
        >
          {{ scaleIsSet ? scaleUiValue : '—' }}
        </Tag>
      </div>

      <div :class="scaleIsSet ? '' : 'opacity-85'">
        <Slider v-model="scaleUiValue" :min="0" :max="10" :step="1" />
      </div>

      <div class="flex items-center justify-between text-[11px] font-semibold text-black/55">
        <span>0</span>
        <span>10</span>
      </div>

      <div v-if="!scaleIsSet" class="text-xs text-black/55">Drag the slider to set your score.</div>
    </div>

    <!-- number -->
    <div v-else-if="question.type === 'number'" class="grid gap-2">
      <InputNumber
        v-model="numberValue"
        :min="question.min"
        :max="question.max"
        :use-grouping="false"
        :input-id="`q_${question.id}_number`"
        class="w-full"
      />
      <div v-if="question.min !== undefined || question.max !== undefined" class="text-xs text-black/55">
        <span v-if="question.min !== undefined">min {{ question.min }}</span>
        <span v-if="question.min !== undefined && question.max !== undefined"> · </span>
        <span v-if="question.max !== undefined">max {{ question.max }}</span>
      </div>
    </div>

    <!-- freeText -->
    <div v-else-if="question.type === 'freeText'" class="grid gap-2">
      <Textarea
        auto-resize
        rows="3"
        class="w-full"
        :placeholder="question.placeholder ?? ''"
        :model-value="typeof modelValue === 'string' ? modelValue : ''"
        @update:model-value="(v) => emit('update:modelValue', v)"
      />
    </div>

    <Message v-else severity="warn" class="!rounded-xl">
      Unknown question type: <code class="font-mono">{{ question.type }}</code>
    </Message>
  </div>
</template>
