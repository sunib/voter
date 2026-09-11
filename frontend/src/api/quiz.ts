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
export const createQuizSubmission = (round: QuizSession, answers: QuizSubmission['spec']['answers']) =>
  request<{ name: string }>(`/${encodeURIComponent(round.metadata.name!)}`, {
    uid: round.metadata.uid, resourceVersion: round.metadata.resourceVersion, answers,
  })
export interface RoundResults {
  round: QuizSession
  total: number
  questions: { question: NonNullable<QuizSessionSpec['questions']>[number]; count: number; choices: Record<string, number>; sum: number; text: string[] }[]
}
export const getRoundResults = (name: string) => request<RoundResults>(`/${encodeURIComponent(name)}/results`)
