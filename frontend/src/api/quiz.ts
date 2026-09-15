import type { QuizSession, QuizSessionSpec, QuizSubmission } from './types'
import { currentCsrfToken } from './session'

async function request<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/public/rounds${path}`, {
    method: body === undefined ? 'GET' : 'POST',
    credentials: 'include',
    headers: { 'content-type': 'application/json', 'x-csrf-token': currentCsrfToken() },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await res.json()
  if (!res.ok) throw Object.assign(new Error(data.error ?? data.message ?? `Request failed (${res.status})`), { status: res.status, code: data.code })
  return data as T
}
export const listRounds = () => request<{ items: QuizSession[] }>('')
export interface RoundView {
  round: QuizSession
  /** This participant already has a QuizSubmission for this round UID. */
  voted: boolean
}
export const getQuizSession = (name: string) => request<RoundView>(`/${encodeURIComponent(name)}`)
/** The backend sets DisallowUnknownFields, so this body and the handler's struct
 *  have to change together: sending a `uid` it no longer reads is a flat 400. */
export const createQuizSubmission = (round: QuizSession, answers: QuizSubmission['spec']['answers']) =>
  request<{ name: string }>(`/${encodeURIComponent(round.metadata.name!)}`, {
    resourceVersion: round.metadata.resourceVersion, answers,
  })
export interface RoundResults {
  round: QuizSession
  total: number
  questions: { question: NonNullable<QuizSessionSpec['questions']>[number]; count: number; choices: Record<string, number>; sum: number; text: string[] }[]
}
/** Open or close a round. There is no client-side permission check on purpose:
 *  the backend patches with the caller's own token, so a participant gets the
 *  API server's own 403 and the page shows it. */
export const setRoundState = (name: string, state: 'live' | 'closed') =>
  request<Record<string, unknown>>(`/${encodeURIComponent(name)}/state`, { state })

export const getRoundResults = (name: string) => request<RoundResults>(`/${encodeURIComponent(name)}/results`)
