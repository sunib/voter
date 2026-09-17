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

// --- what a page needs ------------------------------------------------------

/** One thing a page needs to be able to do, and what it is for. */
export interface Requirement {
  apiGroup: string
  resource: string
  verb: string
  /** Said in the operator's words, not Kubernetes': "open and close rounds",
   *  not "patch quizsessions". The verb is already shown beside it. */
  purpose: string
}

/** The requirements this identity does NOT hold.
 *
 *  Empty while `authz` is null, because "not asked yet" is not "refused" — a
 *  page that treated the two the same would announce a refusal it had not been
 *  told about. Callers pair this with the composable's `loading`. */
export function missingPermissions(
  authz: Authorization | null,
  requirements: Requirement[],
): Requirement[] {
  if (authz === null) return []
  return requirements.filter(
    (r) => !allows(authz, r.apiGroup, r.resource, r.verb),
  )
}

const EXAMPLES = 'examples.configbutler.ai'
const PLATFORM = 'platform.configbutler.ai'
const RBAC = 'rbac.authorization.k8s.io'
const ROOMPASS = 'roompass.configbutler.ai'

/** What the coffee menu editor uses. Declared here rather than described in
 *  prose on the screen, so the page and the Role cannot drift apart quietly. */
export const ADMIN_REQUIREMENTS: Requirement[] = [
  {
    apiGroup: EXAMPLES,
    resource: 'coffeeconfigs',
    verb: 'get',
    purpose: 'read the menu',
  },
  {
    apiGroup: EXAMPLES,
    resource: 'coffeeconfigs',
    verb: 'watch',
    purpose: "see other people's edits arrive live",
  },
  {
    apiGroup: EXAMPLES,
    resource: 'coffeeconfigs',
    verb: 'patch',
    purpose: 'save a change to the menu',
  },
  {
    apiGroup: 'configbutler.ai',
    resource: 'commitrequests',
    verb: 'create',
    purpose: 'press "save now" to commit without waiting for the window',
  },
]

/** What the database request pages use. Note what is NOT here: delete. The
 *  pages do not offer it and the Role does not grant it — withdrawing another
 *  team's request is not a thing this demo does. */
export const DATABASE_REQUIREMENTS: Requirement[] = [
  {
    apiGroup: PLATFORM,
    resource: 'databases',
    verb: 'list',
    purpose: 'see what every team has asked for',
  },
  {
    apiGroup: PLATFORM,
    resource: 'databases',
    verb: 'watch',
    purpose: "see another team's request arrive live",
  },
  {
    apiGroup: PLATFORM,
    resource: 'databases',
    verb: 'create',
    purpose: 'file a new request',
  },
  {
    apiGroup: PLATFORM,
    resource: 'databases',
    verb: 'patch',
    purpose: 'change a request that already exists',
  },
]

/** What the operator page uses. The join code, the rounds, and the switch that
 *  widens what the audience may do — three separate grants, and an identity can
 *  hold some and not others. */
export const ROOM_REQUIREMENTS: Requirement[] = [
  {
    apiGroup: ROOMPASS,
    resource: 'rooms',
    verb: 'get',
    purpose: 'read the room and its rotating join code',
  },
  {
    apiGroup: ROOMPASS,
    resource: 'rooms',
    verb: 'watch',
    purpose: 'follow the code as it rotates, without polling',
  },
  {
    apiGroup: EXAMPLES,
    resource: 'quizsessions',
    verb: 'patch',
    purpose: 'open and close rounds',
  },
  {
    apiGroup: RBAC,
    resource: 'rolebindings',
    verb: 'get',
    purpose: 'see whether the audience may edit the coffee menu',
  },
  {
    apiGroup: RBAC,
    resource: 'rolebindings',
    verb: 'create',
    purpose: 'grant the audience the coffee menu, live',
  },
  {
    apiGroup: RBAC,
    resource: 'rolebindings',
    verb: 'delete',
    purpose: 'take that grant away again',
  },
]
