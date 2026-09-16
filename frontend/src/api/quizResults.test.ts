import { describe, expect, it } from 'vitest'
import { resultsFromRest, resultsFromStatus, type RoundResults } from './quiz'
import type { QuizSession, QuizSessionSpec } from './types'

// The results screen renders whichever of these two returns something, so the
// shapes have to agree on everything except `asOf` -- and disagree loudly on
// that, because a REST read is always now and a status may be minutes old.

type Question = NonNullable<QuizSessionSpec['questions']>[number]
const choiceQuestion: Question = {
  id: 'choice',
  type: 'singleChoice',
  title: 'Choose',
  choices: ['A', 'B'],
}
const textQuestion: Question = {
  id: 'text',
  type: 'freeText',
  title: 'Explain',
}
const questions = [choiceQuestion, textQuestion]

const round = (status?: QuizSession['status']): QuizSession => ({
  apiVersion: 'examples.configbutler.ai/v1alpha1',
  kind: 'QuizSession',
  metadata: { name: 'demo', namespace: 'voter', uid: 'round' },
  spec: { title: 'Demo', state: 'live', questions },
  status,
})

describe('resultsFromStatus', () => {
  it('is undefined until the controller has written a tally', () => {
    expect(resultsFromStatus(round())).toBeUndefined()
    // Counts with no timestamp is not a tally: without lastTallyTime the screen
    // cannot say how old the number is, which is the one thing it must say.
    expect(
      resultsFromStatus(round({ counted: 7, questions: [] })),
    ).toBeUndefined()
    expect(resultsFromStatus(undefined)).toBeUndefined()
  })

  it("reads the round's own status, including the ballots that did not count", () => {
    const shown = resultsFromStatus(
      round({
        observedGeneration: 3,
        lastTallyTime: '2026-09-17T13:22:41Z',
        filed: 4,
        counted: 3,
        questions: [
          {
            id: 'choice',
            count: 3,
            choices: { A: 2, B: 1 },
            sum: 0,
            textTotal: 0,
            text: [],
          },
          {
            id: 'text',
            count: 2,
            choices: {},
            sum: 0,
            textTotal: 30,
            text: ['newest'],
          },
        ],
      }),
    )!
    expect(shown.total).toBe(3)
    expect(shown.filed).toBe(4)
    expect(shown.asOf).toBe('2026-09-17T13:22:41Z')
    const [choice, text] = shown.questions
    expect(choice!.choices).toEqual({ A: 2, B: 1 })
    // Status carries a bounded sample against an exact total, so the screen can
    // say "the most recent 1 of 30" rather than quietly showing one answer.
    expect(text!.text).toEqual(['newest'])
    expect(text!.textTotal).toBe(30)
  })

  it('keeps a question the tally has not caught up with', () => {
    // An older tally, missing the question that was added after it. Dropping
    // the question off the projector would be worse than showing it empty.
    const shown = resultsFromStatus(
      round({
        lastTallyTime: '2026-09-17T13:22:41Z',
        counted: 1,
        questions: [
          {
            id: 'choice',
            count: 1,
            choices: { A: 1 },
            sum: 0,
            textTotal: 0,
            text: [],
          },
        ],
      }),
    )!
    expect(shown.questions.map((q) => q.question.id)).toEqual([
      'choice',
      'text',
    ])
    expect(shown.questions[1]!.count).toBe(0)
  })
})

describe('resultsFromRest', () => {
  const rest: RoundResults = {
    round: round(),
    total: 3,
    filed: 4,
    questions: [
      {
        question: choiceQuestion,
        count: 3,
        choices: { A: 2, B: 1 },
        sum: 0,
        text: [],
      },
      {
        question: textQuestion,
        count: 2,
        choices: {},
        sum: 0,
        text: ['one', 'two'],
      },
    ],
  }

  it('carries no timestamp, because it is computed on demand', () => {
    expect(resultsFromRest(rest)!.asOf).toBeUndefined()
    expect(resultsFromRest(undefined)).toBeUndefined()
  })

  it('has the whole free-text list, so textTotal is its length', () => {
    const text = resultsFromRest(rest)!.questions[1]!
    expect(text.text).toEqual(['one', 'two'])
    expect(text.textTotal).toBe(2)
  })

  it('falls back to total when an older backend sends no filed', () => {
    expect(resultsFromRest({ ...rest, filed: undefined })!.filed).toBe(3)
  })
})
