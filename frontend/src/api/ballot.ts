// A ballot, as the participant casts it: a QuizSubmission they create
// themselves through /k8s.
//
// The rules a ballot must meet are admission's (voter/config/admission/
// quizsubmission-policy.yaml): who may cast one, what it is called, which
// round and questions it pins, whether the round is live. This module builds a
// ballot that meets them, and checks the answers first so the phone can say
// "answer required" before anything is sent. The tally checks the answers
// again (validateQuizAnswers in voter/quiz_rules.go), so this copy is
// the courtesy, never the control.

import type { QuizSession, QuizSessionSpec, QuizSubmission } from './types'

type Question = NonNullable<QuizSessionSpec['questions']>[number]
/** An answer as it is checked: any of the four fields, so a ballot with two
 *  of them is caught rather than unrepresentable. */
export type Answer = {
  questionId: string
  singleChoice?: string | null
  multiChoice?: string[] | null
  number?: number | null
  freeText?: string | null
}

/** The same checks, in the same order and words, as validateQuizAnswers in
 *  voter/quiz_rules.go. Returns the first problem, or '' for none. */
export function validateAnswers(
  questions: Question[],
  answers: Answer[],
): string {
  const byId = new Map(questions.map((q) => [q.id, q]))
  const seen = new Set<string>()
  for (const answer of answers) {
    const q = byId.get(answer.questionId)
    if (q === undefined || seen.has(answer.questionId)) {
      return `unknown or duplicate question: ${answer.questionId}`
    }
    seen.add(answer.questionId)
    const fields = [
      answer.singleChoice,
      answer.multiChoice,
      answer.number,
      answer.freeText,
    ].filter((field) => field !== undefined && field !== null).length
    let valid = fields === 1
    switch (q.type) {
      case 'singleChoice':
        valid =
          valid &&
          answer.singleChoice != null &&
          (q.choices ?? []).includes(answer.singleChoice)
        break
      case 'multiChoice': {
        const chosen = answer.multiChoice
        valid =
          valid &&
          chosen != null &&
          new Set(chosen).size === chosen.length &&
          chosen.every((c) => (q.choices ?? []).includes(c)) &&
          chosen.length <= 20 &&
          (!q.required || chosen.length > 0)
        break
      }
      case 'number':
      case 'scale0to10': {
        const n = answer.number
        valid =
          valid &&
          n != null &&
          (q.min === undefined || n >= q.min) &&
          (q.max === undefined || n <= q.max) &&
          (q.type !== 'scale0to10' || (n >= 0 && n <= 10))
        break
      }
      case 'freeText': {
        const text = answer.freeText
        valid =
          valid &&
          text != null &&
          [...text].length <= 2000 &&
          (!q.required || text.trim() !== '')
        break
      }
      default:
        valid = false
    }
    if (!valid) return `invalid answer for ${q.title}`
  }
  for (const q of questions) {
    if (q.required && !seen.has(q.id)) return `answer required: ${q.title}`
  }
  return ''
}

/** ASCII-only lower case, as CEL's lowerAscii(), which admission compares
 *  against. Room Pass folds display names to letters, digits and dashes, so
 *  this and toLowerCase() agree on every name it issues; using the narrower
 *  one keeps the two sides identical by construction. */
function lowerAscii(s: string): string {
  return s.replace(/[A-Z]/g, (c) => c.toLowerCase())
}

/** One ballot per participant and round, named `<round>-<display name>`. Name
 *  uniqueness in the API server, not bookkeeping here, is what keeps a vote
 *  single-use: a second create is a 409. */
export function ballotName(roundName: string, displayName: string): string {
  return `${roundName}-${lowerAscii(displayName)}`
}

/** The QuizSubmission for these answers, meeting every rule admission holds.
 *
 *  It pins the round twice: its UID, so a round deleted and recreated under the
 *  same name does not take it, and the digest of its questions as the round's
 *  status publishes it, so a ballot for since-edited questions is refused. Not
 *  metadata.generation: state is in the spec, so opening and closing move it
 *  (docs/krm-foyer-migration.md, decision 1). */
export function buildBallot(
  round: QuizSession,
  displayName: string,
  answers: Answer[],
  now = new Date(),
): QuizSubmission {
  const roundName = round.metadata.name ?? ''
  return {
    apiVersion: 'examples.configbutler.ai/v1alpha1',
    kind: 'QuizSubmission',
    metadata: {
      name: ballotName(roundName, displayName),
      namespace: round.metadata.namespace,
      labels: {
        'voter.configbutler.ai/round': roundName,
        'voter.configbutler.ai/submitter': displayName,
      },
    },
    spec: {
      sessionRef: {
        group: 'examples.configbutler.ai',
        kind: 'QuizSession',
        name: roundName,
      },
      roundUID: round.metadata.uid,
      questionsDigest: round.status?.questionsDigest,
      submittedAt: now.toISOString().replace(/\.\d{3}Z$/, 'Z'),
      answers: answers as QuizSubmission['spec']['answers'],
    },
  }
}
