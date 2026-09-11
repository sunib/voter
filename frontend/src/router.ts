import { createRouter, createWebHistory } from 'vue-router'

import { getSession } from './api/session'
import OrderScreen from './screens/OrderScreen.vue'
import AdminScreen from './screens/AdminScreen.vue'
import RoundsScreen from './screens/RoundsScreen.vue'
import VoteResultsScreen from './screens/VoteResultsScreen.vue'
import AnswerScreen from './screens/AnswerScreen.vue'
import LoginScreen from './screens/LoginScreen.vue'
import ThanksScreen from './screens/ThanksScreen.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/vote', name: 'vote', component: RoundsScreen },
    { path: '/answer/:session/results', name: 'vote-results', component: VoteResultsScreen, props: true },
    {
      path: '/login',
      name: 'login',
      component: LoginScreen,
      meta: { public: true },
    },
    // NOTE: there is deliberately no '/join' route. That path belongs to Room
    // Pass, which Traefik routes to a different Service on this same host.
    // Claiming it here made client-side navigation shadow the real join form.
    {
      path: '/',
      name: 'order',
      component: OrderScreen,
    },
    {
      path: '/admin',
      name: 'admin',
      component: AdminScreen,
    },
    {
      path: '/admin/orders',
      name: 'admin-orders',
      redirect: '/admin',
    },
    {
      path: '/admin/commits',
      name: 'admin-commits',
      redirect: '/admin',
    },
    {
      path: '/answer/:session',
      name: 'answer',
      component: AnswerScreen,
      props: true,
    },
    {
      path: '/thanks',
      name: 'thanks',
      component: ThanksScreen,
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach(async (to) => {
  if (to.meta.public) {
    return true
  }

  // /auth/session, not /public/session. The legacy endpoint reported on a
  // browser-asserted identity and returned 401 for a real OIDC session, so a
  // successful login still bounced back to the login screen.
  const session = await getSession()
  if (session !== null) {
    return true
  }
  return { name: 'login', query: { next: to.fullPath } }
})
