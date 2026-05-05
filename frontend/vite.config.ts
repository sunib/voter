import { defineConfig, loadEnv } from 'vite'
import basicSsl from '@vitejs/plugin-basic-ssl'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

import { kubeMockPlugin } from './dev/kube-mock-plugin.ts'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const enableMock = env.VITE_DEV_KUBE_MOCK === '1'
  const apiOrigin = env.VITE_DEV_API_ORIGIN?.trim()
  const enableHttps = env.VITE_DEV_HTTPS === '1'

  const proxy = apiOrigin
    ? Object.fromEntries(
        ['/public', '/auth', '/apis'].map((prefix) => [
          prefix,
          {
            target: apiOrigin,
            changeOrigin: true,
            secure: true,
            cookieDomainRewrite: '',
          },
        ]),
      )
    : undefined

  return {
    plugins: [vue(), tailwindcss(), enableMock ? kubeMockPlugin() : undefined, enableHttps ? basicSsl() : undefined].filter(Boolean),
    server: {
      https: enableHttps ? {} : undefined,
      proxy,
    },
  }
})
