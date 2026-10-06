// The two grants the operator hands out live from /room besides the room-wide
// coffee switch (authz.ts): who may vote, and one person who may edit the
// coffee menu before the rest of the room.
//
// Both are RoleBindings to a Role that lives in Git, created and deleted with
// the operator's own token, exactly like the coffee switch. The state is read
// back from the bindings that exist, matched by the Role they bind and not by
// the names this page gives them, so a binding made from a terminal during the
// talk shows up here as the switch it is.

import { appConfig } from './appConfig'
import type { ApiError } from './http'
import { ROLEBINDINGS, createObject, deleteObject, listObjects } from './kube'

const RBAC = 'rbac.authorization.k8s.io'

export interface Subject {
  kind: string
  name: string
  apiGroup?: string
}

export interface RoleBinding {
  metadata: {
    name: string
    annotations?: Record<string, string>
    labels?: Record<string, string>
  }
  subjects?: Subject[]
  roleRef: { kind: string; name: string; apiGroup?: string }
}

export interface Participant {
  metadata: { name: string }
  spec: {
    displayName: string
    roomRef?: { name: string }
    groups?: string[]
    revoked?: boolean
  }
}

/** The bindings in the application's namespace. Each grant below is a filter
 *  over this one list, so a poll asks the API server once for both. */
export async function listRoleBindings(): Promise<RoleBinding[]> {
  return await listObjects<RoleBinding>(ROLEBINDINGS)
}

function bindsRole(binding: RoleBinding, role: string): boolean {
  return binding.roleRef.kind === 'Role' && binding.roleRef.name === role
}

// --- who may vote -----------------------------------------------------------

/** One binding per group, named after the Role and the group: demo:frontend-vue
 *  gives voter-audience-ballot-frontend-vue. Lower-cased and reduced to what an
 *  object name allows; two groups that differ only in case would share one,
 *  which the Room's own answers never do. */
export function ballotBindingName(group: string): string {
  const suffix = group
    .replace(/^demo:/, '')
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return `${appConfig().audienceBallotRole}-${suffix}`.slice(0, 253)
}

/** The groups that may vote: every Group subject of a binding to the ballot
 *  Role. */
export function ballotGroups(bindings: RoleBinding[]): Set<string> {
  const role = appConfig().audienceBallotRole
  const groups = new Set<string>()
  for (const binding of bindings) {
    if (!bindsRole(binding, role)) continue
    for (const subject of binding.subjects ?? []) {
      if (subject.kind === 'Group') groups.add(subject.name)
    }
  }
  return groups
}

/** Let one group vote, or stop it. Closing deletes every ballot binding that
 *  names only this group, including one made by hand; a binding that also names
 *  other groups is left alone, and the next poll shows the switch still on. */
export async function setBallotGrant(
  group: string,
  granted: boolean,
): Promise<void> {
  const role = appConfig().audienceBallotRole
  if (granted) {
    try {
      await createObject(ROLEBINDINGS, {
        apiVersion: `${RBAC}/v1`,
        kind: 'RoleBinding',
        metadata: {
          name: ballotBindingName(group),
          namespace: appConfig().namespace,
          labels: {
            'app.kubernetes.io/managed-by': 'voter',
            'voter.configbutler.ai/grant': 'ballot',
          },
          annotations: {
            'voter.configbutler.ai/description':
              'Created live from the operator page. Not a Flux resource; gitops-reverser mirrors it to the audit trail.',
          },
        },
        subjects: [{ kind: 'Group', apiGroup: RBAC, name: group }],
        roleRef: { apiGroup: RBAC, kind: 'Role', name: role },
      })
    } catch (cause) {
      // Already granted is the state that was asked for.
      if ((cause as ApiError).status !== 409) throw cause
    }
    return
  }
  const bindings = await listRoleBindings()
  for (const binding of bindings) {
    if (!bindsRole(binding, role)) continue
    const subjects = binding.subjects ?? []
    if (subjects.length === 0) continue
    if (!subjects.every((s) => s.kind === 'Group' && s.name === group)) continue
    await deleteQuietly(binding.metadata.name)
  }
}

// --- one person on the coffee menu ------------------------------------------

/** The apiserver's username for a Room Pass login is `'demo:' + claims.sub`
 *  (authentication-config). */
const PARTICIPANT_USERNAME_PREFIX = 'demo:'
/** Room Pass names a Participant `p-<id>` and sends Dex the id as the user id
 *  (X-Remote-User-Id). */
const PARTICIPANT_NAME_PREFIX = 'p-'

/** The Kubernetes username a participant signs in as.
 *
 *  Dex's `sub` is not opaque in practice: it is base64url, unpadded, of the
 *  protobuf IDTokenSubject{1: user_id, 2: conn_id} (dex server/internal). For a
 *  Room Pass participant both are known here, so the binding can name the user
 *  before they ever sign in again. If Dex ever changed the encoding, the binding
 *  would grant nobody, and the editor's refusal would say so on stage. */
export function participantUsername(
  participantName: string,
  connectorID: string = appConfig().participantConnector,
): string {
  const userID = participantName.startsWith(PARTICIPANT_NAME_PREFIX)
    ? participantName.slice(PARTICIPANT_NAME_PREFIX.length)
    : participantName
  const bytes = [...protoString(1, userID), ...protoString(2, connectorID)]
  return PARTICIPANT_USERNAME_PREFIX + base64url(bytes)
}

function protoString(field: number, value: string): number[] {
  const data = [...new TextEncoder().encode(value)]
  // Wire type 2 (length-delimited), then the length as a varint.
  return [(field << 3) | 2, ...varint(data.length), ...data]
}

function varint(n: number): number[] {
  const out: number[] = []
  while (n > 0x7f) {
    out.push((n & 0x7f) | 0x80)
    n >>>= 7
  }
  out.push(n)
  return out
}

function base64url(bytes: number[]): string {
  return btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '')
}

export interface PersonGrant {
  binding: string
  /** The Kubernetes username the binding names. */
  username: string
  /** What the room knows them as, from the binding's annotation. */
  displayName: string
}

const PERSON_DISPLAY_NAME = 'voter.configbutler.ai/display-name'

function personBindingName(): string {
  return `${appConfig().audienceCoffeeAdminRole}-person`
}

/** Who holds the coffee menu on their own: a binding to the coffee-admin Role
 *  that names a User. Null when nobody does. */
export function personGrant(bindings: RoleBinding[]): PersonGrant | null {
  const role = appConfig().audienceCoffeeAdminRole
  for (const binding of bindings) {
    if (!bindsRole(binding, role)) continue
    const user = (binding.subjects ?? []).find((s) => s.kind === 'User')
    if (user === undefined) continue
    return {
      binding: binding.metadata.name,
      username: user.name,
      displayName:
        binding.metadata.annotations?.[PERSON_DISPLAY_NAME] ?? user.name,
    }
  }
  return null
}

/** Participants who could be picked: in this Room and not revoked. */
export function pickable(participants: Participant[]): Participant[] {
  return participants.filter(
    (p) =>
      !p.spec.revoked &&
      (p.spec.roomRef?.name ?? appConfig().roomName) === appConfig().roomName,
  )
}

/** One participant at random, avoiding whoever holds it now if anyone else
 *  could. */
export function pickParticipant(
  participants: Participant[],
  current: PersonGrant | null,
  random: () => number = Math.random,
): Participant | null {
  const candidates = pickable(participants)
  const others = candidates.filter(
    (p) => participantUsername(p.metadata.name) !== current?.username,
  )
  const pool = others.length > 0 ? others : candidates
  if (pool.length === 0) return null
  return pool[Math.floor(random() * pool.length)] ?? null
}

/** Give the coffee menu to this one participant, taking it from whoever had
 *  it. The room-wide switch is a different binding and is not touched. */
export async function grantPerson(participant: Participant): Promise<void> {
  await revokePerson()
  const role = appConfig().audienceCoffeeAdminRole
  await createObject(ROLEBINDINGS, {
    apiVersion: `${RBAC}/v1`,
    kind: 'RoleBinding',
    metadata: {
      name: personBindingName(),
      namespace: appConfig().namespace,
      labels: {
        'app.kubernetes.io/managed-by': 'voter',
        'voter.configbutler.ai/grant': 'coffee-admin-person',
      },
      annotations: {
        [PERSON_DISPLAY_NAME]: participant.spec.displayName,
        'voter.configbutler.ai/participant': participant.metadata.name,
        'voter.configbutler.ai/description':
          'Created live from the operator page. Not a Flux resource; gitops-reverser mirrors it to the audit trail.',
      },
    },
    subjects: [
      {
        kind: 'User',
        apiGroup: RBAC,
        name: participantUsername(participant.metadata.name),
      },
    ],
    roleRef: { apiGroup: RBAC, kind: 'Role', name: role },
  })
}

/** Take the coffee menu back from the one person who has it. */
export async function revokePerson(): Promise<void> {
  const current = personGrant(await listRoleBindings())
  if (current !== null) await deleteQuietly(current.binding)
  // A binding under the page's own name that somehow names no User would
  // still block the create; it is ours, so it goes too.
  await deleteQuietly(personBindingName())
}

async function deleteQuietly(name: string): Promise<void> {
  try {
    await deleteObject(ROLEBINDINGS, name)
  } catch (cause) {
    // Already gone is the state that was asked for.
    if ((cause as ApiError).status !== 404) throw cause
  }
}
