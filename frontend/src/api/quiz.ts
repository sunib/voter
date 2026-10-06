import { deepEqual } from '@configbutler/krm-stream'

import type { QuizSession, QuizSessionSpec } from './types'
import { ballotName, buildBallot, validateAnswers, type Answer } from './ballot'
import { createApiError, type ApiError } from './http'
import {
  QUIZSESSIONS,
  QUIZSUBMISSIONS,
  createObject,
  getObject,
  listObjects,
  mergePatch,
} from './kube'
import { currentSession } from './session'

/** Every round in the namespace, read through /k8s as this person. */
export const listRounds = async () => ({
  items: await listObjects<QuizSession>(QUIZSESSIONS),
})

export interface RoundView {
  round: QuizSession
  /** This participant already has a ballot for this round. */
  voted: boolean
}

/** The round, and whether this participant has voted in it: a GET of the one
 *  name their ballot would have. Only an early warning -- the create is what
 *  decides, with a 409 -- so a failed lookup reads as "not yet". */
export async function getQuizSession(name: string): Promise<RoundView> {
  const round = await getObject<QuizSession>(QUIZSESSIONS, name)
  const displayName = currentSession()?.displayName ?? ''
  let voted = false
  if (displayName !== '') {
    voted = await getObject(
      QUIZSUBMISSIONS,
      ballotName(name, displayName),
    ).then(
      () => true,
      () => false,
    )
  }
  return { round, voted }
}

/** Casts this participant's ballot: a QuizSubmission they create themselves,
 *  through /k8s, so the API server's audit event -- and the Git commit that
 *  follows it -- names them.
 *
 *  The answers are checked here first, for an immediate "answer required".
 *  Everything else is admission's to refuse (a closed round, changed questions,
 *  someone else's name), with its own message. A second ballot is the API
 *  server's 409 AlreadyExists, reported as AlreadyVoted. */
/** How long a ballot waits for a brand-new round's digest, and how often it
 *  looks. Measured on the fixture: one to several seconds after creation. */
const DIGEST_WAIT_MS = 8000
const DIGEST_POLL_MS = 500

/** The round on screen, carrying its questions digest. A round read the
 *  moment it was created has none yet: Voter's reconciler publishes it with the
 *  first tally, seconds later. So it is read again until it has one, and that
 *  digest used when it is still the same round asking the same questions -- it
 *  then names exactly the questions being answered. Anything else keeps the copy
 *  on screen, and admission refuses the ballot as "the round changed", which is
 *  the truth. */
async function withDigest(
  round: QuizSession,
  sleep = (ms: number) => new Promise((r) => setTimeout(r, ms)),
): Promise<QuizSession> {
  if (round.status?.questionsDigest) return round
  let fresh = await getObject<QuizSession>(
    QUIZSESSIONS,
    round.metadata.name ?? '',
  )
  for (
    let waited = 0;
    !fresh.status?.questionsDigest && waited < DIGEST_WAIT_MS;
    waited += DIGEST_POLL_MS
  ) {
    await sleep(DIGEST_POLL_MS)
    fresh = await getObject<QuizSession>(
      QUIZSESSIONS,
      round.metadata.name ?? '',
    )
  }
  const digest = fresh.status?.questionsDigest
  if (!digest) {
    throw createApiError(409, {
      error: 'This round is still being prepared. Try again in a moment.',
    })
  }
  if (
    fresh.metadata.uid !== round.metadata.uid ||
    !deepEqual(fresh.spec.questions ?? [], round.spec.questions ?? [])
  ) {
    return round
  }
  return { ...round, status: { ...round.status, questionsDigest: digest } }
}

export async function createQuizSubmission(
  round: QuizSession,
  answers: Answer[],
): Promise<{ name: string }> {
  const problem = validateAnswers(round.spec.questions ?? [], answers)
  if (problem !== '') throw createApiError(400, { error: problem })
  const ballot = buildBallot(
    await withDigest(round),
    currentSession()?.displayName ?? '',
    answers,
  )
  try {
    await createObject(QUIZSUBMISSIONS, ballot)
  } catch (cause) {
    const error = cause as ApiError
    if (error.status === 409 && error.code === 'AlreadyExists') {
      error.code = 'AlreadyVoted'
      error.message = 'You have already voted in this round.'
    }
    throw error
  }
  return { name: ballot.metadata.name ?? '' }
}

type Question = NonNullable<QuizSessionSpec['questions']>[number]

/** The results screen's shape, from the tally Voter's reconciler writes into
 *  the round's status. It arrives on the `quizsessions` stream the screen opens
 *  anyway. `asOf` must be shown, because a controller that has stopped looks
 *  exactly like a room that has stopped voting. */
export interface ShownResult {
  question: Question
  count: number
  choices: Record<string, number>
  sum: number
  text: string[]
  /** How many free-text answers were written. Larger than `text.length`,
   *  because status keeps a bounded sample. */
  textTotal: number
}
export interface ShownResults {
  total: number
  filed: number
  questions: ShownResult[]
  /** When this tally was computed. */
  asOf?: string
}

/** The tally the controller wrote, or undefined if it has not written one.
 *
 *  Undefined is not an error: a round created a moment ago, or one served by a
 *  deployment whose controller is not running, simply has no status, and the
 *  screen says it is waiting rather than rendering zeroes over real votes. */
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

/** Open or close a round: a merge patch of spec.state through /k8s. There is no
 *  client-side permission check on purpose: the patch carries the caller's own
 *  token, so a participant gets the API server's own 403 and the page shows it. */
export const setRoundState = (name: string, state: 'live' | 'closed') =>
  mergePatch<QuizSession>(QUIZSESSIONS, name, { spec: { state } })
