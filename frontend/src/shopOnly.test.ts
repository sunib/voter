import { describe, expect, it } from 'vitest'

import { useTestAppConfig } from './api/testAppConfig'
import { shopOnlyRedirect } from './shopOnly'

describe('shopOnlyRedirect', () => {
  it('lets every route through when the deployment is not shop-only', async () => {
    await useTestAppConfig()
    for (const name of ['home', 'admin', 'room', 'order', undefined]) {
      expect(shopOnlyRedirect(name)).toBeNull()
    }
  })

  it('sends everything but the shop to the order page when shop-only', async () => {
    await useTestAppConfig({ shopOnly: true })
    for (const name of ['order', 'thanks', 'login', 'me']) {
      expect(shopOnlyRedirect(name)).toBeNull()
    }
    for (const name of [
      'home',
      'admin',
      'admin-orders',
      'room',
      'databases',
      'vote-results',
      undefined,
    ]) {
      expect(shopOnlyRedirect(name)).toEqual({ name: 'order' })
    }
  })
})
