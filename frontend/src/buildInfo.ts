const gitCommitRaw = import.meta.env.VITE_GIT_COMMIT
const gitDirtyRaw = import.meta.env.VITE_GIT_DIRTY
const buildDateRaw = import.meta.env.VITE_BUILD_DATE

const gitCommit = typeof gitCommitRaw === 'string' && gitCommitRaw.trim() !== '' ? gitCommitRaw.trim() : 'unknown'
const isDirty = gitDirtyRaw === '1'
const buildDate = typeof buildDateRaw === 'string' && buildDateRaw.trim() !== '' ? buildDateRaw.trim() : 'unknown'

const commitWithDirty = isDirty ? `${gitCommit}-dirty` : gitCommit

export const buildInfo = {
  gitCommit,
  isDirty,
  buildDate,
  commitWithDirty,
} as const

