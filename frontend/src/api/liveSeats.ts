// The room as the map draws it: the active SeatingPlan and every Seat on it,
// live.
//
// Both arrive twice, for the same reason the round list does (HomeScreen): the
// REST read is the first paint and the fallback for a stream that cannot be
// served; the watch is what makes a seat claimed on another phone, or a plan
// resized in Git, reach this one without a reload.
import { computed, onMounted, ref } from 'vue'

import { appConfig } from './appConfig'
import { SEATINGPLANS, SEATS } from './kube'
import { useLiveResources } from './liveResources'
import {
  getSeatingPlan,
  listSeats,
  type Seat,
  type SeatingPlan,
} from './seats'

/** One place on the map, and who has it, if anyone. */
export interface SeatCell {
  row: number
  column: number
  seat?: Seat
  /** A walkway runs to its right: the plan's aisles name this column. */
  aisleAfter: boolean
}

/** The plan as rows of cells, row 1 first, column 1 first. A seat outside the
 *  plan -- claimed before it shrank -- is not drawn, and neither is an aisle
 *  past the last column. */
export function seatGrid(
  plan: Pick<SeatingPlan['spec'], 'rows' | 'columns' | 'aisles'>,
  seats: Seat[],
): SeatCell[][] {
  const taken = new Map(seats.map((s) => [`${s.spec.row}-${s.spec.column}`, s]))
  const aisles = new Set(plan.aisles ?? [])
  return Array.from({ length: plan.rows }, (_, r) =>
    Array.from({ length: plan.columns }, (_, c) => ({
      row: r + 1,
      column: c + 1,
      seat: taken.get(`${r + 1}-${c + 1}`),
      aisleAfter: c + 1 < plan.columns && aisles.has(c + 1),
    })),
  )
}

export function useSeatMap() {
  const config = appConfig()
  const livePlan = useLiveResources({
    ...SEATINGPLANS,
    namespace: config.namespace,
    name: config.seatingPlanName,
  })
  const liveSeats = useLiveResources({ ...SEATS, namespace: config.namespace })

  // undefined while nothing has answered: the map shows nothing rather than a
  // guessed grid. null is an answer: there is no plan.
  const readPlan = ref<SeatingPlan | null | undefined>(undefined)
  const readSeats = ref<Seat[]>([])
  const error = ref('')

  async function refresh() {
    try {
      const [plan, seats] = await Promise.all([getSeatingPlan(), listSeats()])
      readPlan.value = plan
      readSeats.value = seats
      error.value = ''
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Could not load the room.'
    }
  }
  onMounted(refresh)

  // Once synced, an empty stream is the truth: the plan was deleted.
  const plan = computed<SeatingPlan | null | undefined>(() =>
    livePlan.synced()
      ? ((livePlan.items.value[0] as unknown as SeatingPlan | undefined) ?? null)
      : readPlan.value,
  )
  const seats = computed<Seat[]>(() => {
    const all = liveSeats.synced()
      ? (liveSeats.items.value as unknown as Seat[])
      : readSeats.value
    return all.filter(
      (s) => s.spec.seatingPlanRef.name === config.seatingPlanName,
    )
  })

  return { plan, seats, error, refresh }
}
