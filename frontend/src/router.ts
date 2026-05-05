import { createRouter, createWebHistory } from 'vue-router'

import { getPublicSession, type ApiError } from './api/coffee'
import OrderScreen from './screens/OrderScreen.vue'
import AdminScreen from './screens/AdminScreen.vue'
import AdminOrdersScreen from './screens/AdminOrdersScreen.vue'
import AnswerScreen from './screens/AnswerScreen.vue'
import LoginScreen from './screens/LoginScreen.vue'
import ThanksScreen from './screens/ThanksScreen.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: LoginScreen,
      meta: { public: true },
    },
    {
      path: '/join',
      redirect: (to) => ({
        name: 'login',
        query: to.query,
      }),
      meta: { public: true },
    },
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
      component: AdminOrdersScreen,
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

  try {
    await getPublicSession()
    return true
  } catch (error) {
    const apiError = error as ApiError
    if (apiError.status === 401) {
      return {
        name: 'login',
        query: { next: to.fullPath },
      }
    }
    throw error
  }
})
