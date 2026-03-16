import type { QuizSession, QuizSubmission, SessionInfo } from './types'

export type KubeConfig = {
  /** e.g. /apis/examples.configbutler.ai/v1alpha1 */
  apiPrefix: string
  namespace: string
}

export type KubeError = {
  status: number
  message: string
  body?: unknown
}

const defaultConfig: KubeConfig = {
  apiPrefix: import.meta.env.VITE_KUBE_API_PREFIX ?? '/apis/examples.configbutler.ai/v1alpha1',
  namespace: import.meta.env.VITE_KUBE_NAMESPACE ?? 'present',
}

function isAuthError(status: number) {
  return status === 401 || status === 403
}

async function readJsonOrText(res: Response): Promise<unknown> {
  const ct = res.headers.get('content-type') ?? ''
  if (ct.includes('application/json')) return await res.json()
  return await res.text()
}

export async function kubeFetch<T>(
  input: {
    path: string
    method?: 'GET' | 'POST'
    body?: unknown
    joinCode?: string
  },
  cfg: KubeConfig = defaultConfig,
): Promise<T> {
  const isRootPath = input.path.startsWith('/auth/')
  const url = isRootPath ? input.path : `${cfg.apiPrefix}${input.path}`

  const headers = new Headers()
  if (input.body !== undefined) headers.set('content-type', 'application/json')
  // IMPORTANT: this header name must match Traefik + forwardAuth expectations.
  if (input.joinCode) headers.set('X-Join-Code', input.joinCode)

  const res = await fetch(url, {
    method: input.method ?? 'GET',
    headers,
    credentials: 'include',
    body: input.body === undefined ? undefined : JSON.stringify(input.body),
  })

  if (!res.ok) {
    const body = await readJsonOrText(res)
    const msg =
      typeof body === 'string'
        ? body
        : (body as any)?.message ?? `Request failed (${res.status})`
    const err: KubeError = { status: res.status, message: msg, body }
    // Make auth errors easy to branch on in screens/guards.
    if (isAuthError(res.status)) throw err
    throw err
  }

  return (await res.json()) as T
}

export function kubePaths(cfg: KubeConfig = defaultConfig) {
  return {
    session: (name: string) =>
      `/namespaces/${encodeURIComponent(cfg.namespace)}/quizsessions/${encodeURIComponent(name)}`,
    submissions: () =>
      `/namespaces/${encodeURIComponent(cfg.namespace)}/quizsubmissions`,
  }
}

export async function getQuizSession(
	sessionName: string,
	opts?: { joinCode?: string },
): Promise<QuizSession> {
	const p = kubePaths()
	return await kubeFetch<QuizSession>({
		path: p.session(sessionName),
		method: 'GET',
		joinCode: opts?.joinCode,
	})
}

export async function getSessionInfo(opts?: { joinCode?: string }): Promise<SessionInfo> {
	return await kubeFetch<SessionInfo>({
		path: '/auth/session-info',
		method: 'GET',
		joinCode: opts?.joinCode,
	})
}

export async function createQuizSubmission(input: {
  sessionName: string
  answers: QuizSubmission['spec']['answers']
}): Promise<QuizSubmission> {
  const p = kubePaths()
  const now = new Date().toISOString()

  const body: QuizSubmission = {
    apiVersion: 'examples.configbutler.ai/v1alpha1',
    kind: 'QuizSubmission',
    metadata: {
      generateName: `${input.sessionName}-`,
    },
    spec: {
      sessionRef: {
        group: 'examples.configbutler.ai',
        kind: 'QuizSession',
        name: input.sessionName,
      },
      submittedAt: now,
      answers: input.answers,
    },
  }

  return await kubeFetch<QuizSubmission>({
    path: p.submissions(),
    method: 'POST',
    body,
  })
}
