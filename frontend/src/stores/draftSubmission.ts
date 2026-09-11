import { defineStore } from 'pinia'
import type { QuizSessionSpec } from '../api/types'

type DraftState = {
  sessionName?: string
  answers: Record<string, unknown>
}

export type DraftAnswer =
  | { questionId: string; singleChoice: string }
  | { questionId: string; multiChoice: string[] }
  | { questionId: string; number: number }
  | { questionId: string; freeText: string }

function storageKey(sessionName: string) {
  return `voter:draft:${sessionName}`
}

export const useDraftSubmissionStore = defineStore('draftSubmission', {
  state: (): DraftState => ({
    sessionName: undefined,
    answers: {},
  }),
  actions: {
    load(sessionName: string) {
      this.sessionName = sessionName
      try {
        const raw = localStorage.getItem(storageKey(sessionName))
        if (!raw) {
          this.answers = {}
          return
        }
        const parsed = JSON.parse(raw) as unknown
        if (parsed && typeof parsed === 'object') this.answers = parsed as any
        else this.answers = {}
      } catch {
        this.answers = {}
      }
    },
    setAnswer(questionId: string, value: unknown) {
      this.answers[questionId] = value
      if (!this.sessionName) return
      try { localStorage.setItem(storageKey(this.sessionName), JSON.stringify(this.answers)) } catch { /* In-memory drafts still work when storage is unavailable. */ }
    },
    toAnswerList(questions: NonNullable<QuizSessionSpec['questions']>): DraftAnswer[] {
      return questions.flatMap((q): DraftAnswer[] => {
        const value = this.answers[q.id]
        if (value === undefined || value === null || value === '') return []
        const questionId = q.id
        switch (q.type) {
          case 'singleChoice': return [{ questionId, singleChoice: value as string }]
          case 'multiChoice': return [{ questionId, multiChoice: value as string[] }]
          case 'number': case 'scale0to10': return [{ questionId, number: value as number }]
          case 'freeText': return [{ questionId, freeText: value as string }]
        }
      })
    },
    clear() {
      try { if (this.sessionName) localStorage.removeItem(storageKey(this.sessionName)) } catch { /* Storage may be unavailable. */ }
      this.answers = {}
    },
  },
})
