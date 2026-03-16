import { defineStore } from 'pinia'
import type { QuizSession, SessionInfo } from '../api/types'
import { getQuizSession, getSessionInfo } from '../api/kube'

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
		async ensureLoaded(name: string, opts?: { joinCode?: string }) {
      const existing = this.byName[name]
      if (existing?.status === 'ready') return existing.data!

      this.byName[name] = {
        status: 'loading',
      }

      try {
        const data = await getQuizSession(name, { joinCode: opts?.joinCode })
        this.byName[name] = {
          status: 'ready',
          data,
          loadedAt: Date.now(),
        }
        return data
      } catch (e: any) {
        this.byName[name] = {
          status: 'error',
          error: e?.message ?? 'Failed to load session',
        }
			throw e
		}
	},
		async fetchSessionInfo(opts?: { joinCode?: string }) {
			this.currentInfo = { status: 'loading' }
			try {
				const data = await getSessionInfo({ joinCode: opts?.joinCode })
				this.currentInfo = {
					status: 'ready',
					data,
					loadedAt: Date.now(),
				}
				return data
			} catch (e: any) {
				this.currentInfo = {
					status: 'error',
					error: e?.message ?? 'Failed to load session info',
				}
				throw e
			}
		},
	},
})
