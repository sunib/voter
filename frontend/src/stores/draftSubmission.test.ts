import { beforeEach, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useDraftSubmissionStore } from './draftSubmission'
beforeEach(() => setActivePinia(createPinia()))
it('serializes answers using question types, preserves zero and omits stale fields', () => {
  const draft = useDraftSubmissionStore()
  draft.answers = { text: 'hello', choice: 'A', score: 0, multi: ['A'], obsolete: 'old', empty: '' }
  expect(draft.toAnswerList([
    { id: 'text', title: 'Text', type: 'freeText' },
    { id: 'choice', title: 'Choice', type: 'singleChoice' },
    { id: 'score', title: 'Score', type: 'scale0to10' },
    { id: 'multi', title: 'Multi', type: 'multiChoice' },
    { id: 'empty', title: 'Empty', type: 'freeText' },
  ])).toEqual([
    { questionId: 'text', freeText: 'hello' },
    { questionId: 'choice', singleChoice: 'A' },
    { questionId: 'score', number: 0 },
    { questionId: 'multi', multiChoice: ['A'] },
  ])
})
