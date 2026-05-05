import { createRouter, createWebHistory } from 'vue-router'

import OrderScreen from './screens/OrderScreen.vue'
import AdminScreen from './screens/AdminScreen.vue'
import ThanksScreen from './screens/ThanksScreen.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
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
      path: '/thanks',
      name: 'thanks',
      component: ThanksScreen,
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
