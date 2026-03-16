import { defineStore } from 'pinia'

type DeviceSessionState = {
  /** purely client-side hint to improve UX; not an auth token */
  hasAuthedOnce: boolean
}

const key = 'present-yaml:hasAuthedOnce'

export const useDeviceSessionStore = defineStore('deviceSession', {
  state: (): DeviceSessionState => ({
    hasAuthedOnce: localStorage.getItem(key) === '1',
  }),
  actions: {
    markAuthedOnce() {
      this.hasAuthedOnce = true
      localStorage.setItem(key, '1')
    },
  },
})

