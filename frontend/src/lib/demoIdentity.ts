export const DEMO_EMAIL_DOMAIN = 'demo.configbutler.ai'

const STABLE_ID_STORAGE_KEY = 'voter.stableId'
const STABLE_ID_PATTERN = /^[1-9][0-9]{5}$/

export function getOrGenerateStableID(): string {
  const existing = localStorage.getItem(STABLE_ID_STORAGE_KEY)
  if (existing && STABLE_ID_PATTERN.test(existing)) {
    return existing
  }
  const fresh = String(Math.floor(100000 + Math.random() * 900000))
  localStorage.setItem(STABLE_ID_STORAGE_KEY, fresh)
  return fresh
}

export function defaultDisplayName(stableID: string): string {
  return `Anonymous ${stableID}`
}

export function defaultEmail(stableID: string): string {
  return `${stableID}@${DEMO_EMAIL_DOMAIN}`
}

export function clearStableID(): void {
  localStorage.removeItem(STABLE_ID_STORAGE_KEY)
}
