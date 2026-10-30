<script setup lang="ts">
// The room as a cinema booking app draws it: the stage at the top, row 1
// closest to it, column 1 on the left as seen from the audience. The same
// orientation on every phone, so everyone reads it the same way.
//
// Names are not drawn: a seat is its colour and emoji. Whose it is shows in an
// accessible label, never on the map itself.
//
// A hall wider than the phone scrolls inside the map, and opens on its middle:
// both blocks show a little, so it is plain there is more on either side. Row
// numbers stand on both edges, so the far block still says which row it is.
import { computed, nextTick, onMounted, ref, watch } from 'vue'

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
  }>(),
  {
    mine: undefined,
    selected: null,
    preview: undefined,
    selectable: false,
  },
)

const emit = defineEmits<{ select: [row: number, column: number] }>()

const grid = computed(() => seatGrid(props.plan.spec, props.seats))

/** The header: each column's number, and whether an aisle follows it. */
const header = computed(() => grid.value[0] ?? [])

/** One track per seat, a narrower one per aisle, a row number either side. */
const tracks = computed(() =>
  [
    'var(--seat-number)',
    ...header.value.flatMap((cell) =>
      cell.aisleAfter
        ? ['var(--seat-size)', 'var(--seat-aisle)']
        : ['var(--seat-size)'],
    ),
    'var(--seat-number)',
  ].join(' '),
)

const scroller = ref<HTMLElement>()

async function centre() {
  await nextTick()
  const el = scroller.value
  if (el) el.scrollLeft = (el.scrollWidth - el.clientWidth) / 2
}
onMounted(centre)
watch(
  () => [props.plan.spec.columns, props.plan.spec.aisles?.join(',')],
  centre,
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
  if (cell.seat)
    return { color: cell.seat.spec.color, emoji: cell.seat.spec.emoji }
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
  <div class="seat-map">
    <div ref="scroller" class="seat-map__scroll">
      <div class="seat-map__grid" :style="{ gridTemplateColumns: tracks }">
        <span class="seat-map__corner" aria-hidden="true" />
        <div class="seat-map__stage" aria-hidden="true">Stage</div>
        <span class="seat-map__corner" aria-hidden="true" />
        <span class="seat-map__corner" aria-hidden="true" />
        <template v-for="cell in header" :key="`c${cell.column}`">
          <span class="seat-map__number" aria-hidden="true">{{
            cell.column
          }}</span>
          <span v-if="cell.aisleAfter" aria-hidden="true" />
        </template>
        <span class="seat-map__corner" aria-hidden="true" />
        <template v-for="cells in grid" :key="`r${cells[0]?.row}`">
          <span
            class="seat-map__number seat-map__number--row"
            aria-hidden="true"
            >{{ cells[0]?.row }}</span
          >
          <template v-for="cell in cells" :key="`${cell.row}-${cell.column}`">
            <component
              :is="canPick(cell) ? 'button' : 'span'"
              :type="canPick(cell) ? 'button' : undefined"
              class="seat-cell"
              :class="{
                'seat-cell--taken': cell.seat !== undefined,
                'seat-cell--mine': isMine(cell),
                'seat-cell--selected': isSelected(cell),
                'seat-cell--free': cell.seat === undefined && !isSelected(cell),
              }"
              :style="
                look(cell) ? { '--seat-color': look(cell)?.color } : undefined
              "
              :aria-label="label(cell)"
              :aria-pressed="canPick(cell) ? isSelected(cell) : undefined"
              :role="canPick(cell) ? undefined : 'img'"
              @click="canPick(cell) && emit('select', cell.row, cell.column)"
              >{{ look(cell)?.emoji ?? '' }}</component
            >
            <span
              v-if="cell.aisleAfter"
              class="seat-map__aisle"
              aria-hidden="true"
            />
          </template>
          <span
            class="seat-map__number seat-map__number--row"
            aria-hidden="true"
            >{{ cells[0]?.row }}</span
          >
        </template>
      </div>
    </div>
  </div>
</template>
