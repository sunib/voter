// What this identity may do, as the API server reports it.
//
// Every value here originates in a SelfSubjectRulesReview the backend spent
// with THIS browser's token. Nothing in the SPA decides permissions; a page
// that reads this is rendering Kubernetes' answer, and the same answer is what
// the API server will give when the page actually tries the thing.
//
// That distinction matters for the demo. A screen may use these rules to
// EXPLAIN a refusal in advance, but it must not use them to hide the attempt --
// the refusal the room should see is a real 403, not a disabled button.

import { currentCsrfToken } from './session'

export interface AuthzRule {
  /** "" for the core group, "*" when the grant really is unrestricted. */
  apiGroup: string
  resource: string
  verbs: string[]
  /** Present only when the grant is restricted to specific object names. */
  names?: string[]
}

export interface Authorization {
  namespace: string
  rules: AuthzRule[]
  /** An authorizer could not enumerate. The list is a floor, not the truth. */
  incomplete: boolean
  evaluationError: string
}

/** The verbs the table shows as columns, in the order a reader expects: read
 *  first, then the ones that change something. A verb outside this list still
 *  appears — see extraVerbs — so a grant can never be silently invisible. */
export const DISPLAY_VERBS = [
  'get',
  'list',
  'watch',
  'create',
  'update',
  'patch',
  'delete',
] as const

export async function getAuthorization(): Promise<Authorization> {
  const res = await fetch('/auth/rules', {
    credentials: 'include',
    headers: { accept: 'application/json' },
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw Object.assign(
      new Error(
        body.error ?? `Could not read your permissions (${res.status})`,
      ),
      { status: res.status },
    )
  }
  return (await res.json()) as Authorization
}

/** True when the identity holds `verb` on `resource` unconditionally.
 *
 *  Rules carrying `names` are deliberately NOT counted: they grant the verb on
 *  named objects only, and treating them as general would make a page claim a
 *  permission the API server will refuse on everything else. */
export function allows(
  authz: Authorization | null,
  apiGroup: string,
  resource: string,
  verb: string,
): boolean {
  if (authz === null) return false
  return authz.rules.some(
    (rule) =>
      (rule.names ?? []).length === 0 &&
      (rule.apiGroup === apiGroup || rule.apiGroup === '*') &&
      (rule.resource === resource || rule.resource === '*') &&
      (rule.verbs.includes(verb) || rule.verbs.includes('*')),
  )
}

/** Verbs held on a resource that are outside DISPLAY_VERBS, so the table can
 *  show them rather than pretend the grant is smaller than it is. */
export function extraVerbs(rule: AuthzRule): string[] {
  return rule.verbs.filter(
    (verb) => verb !== '*' && !DISPLAY_VERBS.includes(verb as never),
  )
}

// --- the audience grant the operator controls -------------------------------

export interface AudienceGrant {
  granted: boolean
  name: string
  /** The group the binding names, echoed back on a successful grant. */
  group?: string
}

/** Whether the room may edit the coffee menu. Reading this needs permission on
 *  rolebindings, which a participant does not have — so a 403 here is the
 *  correct answer for a participant and the caller should treat it as "not an
 *  operator" rather than as a fault. */
export async function getAudienceGrant(): Promise<AudienceGrant> {
  const res = await fetch('/public/audience/coffee-admin', {
    credentials: 'include',
    headers: { accept: 'application/json' },
  })
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw Object.assign(
      new Error(body.error ?? `Could not read the grant (${res.status})`),
      { status: res.status },
    )
  }
  return body as AudienceGrant
}

/** Create or delete the RoleBinding. The caller's own token does it, so a
 *  refusal here is the API server's, rendered verbatim. */
export async function setAudienceGrant(
  granted: boolean,
): Promise<AudienceGrant> {
  const res = await fetch('/public/audience/coffee-admin', {
    method: 'PUT',
    credentials: 'include',
    headers: {
      'content-type': 'application/json',
      'x-csrf-token': currentCsrfToken(),
    },
    body: JSON.stringify({ granted }),
  })
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw Object.assign(
      new Error(body.error ?? `Could not change the grant (${res.status})`),
      { status: res.status },
    )
  }
  return body as AudienceGrant
}
