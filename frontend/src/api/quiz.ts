import type { QuizSession, QuizSessionSpec, QuizSubmission } from './types'
import { currentCsrfToken } from './session'

async function request<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/public/rounds${path}`, {
    method: body === undefined ? 'GET' : 'POST',
    credentials: 'include',
    headers: {
      'content-type': 'application/json',
      'x-csrf-token': currentCsrfToken(),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await res.json()
  if (!res.ok)
    throw Object.assign(
      new Error(data.error ?? data.message ?? `Request failed (${res.status})`),
      { status: res.status, code: data.code },
    )
  return data as T
}
export const listRounds = () => request<{ items: QuizSession[] }>('')
export interface RoundView {
  round: QuizSession
  /** This participant already has a QuizSubmission for this round UID. */
  voted: boolean
}
export const getQuizSession = (name: string) =>
  request<RoundView>(`/${encodeURIComponent(name)}`)
/** The backend sets DisallowUnknownFields, so this body and the handler's struct
 *  have to change together: sending a `resourceVersion` it no longer reads is a
 *  flat 400.
 *
 *  `generation` and not `resourceVersion`, because the round's status carries the
 *  live tally and a controller rewrites it on every ballot -- which moves
 *  resourceVersion and not generation. Pinning the wrong one cost a large part of
 *  the room its vote on 2026-09-17; docs/post-demo-2026-09-17.md. `uid` comes
 *  along because generation restarts at 1 on a round recreated under the same
 *  name. */
export const createQuizSubmission = (
  round: QuizSession,
  answers: QuizSubmission['spec']['answers'],
) =>
  request<{ name: string }>(`/${encodeURIComponent(round.metadata.name!)}`, {
    uid: round.metadata.uid,
    generation: round.metadata.generation,
    answers,
  })
export interface RoundResults {
  round: QuizSession
  /** Ballots that passed validation -- the same number as status.counted. */
  total: number
  /** Ballots carrying the round's name, counted or not. */
  filed?: number
  questions: {
    question: NonNullable<QuizSessionSpec['questions']>[number]
    count: number
    choices: Record<string, number>
    sum: number
    text: string[]
  }[]
}

type Question = NonNullable<QuizSessionSpec['questions']>[number]

/** One shape for the results screen, whichever of the two paths fed it.
 *
 *  The round's own status is the live one and arrives on the `quizsessions`
 *  stream the screen opens anyway; the REST endpoint is the first paint and the
 *  fallback. Both mean the same thing, so the screen should not have to know
 *  which it got -- except for `asOf`, which it must show, because a controller
 *  that has stopped looks exactly like a room that has stopped voting. */
export interface ShownResult {
  question: Question
  count: number
  choices: Record<string, number>
  sum: number
  text: string[]
  /** How many free-text answers were written. Larger than `text.length` when
   *  the tally came from status, which keeps a bounded sample. */
  textTotal: number
}
export interface ShownResults {
  total: number
  filed: number
  questions: ShownResult[]
  /** When this tally was computed, or undefined for a REST read -- which is
   *  computed on demand and is therefore always now. */
  asOf?: string
}

/** The tally the controller wrote, or undefined if it has not written one.
 *
 *  Undefined is not an error: a round created a moment ago, or one served by a
 *  deployment whose controller is not running, simply has no status, and the
 *  caller falls back to REST rather than rendering zeroes over real votes. */
export function resultsFromStatus(
  round: QuizSession | undefined,
): ShownResults | undefined {
  const status = round?.status
  if (!status?.lastTallyTime || !status.questions) return undefined
  const counted = status.counted ?? 0
  const byId = new Map(status.questions.map((q) => [q.id, q]))
  return {
    total: counted,
    filed: status.filed ?? counted,
    asOf: status.lastTallyTime,
    // Driven by the round's OWN questions, not by the status list: a tally
    // computed before a question was added should leave that question on screen
    // showing no answers, rather than dropping it off the projector.
    questions: (round?.spec.questions ?? []).map((question) => {
      const tallied = byId.get(question.id)
      const text = tallied?.text ?? []
      return {
        question,
        count: tallied?.count ?? 0,
        choices: tallied?.choices ?? {},
        sum: tallied?.sum ?? 0,
        text,
        textTotal: tallied?.textTotal ?? text.length,
      }
    }),
  }
}

export function resultsFromRest(
  results: RoundResults | undefined,
): ShownResults | undefined {
  if (!results) return undefined
  return {
    total: results.total,
    filed: results.filed ?? results.total,
    questions: results.questions.map((r) => ({
      ...r,
      textTotal: r.text.length,
    })),
  }
}
/** Open or close a round. There is no client-side permission check on purpose:
 *  the backend patches with the caller's own token, so a participant gets the
 *  API server's own 403 and the page shows it. */
export const setRoundState = (name: string, state: 'live' | 'closed') =>
  request<Record<string, unknown>>(`/${encodeURIComponent(name)}/state`, {
    state,
  })

export const getRoundResults = (name: string) =>
  request<RoundResults>(`/${encodeURIComponent(name)}/results`)
