const gitCommitRaw = import.meta.env.VITE_GIT_COMMIT
const gitDirtyRaw = import.meta.env.VITE_GIT_DIRTY
const buildDateRaw = import.meta.env.VITE_BUILD_DATE

export type BuildInfoSummary = {
  gitCommit: string
  isDirty: boolean
  buildDate: string
  buildTimestamp: number | null
  commitWithDirty: string
}

function normalizeText(value: unknown): string {
  return typeof value === 'string' && value.trim() !== '' ? value.trim() : 'unknown'
}

export function parseBuildTimestamp(buildDate: string): number | null {
  const timestamp = Date.parse(buildDate)
  return Number.isNaN(timestamp) ? null : timestamp
}

export function createBuildInfoSummary(input: {
  gitCommit: unknown
  isDirty: boolean
  buildDate: unknown
}): BuildInfoSummary {
  const gitCommit = normalizeText(input.gitCommit)
  const buildDate = normalizeText(input.buildDate)
  const isDirty = input.isDirty

  return {
    gitCommit,
    isDirty,
    buildDate,
    buildTimestamp: parseBuildTimestamp(buildDate),
    commitWithDirty: isDirty ? `${gitCommit}-dirty` : gitCommit,
  }
}

export function getBuildAgeLabel(buildTimestamp: number | null, now = Date.now()): string {
  if (buildTimestamp === null) {
    return 'unknown'
  }

  const elapsedMinutes = Math.max(0, Math.floor((now - buildTimestamp) / 60000))

  if (elapsedMinutes < 1) {
    return '<1m'
  }
  if (elapsedMinutes < 60) {
    return `${elapsedMinutes}m`
  }

  const elapsedHours = Math.floor(elapsedMinutes / 60)
  if (elapsedHours < 48) {
    return `${elapsedHours}h`
  }

  const elapsedDays = Math.floor(elapsedHours / 24)
  if (elapsedDays < 14) {
    return `${elapsedDays}d`
  }

  const elapsedWeeks = Math.floor(elapsedDays / 7)
  if (elapsedWeeks < 8) {
    return `${elapsedWeeks}w`
  }

  const elapsedMonths = Math.floor(elapsedDays / 30)
  if (elapsedMonths < 24) {
    return `${elapsedMonths}mo`
  }

  return `${Math.floor(elapsedDays / 365)}y`
}

export const buildInfo = createBuildInfoSummary({
  gitCommit: gitCommitRaw,
  isDirty: gitDirtyRaw === '1',
  buildDate: buildDateRaw,
})
