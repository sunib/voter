import { describe, expect, it } from 'vitest'

import { DATABASE_FIELDS, DATABASE_FORM, fieldLabel } from './databaseForm'
import {
  COST_CENTRES,
  ENGINES,
  emptyDatabaseSpec,
  intentSentence,
  TIERS,
} from './databaseTypes'

// The form is data, so the things that would be typos in a hand-written
// template are assertions here instead.
describe('the Database form description', () => {
  it('addresses each field exactly once, all of them under spec', () => {
    const paths = DATABASE_FIELDS.map((field) => field.path)
    expect(new Set(paths).size).toBe(paths.length)
    for (const path of paths) {
      expect(path.startsWith('spec.')).toBe(true)
    }
  })

  // The CRD's own `required` lists, transcribed. If somebody adds a required
  // field to the CRD and not to the form, the create page will let a person
  // press the button and be refused by the apiserver for a field they were
  // never shown -- which is the one refusal in this demo that is not
  // interesting, just confusing.
  it('marks exactly the fields the CRD requires', () => {
    const required = DATABASE_FIELDS.filter((field) => field.required).map(
      (field) => field.path,
    )
    expect(required.sort()).toEqual(
      [
        'spec.engine',
        'spec.owner.contact',
        'spec.owner.costCentre',
        'spec.owner.team',
        'spec.service',
        'spec.tier',
      ].sort(),
    )
  })

  it('offers only values the CRD enumerates', () => {
    const byPath = new Map(DATABASE_FIELDS.map((f) => [f.path, f]))
    expect(byPath.get('spec.engine')?.options).toEqual(ENGINES)
    expect(byPath.get('spec.tier')?.options).toEqual(TIERS)
    expect(byPath.get('spec.owner.costCentre')?.options).toEqual(COST_CENTRES)
  })

  it('names every group and labels every field', () => {
    for (const group of DATABASE_FORM) {
      expect(group.title).not.toBe('')
      for (const field of group.fields) {
        expect(field.label).not.toBe('')
        expect(fieldLabel(field.path)).toBe(field.label)
      }
    }
  })

  // The change summary prints these. An unknown path has to stay visible rather
  // than come back blank, or a save would list "  → large" with no subject.
  it('falls back to the path for a field it does not render', () => {
    expect(fieldLabel('spec.somethingNew')).toBe('spec.somethingNew')
  })
})

describe('a brand new request', () => {
  it('fills in every CRD default and leaves every required field empty', () => {
    const spec = emptyDatabaseSpec()
    // Defaults, so nobody has to choose a region to ask for a sandbox.
    expect(spec.size).toBe('small')
    expect(spec.region).toBe('eu-central-2')
    expect(spec.dataClassification).toBe('internal')
    expect(spec.deletionPolicy).toBe('snapshot-then-delete')
    // Required and unchosen. A cost centre nobody picked is a bill somebody
    // else pays, so the form asks rather than defaults.
    expect(spec.owner.team).toBe('')
    expect(spec.owner.costCentre).toBe('')
    expect(spec.owner.contact).toBe('')
    expect(spec.service).toBe('')
  })

  // The create page shows this above a form that is empty for the first minute
  // somebody is on it, so a blank has to read as English rather than as a gap.
  it('reads as a sentence even before it is filled in', () => {
    expect(intentSentence(emptyDatabaseSpec())).toBe(
      'A team not yet named needs a postgresql database for a service not yet named.',
    )
  })

  it('reads as the sentence on the slide once it is', () => {
    expect(
      intentSentence({
        ...emptyDatabaseSpec(),
        service: 'checkout',
        owner: {
          team: 'payments-core',
          costCentre: 'CC-finance-07',
          contact: 'pc@example.com',
        },
      }),
    ).toBe(
      'The payments-core team needs a postgresql database for their checkout service.',
    )
  })
})
