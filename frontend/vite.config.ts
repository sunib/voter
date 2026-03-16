import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

import { kubeMockPlugin } from './dev/kube-mock-plugin.ts'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const enableMock = env.VITE_DEV_KUBE_MOCK === '1'

  return {
    plugins: [vue(), tailwindcss(), enableMock ? kubeMockPlugin() : undefined].filter(Boolean),
  }
})
