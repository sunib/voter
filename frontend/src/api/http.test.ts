import { describe, expect, it } from 'vitest'

import { createApiError, kubernetesAnswer } from './http'

// Bodies as the API server answered them on the fixture (k3d 1.37).
describe('createApiError', () => {
  it('names each refused field, and leaves out the note about stopping', () => {
    const err = createApiError(422, {
      kind: 'Status',
      reason: 'Invalid',
      message:
        'CoffeeConfig.examples.configbutler.ai "demo-coffee" is invalid: [spec.mail.apiKeySecretRef.name: Unsupported value: "nope": supported values: "coffee-mail", <nil>: Invalid value: "null": some validation rules were not checked because the object was invalid; correct the existing errors to complete validation]',
      details: {
        causes: [
          {
            reason: 'FieldValueNotSupported',
            message:
              'Unsupported value: "nope": supported values: "coffee-mail"',
            field: 'spec.mail.apiKeySecretRef.name',
          },
          {
            reason: 'FieldValueInvalid',
            message:
              'Invalid value: "null": some validation rules were not checked because the object was invalid; correct the existing errors to complete validation',
            field: '<nil>',
          },
        ],
      },
    })
    expect(err.message).toBe(
      'spec.mail.apiKeySecretRef.name: Unsupported value: "nope": supported values: "coffee-mail"',
    )
  })

  it("shows an admission policy's own sentence, which names no field", () => {
    const err = createApiError(422, {
      kind: 'Status',
      reason: 'Invalid',
      message:
        "coffeeconfigs.examples.configbutler.ai \"demo-coffee\" is forbidden: ValidatingAdmissionPolicy 'voter-coffee-price' with binding 'voter-coffee-price' denied request: Nobody pays more than €10 for a coffee: Flat White.",
      details: {
        causes: [
          {
            message:
              "ValidatingAdmissionPolicy 'voter-coffee-price' with binding 'voter-coffee-price' denied request: Nobody pays more than €10 for a coffee: Flat White.",
          },
        ],
      },
    })
    expect(err.message).toBe(
      'Nobody pays more than €10 for a coffee: Flat White.',
    )
  })

  it('leaves an RBAC refusal as Kubernetes said it', () => {
    const message =
      'secrets "coffee-mail" is forbidden: User "demo:probe" cannot patch resource "secrets" in API group "" in the namespace "voter"'
    const err = createApiError(403, {
      kind: 'Status',
      reason: 'Forbidden',
      message,
      details: { name: 'coffee-mail', kind: 'secrets' },
    })
    expect(err.message).toBe(message)
  })
})

describe('kubernetesAnswer', () => {
  it('is the whole Status, with the wrapper the screen leaves out', () => {
    const message =
      "x is forbidden: ValidatingAdmissionPolicy 'p' with binding 'p' denied request: No."
    const answer = kubernetesAnswer(
      createApiError(422, { kind: 'Status', code: 422, message }),
    )
    expect(JSON.parse(answer!)).toEqual({ kind: 'Status', code: 422, message })
  })

  it('has nothing to show when something else answered', () => {
    expect(
      kubernetesAnswer(createApiError(409, { error: 'stale' })),
    ).toBeUndefined()
    expect(kubernetesAnswer(new Error('network'))).toBeUndefined()
  })
})
