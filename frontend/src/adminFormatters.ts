function isObjectLike(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function formatConflictValue(value: unknown): string {
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return '(empty)'
    }
    if (value.every((item) => typeof item !== 'object' || item === null)) {
      return value.map((item) => String(item)).join(', ')
    }
    return `${value.length} item${value.length === 1 ? '' : 's'}`
  }
  if (typeof value === 'boolean') {
    return value ? 'true' : 'false'
  }
  if (isObjectLike(value)) {
    const keys = Object.keys(value)
    return keys.length === 0
      ? '(empty)'
      : `${keys.length} field${keys.length === 1 ? '' : 's'}`
  }
  if (value === null || value === undefined || value === '') {
    return '(empty)'
  }
  return String(value)
}

export function humanizePath(path: string): string {
  return path
    .replace(/^spec\./, '')
    .replace(/\.apiKeySecretRef\./g, ' secret ')
    .replace(/\.orderConfirmationTemplate/g, ' mail template')
    .replace(/\.zeroAmountCheckoutAllowed/g, ' zero checkout')
    .replace(/\./g, ' / ')
}

export function formatEventTimestamp(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date)
}
