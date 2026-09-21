// Every surface a participant reads, on one page, wired up exactly as the app
// wires it: the real stylesheet, the real PrimeVue preset with the real options,
// and the real QuestionRenderer rather than a copy of its markup.
//
// It exists because the 2026-09-17 demo shipped an editor that was unreadable on
// a dark-mode phone and nobody found out until afterwards. The defects were not
// subtle -- the answer options were blank rectangles -- they were just never
// looked at under `prefers-color-scheme: dark`. So: somewhere to look.
//
// `npm run theme:shoot` drives this with Playwright, in both colour schemes, and
// fails on anything under 4.5:1. Open it by hand with `npm run dev` and
// /dev/theme-preview/index.html when you want to see rather than measure.

import { createApp, h, ref } from 'vue'
import PrimeVue from 'primevue/config'
import Aura from '@primeuix/themes/aura'
import 'primeicons/primeicons.css'
import '../../src/style.css'

import QuestionRenderer from '../../src/components/questions/QuestionRenderer.vue'

/** A labelled specimen. `probe` marks the text node the contrast check reads. */
function section(title: string, body: unknown) {
  return h('section', { style: 'margin: 0 0 2rem' }, [
    h(
      'h2',
      {
        style:
          'font-size:.7rem;letter-spacing:.18em;text-transform:uppercase;opacity:.6;margin:0 0 .6rem',
      },
      title,
    ),
    body as never,
  ])
}

const App = {
  setup() {
    const single = ref<string>()
    const multi = ref<string[]>([])
    const scale = ref<number>()
    return () =>
      h('main', { class: 'page-shell page-shell--narrow' }, [
        section(
          'answer options — single choice',
          h(QuestionRenderer, {
            question: {
              id: 'q1',
              type: 'singleChoice',
              title: 'Does GitOps belong on stage?',
              required: true,
              choices: ['Yes', 'No', 'Ask me after the demo'],
            },
            modelValue: single.value,
            'onUpdate:modelValue': (v: unknown) => (single.value = v as string),
          }),
        ),
        section(
          'answer options — multi choice',
          h(QuestionRenderer, {
            question: {
              id: 'q2',
              type: 'multiChoice',
              title: 'Which of these do you run?',
              choices: ['Flux', 'Argo CD', 'Neither'],
            },
            modelValue: multi.value,
            'onUpdate:modelValue': (v: unknown) =>
              (multi.value = v as string[]),
          }),
        ),
        section(
          'answer options — scale',
          h(QuestionRenderer, {
            question: {
              id: 'q3',
              type: 'scale0to10',
              title: 'How well did that land?',
            },
            modelValue: scale.value,
            'onUpdate:modelValue': (v: unknown) => (scale.value = v as number),
          }),
        ),
        section(
          'the error panel',
          h('section', { class: 'panel panel--danger' }, [
            h('h2', 'Could not open this request'),
            h(
              'p',
              { 'data-probe': 'panel-danger' },
              'databases.platform.configbutler.ai "checkout-postgresql" is forbidden.',
            ),
          ]),
        ),
        section('editor fields', [
          h('label', { class: 'field' }, [
            h('div', { class: 'field__heading' }, 'Shop name'),
            h('input', { value: 'Coffee bar', 'data-probe': 'field-clean' }),
          ]),
          h(
            'label',
            { class: 'field field--dirty', style: 'margin-top:.8rem' },
            [
              h('div', { class: 'field__heading' }, 'Price (cents)'),
              h('input', { value: '273', 'data-probe': 'field-dirty' }),
            ],
          ),
          h(
            'label',
            { class: 'field field--conflict', style: 'margin-top:.8rem' },
            [
              h('div', { class: 'field__heading' }, 'Banner text'),
              h('input', {
                value: 'Their chosen name',
                'data-probe': 'field-conflict',
              }),
            ],
          ),
        ]),
        section(
          'change summary',
          h('ul', { class: 'save-summary__list' }, [
            h('li', { class: 'save-summary__item save-summary__item--dirty' }, [
              h(
                'div',
                { 'data-probe': 'save-summary' },
                'spec.products.0.priceCents — 250 to 273',
              ),
            ]),
          ]),
        ),
        section(
          'recent changes',
          h('ul', { class: 'recent-changes__list' }, [
            h('li', { class: 'recent-changes__item' }, [
              h(
                'div',
                { 'data-probe': 'recent-changes' },
                'Someone changed the banner a moment ago',
              ),
            ]),
          ]),
        ),
        section('buttons', [
          h('button', { class: 'button', 'data-probe': 'button' }, 'Order'),
          h(
            'button',
            {
              class: 'stepper__button',
              style: 'margin-left:.6rem',
              'data-probe': 'stepper',
            },
            '+',
          ),
        ]),
        section(
          'nested card',
          h('div', { class: 'embedded-card' }, [
            h('div', { 'data-probe': 'embedded-card' }, 'Flat white — 273'),
          ]),
        ),
      ])
  },
}

createApp(App)
  .use(PrimeVue, { theme: { preset: Aura } })
  .mount('#app')
