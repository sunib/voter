import { describe, expect, it } from 'vitest'

import {
  allows,
  extraVerbs,
  flattenResourceRules,
  missingPermissions,
  ADMIN_REQUIREMENTS,
  ROOM_REQUIREMENTS,
  type Authorization,
} from './authz'

function authorization(rules: Authorization['rules']): Authorization {
  return {
    namespace: 'voter',
    rules,
    incomplete: false,
    evaluationError: '',
    review: {
      apiVersion: 'authorization.k8s.io/v1',
      kind: 'SelfSubjectRulesReview',
      spec: { namespace: 'voter' },
    },
  }
}

const COFFEE = 'examples.configbutler.ai'

describe('allows', () => {
  it('reports a verb the identity holds', () => {
    const authz = authorization([
      { apiGroup: COFFEE, resource: 'coffeeconfigs', verbs: ['get', 'patch'] },
    ])
    expect(allows(authz, COFFEE, 'coffeeconfigs', 'patch')).toBe(true)
  })

  it('reports a verb the identity does not hold', () => {
    const authz = authorization([
      {
        apiGroup: COFFEE,
        resource: 'coffeeconfigs',
        verbs: ['get', 'list', 'watch'],
      },
    ])
    // The state demo 1 opens in: readable, not editable.
    expect(allows(authz, COFFEE, 'coffeeconfigs', 'patch')).toBe(false)
  })

  // The important one. A grant restricted to named objects gives the verb on
  // THOSE objects only, so treating it as general would make the admin page
  // claim an edit the API server is going to refuse.
  it('does not count a grant restricted to named objects', () => {
    const authz = authorization([
      {
        apiGroup: COFFEE,
        resource: 'coffeeconfigs',
        verbs: ['patch'],
        names: ['some-other-menu'],
      },
    ])
    expect(allows(authz, COFFEE, 'coffeeconfigs', 'patch')).toBe(false)
  })

  it('honours wildcards, which is what an operator identity holds', () => {
    const authz = authorization([
      { apiGroup: '*', resource: '*', verbs: ['*'] },
    ])
    expect(allows(authz, COFFEE, 'coffeeconfigs', 'patch')).toBe(true)
    expect(allows(authz, 'anything', 'else', 'delete')).toBe(true)
  })

  it('is false before any answer has arrived', () => {
    // Null means "not asked yet", never "refused". A page that treated the two
    // the same would announce a refusal it had not been told about.
    expect(allows(null, COFFEE, 'coffeeconfigs', 'get')).toBe(false)
  })

  it('does not confuse two resources in the same group', () => {
    const authz = authorization([
      { apiGroup: COFFEE, resource: 'quizsubmissions', verbs: ['create'] },
    ])
    expect(allows(authz, COFFEE, 'coffeeconfigs', 'create')).toBe(false)
  })
})

describe('extraVerbs', () => {
  it('surfaces verbs the table has no column for', () => {
    // deletecollection has no column. Dropping it silently would show a
    // narrower grant than the identity actually holds.
    expect(
      extraVerbs({
        apiGroup: COFFEE,
        resource: 'coffeeconfigs',
        verbs: ['get', 'deletecollection'],
      }),
    ).toEqual(['deletecollection'])
  })

  it('does not repeat a wildcard, which the columns already show', () => {
    expect(
      extraVerbs({ apiGroup: COFFEE, resource: 'coffeeconfigs', verbs: ['*'] }),
    ).toEqual([])
  })
})

describe('missingPermissions', () => {
  const requirements = [
    {
      apiGroup: COFFEE,
      resource: 'coffeeconfigs',
      verb: 'get',
      purpose: 'read the menu',
    },
    {
      apiGroup: COFFEE,
      resource: 'coffeeconfigs',
      verb: 'patch',
      purpose: 'save a change',
    },
  ]

  it('names only what is absent', () => {
    const authz = authorization([
      { apiGroup: COFFEE, resource: 'coffeeconfigs', verbs: ['get', 'watch'] },
    ])
    // Mapped rather than indexed: under strict TS, missing[0] is possibly
    // undefined, and this asserts the whole set rather than one element.
    expect(missingPermissions(authz, requirements).map((r) => r.verb)).toEqual([
      'patch',
    ])
  })

  it('is empty when everything is held', () => {
    const authz = authorization([
      { apiGroup: COFFEE, resource: 'coffeeconfigs', verbs: ['get', 'patch'] },
    ])
    expect(missingPermissions(authz, requirements)).toEqual([])
  })

  // "Not asked yet" is not "refused". A page that treated the two the same
  // would flash a refusal panel at someone who holds everything, for as long
  // as the first round trip takes.
  it('reports nothing missing before the first answer arrives', () => {
    expect(missingPermissions(null, requirements)).toEqual([])
  })

  it('reports every requirement for an identity holding nothing', () => {
    expect(missingPermissions(authorization([]), requirements)).toHaveLength(2)
  })
})

// The requirement sets are what the screens render instead of hand-written
// prose, so a typo in one is a line of nonsense in front of a room.
describe('the declared page requirements', () => {
  it.each([
    ['admin', ADMIN_REQUIREMENTS],
    ['room', ROOM_REQUIREMENTS],
  ])('%s requirements are complete and unique', (_name, requirements) => {
    expect(requirements.length).toBeGreaterThan(0)
    for (const requirement of requirements) {
      expect(requirement.resource).not.toBe('')
      expect(requirement.verb).not.toBe('')
      // The purpose is the whole point: the verb is already on screen beside
      // it, so an empty or duplicated purpose makes the row worthless.
      expect(requirement.purpose.length).toBeGreaterThan(8)
    }
    const keys = requirements.map(
      (r) => `${r.apiGroup}/${r.resource}/${r.verb}`,
    )
    expect(new Set(keys).size).toBe(keys.length)
  })

  it('asks for the grant verbs the room switch actually uses', () => {
    // get to render the switch, create and delete to move it. Dropping one
    // would leave an operator staring at a control that half works.
    const rolebindings = ROOM_REQUIREMENTS.filter(
      (r) => r.resource === 'rolebindings',
    ).map((r) => r.verb)
    expect(rolebindings.sort()).toEqual(['create', 'delete', 'get'])
  })
})

// Ported from the Go handler that used to answer /auth/rules; these are its
// rules, now kept in the browser.
describe('flattenResourceRules', () => {
  it('merges the verbs of every rule for one group and resource', () => {
    expect(
      flattenResourceRules([
        { apiGroups: [COFFEE], resources: ['coffeeconfigs'], verbs: ['get'] },
        { apiGroups: [COFFEE], resources: ['coffeeconfigs'], verbs: ['patch', 'get'] },
      ]),
    ).toEqual([
      { apiGroup: COFFEE, resource: 'coffeeconfigs', verbs: ['get', 'patch'] },
    ])
  })

  // Merging would show "patch" as holding on every CoffeeConfig, when it only
  // holds on one -- a page claiming more than the API server will allow.
  it('keeps a grant on named objects apart from an unrestricted one', () => {
    expect(
      flattenResourceRules([
        { apiGroups: [COFFEE], resources: ['coffeeconfigs'], verbs: ['get'] },
        {
          apiGroups: [COFFEE],
          resources: ['coffeeconfigs'],
          resourceNames: ['demo-coffee'],
          verbs: ['patch'],
        },
      ]),
    ).toEqual([
      { apiGroup: COFFEE, resource: 'coffeeconfigs', verbs: ['get'] },
      {
        apiGroup: COFFEE,
        resource: 'coffeeconfigs',
        verbs: ['patch'],
        names: ['demo-coffee'],
      },
    ])
  })

  it('reads a rule without apiGroups as the core group, and sorts core first', () => {
    expect(
      flattenResourceRules([
        { apiGroups: [COFFEE], resources: ['quizsessions'], verbs: ['list'] },
        { resources: ['configmaps'], verbs: ['get'] },
      ]).map((row) => `${row.apiGroup}/${row.resource}`),
    ).toEqual(['/configmaps', `${COFFEE}/quizsessions`])
  })

  it('passes a wildcard through rather than inventing resources', () => {
    expect(
      flattenResourceRules([{ apiGroups: ['*'], resources: ['*'], verbs: ['*'] }]),
    ).toEqual([{ apiGroup: '*', resource: '*', verbs: ['*'] }])
  })
})
