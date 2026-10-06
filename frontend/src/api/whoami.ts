// Who Kubernetes takes this browser's person to be.
//
// krm-foyer's /auth/whoami spends a fresh SelfSubjectReview with the person's
// own token and answers with the API server's userInfo. Not what the session
// remembers: what the API server says right now, which is the name on every
// write and what RBAC matches on.

import { requestJson } from './http'

export interface WhoAmI {
  userInfo: {
    username?: string
    uid?: string
    groups?: string[]
    extra?: Record<string, string[]>
  }
  issuer: string
  expiresAt: string
}

export async function getWhoAmI(): Promise<WhoAmI> {
  return await requestJson<WhoAmI>('/auth/whoami')
}
