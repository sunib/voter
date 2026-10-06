import { beforeAll, describe, expect, it } from 'vitest'

import { useTestAppConfig } from './testAppConfig'
import {
  ballotBindingName,
  ballotGroups,
  participantUsername,
  personGrant,
  pickParticipant,
  type Participant,
  type RoleBinding,
} from './roomGrants'

beforeAll(async () => {
  await useTestAppConfig()
})

function binding(
  name: string,
  role: string,
  subjects: RoleBinding['subjects'],
  annotations?: Record<string, string>,
): RoleBinding {
  return {
    metadata: { name, annotations },
    subjects,
    roleRef: {
      kind: 'Role',
      name: role,
      apiGroup: 'rbac.authorization.k8s.io',
    },
  }
}

function participant(name: string, extra: Partial<Participant['spec']> = {}) {
  return {
    metadata: { name },
    spec: {
      displayName: name.replace(/^p-/, ''),
      roomRef: { name: 'demo' },
      ...extra,
    },
  } satisfies Participant
}

describe('participantUsername', () => {
  // base64url(0a 05 "simon" 12 09 "room-pass"), computed outside this module.
  it("is demo: plus Dex's subject for the participant id", () => {
    expect(participantUsername('p-simon')).toBe('demo:CgVzaW1vbhIJcm9vbS1wYXNz')
  })

  it('encodes a length past one varint byte', () => {
    const id = 'a'.repeat(130)
    const sub = participantUsername(`p-${id}`).slice('demo:'.length)
    const bytes = Uint8Array.from(
      atob(sub.replace(/-/g, '+').replace(/_/g, '/')),
      (c) => c.charCodeAt(0),
    )
    expect([...bytes.slice(0, 3)]).toEqual([0x0a, 0x82, 0x01])
  })
})

describe('ballotBindingName', () => {
  it('names the binding after the Role and the group', () => {
    expect(ballotBindingName('demo:frontend-vue')).toBe(
      'voter-audience-ballot-frontend-vue',
    )
    expect(ballotBindingName('demo:voter-audience')).toBe(
      'voter-audience-ballot-voter-audience',
    )
  })

  it('reduces what an object name cannot hold', () => {
    expect(ballotBindingName('demo:Talk:Front_End')).toBe(
      'voter-audience-ballot-talk-front-end',
    )
  })
})

describe('ballotGroups', () => {
  it('collects the groups bound to the ballot Role, however the binding is named', () => {
    const groups = ballotGroups([
      binding('voter-audience-ballot-frontend-vue', 'voter-audience-ballot', [
        { kind: 'Group', name: 'demo:frontend-vue' },
      ]),
      binding('made-by-hand', 'voter-audience-ballot', [
        { kind: 'Group', name: 'demo:voter-audience' },
      ]),
      binding('voter-audience', 'voter-audience', [
        { kind: 'Group', name: 'demo:frontend-svelte' },
      ]),
    ])
    expect([...groups].sort()).toEqual([
      'demo:frontend-vue',
      'demo:voter-audience',
    ])
  })
})

describe('personGrant', () => {
  it('finds the User bound to the coffee-admin Role, not the room-wide Group', () => {
    const grant = personGrant([
      binding('voter-audience-coffee-admin', 'voter-audience-coffee-admin', [
        { kind: 'Group', name: 'demo:voter-audience' },
      ]),
      binding(
        'voter-audience-coffee-admin-person',
        'voter-audience-coffee-admin',
        [{ kind: 'User', name: 'demo:abc' }],
        { 'voter.configbutler.ai/display-name': 'Ada' },
      ),
    ])
    expect(grant).toEqual({
      binding: 'voter-audience-coffee-admin-person',
      username: 'demo:abc',
      displayName: 'Ada',
    })
  })

  it('is null when only the room-wide switch is on', () => {
    expect(
      personGrant([
        binding('voter-audience-coffee-admin', 'voter-audience-coffee-admin', [
          { kind: 'Group', name: 'demo:voter-audience' },
        ]),
      ]),
    ).toBeNull()
  })
})

describe('pickParticipant', () => {
  it('skips revoked participants and those of another Room', () => {
    const picked = pickParticipant(
      [
        participant('p-gone', { revoked: true }),
        participant('p-elsewhere', { roomRef: { name: 'other' } }),
        participant('p-ada'),
      ],
      null,
      () => 0,
    )
    expect(picked?.metadata.name).toBe('p-ada')
  })

  it('picks somebody else than the current holder when it can', () => {
    const current = {
      binding: 'x',
      username: participantUsername('p-ada'),
      displayName: 'ada',
    }
    const picked = pickParticipant(
      [participant('p-ada'), participant('p-bob')],
      current,
      () => 0,
    )
    expect(picked?.metadata.name).toBe('p-bob')
  })

  it('picks the holder again when nobody else is in the room', () => {
    const current = {
      binding: 'x',
      username: participantUsername('p-ada'),
      displayName: 'ada',
    }
    expect(
      pickParticipant([participant('p-ada')], current, () => 0.99)?.metadata
        .name,
    ).toBe('p-ada')
  })

  it('is null in an empty room', () => {
    expect(pickParticipant([], null)).toBeNull()
  })
})
