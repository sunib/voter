import { defineStore } from 'pinia'

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
      localStorage.setItem(storageKey(this.sessionName), JSON.stringify(this.answers))
    },
    toAnswerList(): DraftAnswer[] {
      return Object.entries(this.answers)
        .filter(([, value]) => value !== undefined && value !== null)
        .map(([questionId, value]) => {
          if (Array.isArray(value)) {
            return { questionId, multiChoice: value as string[] }
          }
          switch (typeof value) {
            case 'string':
              return { questionId, singleChoice: value }
            case 'number':
              return { questionId, number: value }
            default:
              return { questionId, freeText: String(value) }
          }
        })
    },
    clear() {
      if (this.sessionName) localStorage.removeItem(storageKey(this.sessionName))
      this.answers = {}
    },
  },
})
