import type { CoffeeConfig, CoffeeStatusCondition } from './coffeeTypes'

/** The conditions the page explains, in the order Voter judges them. The first
 *  one that fails is the reason `Ready` gives. */
const PARTS = [
  { type: 'MenuAvailable', label: 'Menu' },
  { type: 'MailKeyAvailable', label: 'Mail' },
  { type: 'PaymentsKeyAvailable', label: 'Payments' },
  { type: 'VouchersAvailable', label: 'Vouchers' },
] as const

export type CoffeeStatusPart = {
  type: string
  label: string
  healthy: boolean
  reason: string
  message: string
}

export type CoffeeStatusSummary = {
  /** Voter has judged this very version of the menu, and it is fine. */
  ready: boolean
  /** The menu moved and Voter has not looked yet. Whatever else this says is
   *  about the version before: true for a second after a save, and for as long
   *  as Voter is down. */
  behind: boolean
  reason: string
  message: string
  generation?: number
  observedGeneration?: number
  firstOrderTime?: string
  parts: CoffeeStatusPart[]
}

/** What the CoffeeConfig's status says, for the page. null when it says nothing:
 *  no Voter has reported on this object, so there is nothing to explain. */
export function summarizeCoffeeStatus(
  config: CoffeeConfig | null | undefined,
): CoffeeStatusSummary | null {
  const conditions = config?.status?.conditions
  const ready = conditions?.find((condition) => condition.type === 'Ready')
  if (!config?.status || !conditions || !ready) return null

  const generation = config.metadata?.generation
  const observedGeneration = config.status.observedGeneration
  const behind = generation !== undefined && observedGeneration !== generation

  const parts = PARTS.flatMap(({ type, label }) => {
    const found = conditions.find(
      (condition: CoffeeStatusCondition) => condition.type === type,
    )
    return found
      ? [
          {
            type,
            label,
            healthy: found.status === 'True',
            reason: found.reason,
            message: found.message ?? '',
          },
        ]
      : []
  })

  return {
    ready: ready.status === 'True' && !behind,
    behind,
    reason: ready.reason,
    message: ready.message ?? '',
    generation,
    observedGeneration,
    firstOrderTime: config.status.firstOrderTime,
    parts,
  }
}

/** An order's time on this person's clock, or what came if it is no date. */
export function formatFirstOrderTime(firstOrderTime: string): string {
  const at = new Date(firstOrderTime)
  return Number.isNaN(at.getTime()) ? firstOrderTime : at.toLocaleTimeString()
}
