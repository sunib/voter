<script setup lang="ts">
// The room as a cinema booking app draws it: the stage at the top, row 1
// closest to it, column 1 on the left as seen from the audience. The same
// orientation on every phone, so everyone reads it the same way.
//
// Names are not drawn: a seat is its colour and emoji. Whose it is shows in an
// accessible label, never on the map itself.
import { computed, nextTick, ref, watch } from 'vue'

import { seatGrid, type SeatCell } from '../../api/liveSeats'
import type { Seat, SeatingPlan } from '../../api/seats'

const props = withDefaults(
  defineProps<{
    plan: SeatingPlan
    seats: Seat[]
    /** The viewer's own seat, highlighted. */
    mine?: string
    /** The place being chosen, drawn in the look being chosen. */
    selected?: { row: number; column: number } | null
    preview?: { color: string; emoji: string }
    /** Free seats are buttons; otherwise the map only shows. */
    selectable?: boolean
    /** Draw this one row only, with big seats: the zoom for a phone. */
    onlyRow?: number
    large?: boolean
  }>(),
  {
    mine: undefined,
    selected: null,
    preview: undefined,
    selectable: false,
    onlyRow: undefined,
    large: false,
  },
)

const emit = defineEmits<{ select: [row: number, column: number] }>()

// A zoomed row is wider than a phone, so the chosen seat is brought to the
// middle of it rather than left past the edge. Sideways only: the page itself
// does not move under the person's thumb.
const scroller = ref<HTMLElement | null>(null)
watch(
  () => [props.selected?.row, props.selected?.column],
  async () => {
    if (!props.large) return
    await nextTick()
    const box = scroller.value
    const cell = box?.querySelector<HTMLElement>('.seat-cell--selected')
    if (!box || !cell) return
    box.scrollLeft = cell.offsetLeft - (box.clientWidth - cell.offsetWidth) / 2
  },
  { immediate: true },
)

const grid = computed(() => {
  const rows = seatGrid(props.plan.spec, props.seats)
  return props.onlyRow === undefined
    ? rows
    : rows.filter((cells) => cells[0]?.row === props.onlyRow)
})

const columnNumbers = computed(() =>
  Array.from({ length: props.plan.spec.columns }, (_, i) => i + 1),
)

function isSelected(cell: SeatCell): boolean {
  return (
    props.selected?.row === cell.row && props.selected?.column === cell.column
  )
}

function isMine(cell: SeatCell): boolean {
  return props.mine !== undefined && cell.seat?.metadata.name === props.mine
}

function canPick(cell: SeatCell): boolean {
  return props.selectable && cell.seat === undefined
}

/** What the seat looks like: its owner's choice, or the one being made. */
function look(cell: SeatCell): { color: string; emoji: string } | undefined {
  if (cell.seat) return { color: cell.seat.spec.color, emoji: cell.seat.spec.emoji }
  if (isSelected(cell) && props.preview) return props.preview
  return undefined
}

function label(cell: SeatCell): string {
  const place = `Row ${cell.row}, seat ${cell.column}`
  if (isMine(cell)) return `${place}, your seat`
  if (cell.seat) return `${place}, taken`
  if (isSelected(cell)) return `${place}, chosen`
  return `${place}, free`
}
</script>

<template>
  <div class="seat-map" :class="{ 'seat-map--large': large }">
    <div ref="scroller" class="seat-map__scroll">
      <div
        class="seat-map__grid"
        :style="{ '--seat-columns': plan.spec.columns }"
      >
        <!-- Once per page: the zoomed row sits under the full map, which
             already says where the stage is. -->
        <div v-if="!large" class="seat-map__stage" aria-hidden="true">Stage</div>
        <span class="seat-map__corner" aria-hidden="true" />
        <span
          v-for="column in columnNumbers"
          :key="`c${column}`"
          class="seat-map__number"
          aria-hidden="true"
          >{{ column }}</span
        >
        <template v-for="cells in grid" :key="`r${cells[0]?.row}`">
          <span class="seat-map__number seat-map__number--row" aria-hidden="true">{{
            cells[0]?.row
          }}</span>
          <component
            :is="canPick(cell) ? 'button' : 'span'"
            v-for="cell in cells"
            :key="`${cell.row}-${cell.column}`"
            :type="canPick(cell) ? 'button' : undefined"
            class="seat-cell"
            :class="{
              'seat-cell--taken': cell.seat !== undefined,
              'seat-cell--mine': isMine(cell),
              'seat-cell--selected': isSelected(cell),
              'seat-cell--free': cell.seat === undefined && !isSelected(cell),
            }"
            :style="look(cell) ? { '--seat-color': look(cell)?.color } : undefined"
            :aria-label="label(cell)"
            :aria-pressed="canPick(cell) ? isSelected(cell) : undefined"
            :role="canPick(cell) ? undefined : 'img'"
            @click="canPick(cell) && emit('select', cell.row, cell.column)"
            >{{ look(cell)?.emoji ?? '' }}</component
          >
        </template>
      </div>
    </div>
  </div>
</template>
