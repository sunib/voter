import { describe, expect, it } from 'vitest'

import { allows, extraVerbs, type Authorization } from './authz'

function authorization(rules: Authorization['rules']): Authorization {
  return { namespace: 'voter', rules, incomplete: false, evaluationError: '' }
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
