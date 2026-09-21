export type KubeObjectMeta = {
  name?: string
  generateName?: string
  namespace?: string
  uid?: string
  resourceVersion?: string
  /** Moves when the spec moves, and not when a controller writes status. What a
   *  ballot pins -- see createQuizSubmission in api/quiz.ts. */
  generation?: number
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

/** The round's result, written by Voter's tally controller and read by anyone
 *  who may read the round -- which is every participant, with no extra grant.
 *  Every field is optional: a round that has never been tallied has no status
 *  at all, and a screen that assumes one renders NaN. */
export type QuizSessionStatus = {
  /** The metadata.generation this tally was computed against. */
  observedGeneration?: number
  lastTallyTime?: string
  /** Ballots carrying this round's name, against how many passed validation. */
  filed?: number
  counted?: number
  questions?: Array<{
    id: string
    count?: number
    choices?: Record<string, number>
    sum?: number
    /** How many free-text answers were written, against the bounded sample in
     *  `text`. The results endpoint still returns all of them. */
    textTotal?: number
    text?: string[]
  }>
}

export type QuizSession = KubeObject<QuizSessionSpec> & {
  status?: QuizSessionStatus
}

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
