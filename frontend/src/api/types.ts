export type KubeObjectMeta = {
  name?: string
  generateName?: string
  namespace?: string
  uid?: string
  resourceVersion?: string
  creationTimestamp?: string
  labels?: Record<string, string>
}

export type KubeTypeMeta = {
  apiVersion: string
  kind: string
}

export type KubeObject<TSpec> = KubeTypeMeta & {
  metadata: KubeObjectMeta
  spec: TSpec
}

export type QuizSessionSpec = {
  title?: string
  state?: 'draft' | 'live' | 'closed'
  questions?: Array<{
    id: string
    type: 'singleChoice' | 'multiChoice' | 'scale0to10' | 'number' | 'freeText'
    title: string
    required?: boolean
    choices?: string[]
    min?: number
    max?: number
    placeholder?: string
  }>
}

export type QuizSession = KubeObject<QuizSessionSpec>

export type SessionInfo = {
	name: string
	namespace: string
	state?: 'draft' | 'live' | 'closed'
	title?: string
}

export type QuizSubmissionSpec = {
  sessionRef: {
    group?: 'examples.configbutler.ai'
    kind?: 'QuizSession'
    name: string
  }
  submittedAt: string
  answers: Array<
    | { questionId: string; singleChoice: string }
    | { questionId: string; multiChoice: string[] }
    | { questionId: string; number: number }
    | { questionId: string; freeText: string }
  >
}

export type QuizSubmission = KubeObject<QuizSubmissionSpec>
