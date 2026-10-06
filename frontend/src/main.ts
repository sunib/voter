import { createApp } from 'vue'
import { createPinia } from 'pinia'
import './style.css'
import App from './App.vue'
import { router } from './router'
import { loadAppConfig } from './api/appConfig'

import PrimeVue from 'primevue/config'
import 'primeicons/primeicons.css'

import Aura from '@primeuix/themes/aura'

const app = createApp(App)

app.use(createPinia())
app.use(router)

// PrimeVue: use a PrimeUIX preset for polished form controls.
// We still keep overall page aesthetics (fonts/background) via our global CSS.
app.use(PrimeVue, {
  theme: {
    preset: Aura,
  },
})

// Every screen reads the deployment's settings synchronously, so they are in
// hand before anything renders. Without them no screen can address its objects;
// say so rather than mount pages that would each fail in their own way.
loadAppConfig().then(
  () => app.mount('#app'),
  (cause: unknown) => {
    console.error(cause)
    const root = document.getElementById('app')
    if (root) {
      root.textContent =
        'This page could not load its configuration. Reload to try again.'
    }
  },
)
