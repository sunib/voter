import { createApp } from 'vue'
import { createPinia } from 'pinia'
import './style.css'
import App from './App.vue'
import { router } from './router'

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

app.mount('#app')
