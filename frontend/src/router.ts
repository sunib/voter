import { createRouter, createWebHistory } from 'vue-router'

import { getSession } from './api/session'
import HomeScreen from './screens/HomeScreen.vue'
import IdentityScreen from './screens/IdentityScreen.vue'
import RoomScreen from './screens/RoomScreen.vue'
import OrderScreen from './screens/OrderScreen.vue'
import AdminScreen from './screens/AdminScreen.vue'
import VoteResultsScreen from './screens/VoteResultsScreen.vue'
import AnswerScreen from './screens/AnswerScreen.vue'
import LoginScreen from './screens/LoginScreen.vue'
import ThanksScreen from './screens/ThanksScreen.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    // The round list is no longer a page of its own -- it is the first section
    // of the home page. Kept as a redirect because it is printed in the demo
    // notes and is the returnTo of any login link still in the wild.
    { path: '/vote', redirect: '/' },
    {
      path: '/answer/:session/results',
      name: 'vote-results',
      component: VoteResultsScreen,
      props: true,
    },
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
      name: 'home',
      component: HomeScreen,
    },
    {
      path: '/coffee',
      name: 'order',
      component: OrderScreen,
    },
    // Reached from the badge carrying your name in the top bar. Guarded like
    // every other page: it reports on a session, so there has to be one.
    {
      path: '/me',
      name: 'identity',
      component: IdentityScreen,
    },
    // The operator's page. No route guard beyond "signed in": what an identity
    // may see here is Kubernetes' answer, and the screen renders the refusal.
    {
      path: '/room',
      name: 'room',
      component: RoomScreen,
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
