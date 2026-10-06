import { describe, expect, it } from 'vitest'

import { ballotName, buildBallot, validateAnswers, type Answer } from './ballot'
import { admissionReason } from './http'
import type { QuizSession } from './types'

// The cases of TestQuizAnswerValidation in voter/participant_quiz_test.go: the
// tally runs the Go copy, and the phone must agree with it.
describe('validateAnswers', () => {
  const questions: NonNullable<QuizSession['spec']['questions']> = [
    {
      id: 'multi',
      title: 'Multi',
      type: 'multiChoice',
      choices: ['a', 'b'],
      required: true,
    },
    { id: 'score', title: 'Score', type: 'scale0to10' },
    { id: 'n', title: 'Number', type: 'number', min: -2, max: 2 },
    { id: 'text', title: 'Text', type: 'freeText', required: true },
  ]
  const valid = (): Answer[] => [
    { questionId: 'multi', multiChoice: ['a', 'b'] },
    { questionId: 'score', number: 0 },
    { questionId: 'n', number: -2 },
    { questionId: 'text', freeText: 'ok' },
  ]
  const change = (i: number, answer: Answer) =>
    valid().map((a, j) => (j === i ? answer : a))

  it.each([
    ['all types', valid(), ''],
    [
      'empty required',
      change(0, { questionId: 'multi', multiChoice: [] }),
      'invalid answer for Multi',
    ],
    [
      'duplicate choice',
      change(0, { questionId: 'multi', multiChoice: ['a', 'a'] }),
      'invalid answer for Multi',
    ],
    [
      'range',
      change(1, { questionId: 'score', number: 11 }),
      'invalid answer for Score',
    ],
    [
      'numeric min',
      change(2, { questionId: 'n', number: -3 }),
      'invalid answer for Number',
    ],
    [
      'blank required',
      change(3, { questionId: 'text', freeText: '  ' }),
      'invalid answer for Text',
    ],
    [
      'two fields',
      change(3, { questionId: 'text', freeText: 'ok', number: 1 }),
      'invalid answer for Text',
    ],
    [
      'duplicate question',
      change(2, { questionId: 'score', number: 1 }),
      'unknown or duplicate question: score',
    ],
    ['missing required', valid().slice(0, 3), 'answer required: Text'],
  ])('%s', (_name, answers, problem) => {
    expect(validateAnswers(questions, answers)).toBe(problem)
  })
})

describe('the ballot admission expects', () => {
  const round: QuizSession = {
    apiVersion: 'examples.configbutler.ai/v1alpha1',
    kind: 'QuizSession',
    metadata: { name: 'demo1', namespace: 'voter', uid: 'round-uid' },
    spec: { state: 'live', questions: [] },
    status: { questionsDigest: 'sha256:abc' },
  }

  // voter-ballot compares the name with <round>-<display name>.lowerAscii().
  it('is named after the round and the lower-cased display name', () => {
    expect(ballotName('demo1', 'Ada-Lovelace')).toBe('demo1-ada-lovelace')
  })

  it('labels its round and submitter, and pins the round twice', () => {
    const ballot = buildBallot(
      round,
      'Ada-Lovelace',
      [],
      new Date('2026-10-06T12:00:00.123Z'),
    )
    expect(ballot.metadata).toEqual({
      name: 'demo1-ada-lovelace',
      namespace: 'voter',
      labels: {
        'voter.configbutler.ai/round': 'demo1',
        'voter.configbutler.ai/submitter': 'Ada-Lovelace',
      },
    })
    expect(ballot.spec.roundUID).toBe('round-uid')
    expect(ballot.spec.questionsDigest).toBe('sha256:abc')
    expect(ballot.spec.submittedAt).toBe('2026-10-06T12:00:00Z')
  })
})

describe('admissionReason', () => {
  it("keeps only the policy's own sentence", () => {
    expect(
      admissionReason(
        "quizsubmissions.examples.configbutler.ai \"demo1-ada\" is forbidden: ValidatingAdmissionPolicy 'voter-ballot' with binding 'voter-ballot' denied request: This round is not open for voting.",
      ),
    ).toBe('This round is not open for voting.')
  })

  it('leaves every other message alone', () => {
    expect(
      admissionReason('coffeeconfigs is forbidden: User "demo:x" cannot patch'),
    ).toBe('coffeeconfigs is forbidden: User "demo:x" cannot patch')
  })
})
