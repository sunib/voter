// Where you sit: a Seat you claim yourself, through /k8s, on the room's
// SeatingPlan.
//
// The rules are the API server's. The CRD (voter/config/crd/seats.yaml) holds
// the name, the emoji list and that a seat never moves; admission holds that it
// is yours and fits the plan (voter/config/admission/seat-policy.yaml,
// seat-in-plan-policy.yaml). This module builds a Seat that meets them, and
// checks the why first so the phone can say so before anything is sent.
//
// The why is the commit message: after a seat is written, a CommitRequest
// carries it, so the change is recorded in the participant's name and words.
// The request is labelled with its seat, and once it has committed Voter copies
// the why onto the Seat's status.lastCommit (voter/seat_reconciler.go). None of
// that is named on the page.

import { appConfig } from './appConfig'
import type { ApiError } from './http'
import {
  SEATINGPLANS,
  SEATS,
  createObject,
  getObject,
  listObjects,
  mergePatch,
  writeWithCommit,
} from './kube'

/** The CRD's enum, in its order. Chosen to read well small and on a dark
 *  projector; the API server refuses anything else. */
export const SEAT_EMOJIS = [
  '🦊', '🐙', '🦄', '🐝', '🐢', '🦉', '🐳', '🦖', '🐧', '🦀', '🐸', '🐼',
  '🚀', '🌵', '🍄', '🌈', '⚡', '🔥', '🍕', '🧇', '☕', '🎸', '🎲', '👾',
] as const

/** Twelve colours that hold up on a dark projector. The CRD takes any #rrggbb;
 *  the page offers only these. */
export const SEAT_COLORS = [
  { hex: '#e4572e', name: 'Vermilion' },
  { hex: '#f3a712', name: 'Amber' },
  { hex: '#ffd23f', name: 'Yellow' },
  { hex: '#a8e05f', name: 'Lime' },
  { hex: '#3bb273', name: 'Green' },
  { hex: '#2ec4b6', name: 'Teal' },
  { hex: '#4ea8de', name: 'Sky' },
  { hex: '#5465ff', name: 'Blue' },
  { hex: '#9b5de5', name: 'Violet' },
  { hex: '#f15bb5', name: 'Pink' },
  { hex: '#ff8fab', name: 'Rose' },
  { hex: '#e9ecef', name: 'White' },
] as const

const LABEL = 'voter.configbutler.ai'

/** On a seat's CommitRequest: the Seat it is for. Read back by Voter, which
 *  copies a committed why onto that Seat's status. Anyone may write any seat
 *  here; Voter believes it only when the request's author owns the seat. */
export const SEAT_COMMIT_LABEL = `${LABEL}/seat`

export interface SeatingPlan {
  apiVersion?: string
  kind?: string
  metadata: { name?: string; uid?: string; resourceVersion?: string }
  spec: { title?: string; rows: number; columns: number; aisles?: number[] }
}

export interface Seat {
  apiVersion?: string
  kind?: string
  metadata: {
    name?: string
    namespace?: string
    uid?: string
    resourceVersion?: string
    labels?: Record<string, string>
  }
  spec: {
    seatingPlanRef: { name: string; group?: string; kind?: string }
    row: number
    column: number
    claimedBy: ClaimedBy
    color: string
    emoji: string
  }
  /** Kept by Voter, never written from here (voter/seat_reconciler.go). */
  status?: {
    lastCommit?: {
      message?: string
      commit?: string
      committedAt?: string
      commitRequest?: string
    }
  }
}

/** Who claimed a seat. The username is the identity, as the API server makes
 *  it of the token -- demo:<id>, github:<email> -- and admission holds it to
 *  the requester's. The display name is what the map shows. */
export interface ClaimedBy {
  displayName: string
  username: string
}

/** A seat's name is its place on its plan, so the API server is the lock: a
 *  second claim of one seat is a 409. */
export function seatName(plan: string, row: number, column: number): string {
  return `${plan}-${row}-${column}`
}

/** Labels pad row and column to two digits, so they sort in a file listing. */
export function twoDigits(n: number): string {
  return String(n).padStart(2, '0')
}

/** The Seat for this place, person and look, meeting every rule admission
 *  holds. */
export function buildSeat(
  plan: string,
  row: number,
  column: number,
  claimedBy: ClaimedBy,
  color: string,
  emoji: string,
): Seat {
  return {
    apiVersion: 'examples.configbutler.ai/v1alpha1',
    kind: 'Seat',
    metadata: {
      name: seatName(plan, row, column),
      namespace: appConfig().namespace,
      labels: {
        [`${LABEL}/seatingplan`]: plan,
        [`${LABEL}/row`]: twoDigits(row),
        [`${LABEL}/column`]: twoDigits(column),
      },
    },
    spec: {
      seatingPlanRef: { name: plan },
      row,
      column,
      claimedBy,
      color,
      emoji,
    },
  }
}

export const WHY_MIN = 3
export const WHY_MAX = 80

/** What is sent: the why without surrounding space. */
export function whyText(why: string): string {
  return why.trim()
}

/** The why is required, 3 to 80 characters once trimmed. Returns the problem,
 *  or '' for none. Counted in characters, not UTF-16 units, so an emoji in the
 *  why counts once. */
export function checkWhy(why: string): string {
  const length = [...whyText(why)].length
  if (length === 0) return 'Tell us why. It goes with your choice.'
  if (length < WHY_MIN) return `A few more words, please: at least ${WHY_MIN} characters.`
  if (length > WHY_MAX) return `Keep it under ${WHY_MAX} characters.`
  return ''
}

/** One of a list, at random: a colour and emoji are pre-selected, so claiming
 *  stays quick. */
export function pickRandom<T>(list: readonly T[], random = Math.random): T {
  return list[Math.floor(random() * list.length) % list.length] as T
}

/** The active plan, or null if it does not exist (yet). */
export async function getSeatingPlan(): Promise<SeatingPlan | null> {
  try {
    return await getObject<SeatingPlan>(SEATINGPLANS, appConfig().seatingPlanName)
  } catch (e) {
    if ((e as ApiError).status === 404) return null
    throw e
  }
}

export async function listSeats(): Promise<Seat[]> {
  return await listObjects<Seat>(SEATS)
}

/** A value as a field selector reads it: only these three are special. */
function fieldSelectorValue(value: string): string {
  return value.replace(/[\\,=]/g, (c) => `\\${c}`)
}

/** This person's seat on the active plan, or null. The API server answers it
 *  itself: the CRD makes spec.claimedBy.username a selectable field, and
 *  admission holds that field to whoever claimed the seat, so the answer is
 *  one small, trustworthy list however full the room is. */
export async function findMySeat(username: string): Promise<Seat | null> {
  if (username === '') return null
  const mine = await listObjects<Seat>(SEATS, undefined, {
    fieldSelector: `spec.claimedBy.username=${fieldSelectorValue(username)}`,
  })
  const plan = appConfig().seatingPlanName
  return mine.find((seat) => seat.spec.seatingPlanRef.name === plan) ?? null
}

/** What a claim or change reports. `whyLost` is the one partial outcome: the
 *  seat was written, and its reason was not passed on. */
export interface SeatReceipt {
  seat: Seat
  whyLost: boolean
}

/** Writes the seat with its why passed on first (kube.ts, writeWithCommit). */
async function withWhy(
  prefix: string,
  seat: string,
  why: string,
  write: () => Promise<Seat>,
): Promise<SeatReceipt> {
  const { result, receipt } = await writeWithCommit(
    appConfig().seatGitTargetName,
    prefix,
    whyText(why),
    write,
    { [SEAT_COMMIT_LABEL]: seat },
  )
  return { seat: result, whyLost: receipt.commitRequested === false }
}

/** Passes on this person's why, then claims a seat as them. A refused claim
 *  throws; the why sent ahead of it then commits nothing. */
export async function claimSeat(
  row: number,
  column: number,
  claimedBy: ClaimedBy,
  color: string,
  emoji: string,
  why: string,
): Promise<SeatReceipt> {
  const plan = appConfig().seatingPlanName
  return withWhy('seat-claim-', seatName(plan, row, column), why, () =>
    createObject<Seat>(SEATS, buildSeat(plan, row, column, claimedBy, color, emoji)),
  )
}

/** Passes on the why, then changes the colour and emoji of a seat, and nothing
 *  else. */
export async function changeSeat(
  seat: Seat,
  color: string,
  emoji: string,
  why: string,
): Promise<SeatReceipt> {
  return withWhy('seat-change-', seat.metadata.name ?? '', why, () =>
    mergePatch<Seat>(SEATS, seat.metadata.name ?? '', { spec: { color, emoji } }),
  )
}

/** A failed claim or change in plain words. A refusal by admission is already
 *  the policy's own sentence (http.ts); the two that are not are named here. */
/** What a seat page says while the operator has taking or changing a seat
 *  switched off on /room, and what a refusal of either turns into. */
export const SEAT_CLAIM_CLOSED = "Taking a seat isn't possible at the moment."
export const SEAT_EDIT_CLOSED = "Editing your seat isn't possible at the moment."

export function seatErrorMessage(error: unknown): string {
  const e = error as Partial<ApiError> & { message?: string }
  if (e?.status === 409 && e.code === 'AlreadyExists') {
    return 'Someone just took that seat. Pick one close by.'
  }
  // The seat-in-plan binding with no plan to read: the API server's words for
  // it are about bindings and params.
  if (e?.message?.includes('no params found')) {
    return "The seating plan isn't set up yet. Try again in a moment."
  }
  // RBAC's refusal names a user, a verb and a resource; admission's is already
  // a sentence for a person, and passes through.
  // Seats are two switches on /room, so the verb says which one is off. Said
  // plainly: the room is not told who decides, yet.
  if (e?.status === 403 && /cannot create resource "?seats/.test(e.message ?? '')) {
    return SEAT_CLAIM_CLOSED
  }
  if (e?.status === 403 && /cannot patch resource "?seats/.test(e.message ?? '')) {
    return SEAT_EDIT_CLOSED
  }
  if (e?.status === 403 && /cannot \w+ resource/.test(e.message ?? '')) {
    return "You're not allowed to do that here."
  }
  return e?.message || 'Something went wrong. Try again.'
}
