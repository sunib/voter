import { defineStore } from 'pinia'
import type { QuizSession, SessionInfo } from '../api/types'
import { getQuizSession } from '../api/kube'

type SessionState = {
  byName: Record<
    string,
    {
      status: 'idle' | 'loading' | 'ready' | 'error'
      data?: QuizSession
      error?: string
      loadedAt?: number
    }
  >
  currentInfo?: {
    status: 'idle' | 'loading' | 'ready' | 'error'
    data?: SessionInfo
    error?: string
    loadedAt?: number
  }
}

export const useSessionStore = defineStore('session', {
  state: (): SessionState => ({
    byName: {},
    currentInfo: { status: 'idle' },
  }),
  actions: {
    async ensureLoaded(name: string) {
      const existing = this.byName[name]
      if (existing?.status === 'ready') {
        return existing.data!
      }

      this.byName[name] = {
        status: 'loading',
      }

      try {
        const data = await getQuizSession(name)
        this.byName[name] = {
          status: 'ready',
          data,
          loadedAt: Date.now(),
        }
        return data
      } catch (error: any) {
        this.byName[name] = {
          status: 'error',
          error: error?.message ?? 'Failed to load session',
        }
        throw error
      }
    },
    setCurrentSession(name: string) {
      const session = this.byName[name]?.data
      if (!session) {
        this.currentInfo = { status: 'idle' }
        return
      }

      this.currentInfo = {
        status: 'ready',
        data: {
          name,
          namespace: session.metadata.namespace ?? '',
          state: session.spec?.state,
          title: session.spec?.title,
        },
        loadedAt: Date.now(),
      }
    },
  },
})
