import { createRouter, createWebHistory } from 'vue-router'

import JoinScreen from './screens/JoinScreen.vue'
import AnswerScreen from './screens/AnswerScreen.vue'
import ThanksScreen from './screens/ThanksScreen.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/join' },
    {
      path: '/join/:session?',
      name: 'join',
      component: JoinScreen,
      props: true,
    },
    {
      path: '/s/:session/answer',
      name: 'answer',
      component: AnswerScreen,
      props: true,
    },
    {
      path: '/s/:session/thanks',
      name: 'thanks',
      component: ThanksScreen,
      props: true,
    },
    { path: '/:pathMatch(.*)*', redirect: '/join' },
  ],
})

