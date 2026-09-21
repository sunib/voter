// Fails when a colour is written down instead of taken from a token.
//
// The app has one dark palette and it lives in six lines of `:root` in
// style.css. Everything that reads a token flips with it; everything that spells
// a colour out does not, and the failure is silent -- the page still renders,
// just white-on-white for whoever has dark mode on. That is not hypothetical:
// on 2026-09-17 the answer-option rows read at 1.18:1 and the error panel at
// 1.06:1 for every attendee whose phone was in dark mode, and nobody in the room
// could report it as anything more precise than "I can't read the buttons".
// docs/post-demo-2026-09-17.md.
//
// So this is a linter and not a test: it does not check contrast, it checks that
// the question of contrast is being asked in one place. Run by `task lint`.

import { readFileSync } from 'node:fs'
import { globSync } from 'node:fs'
import { relative } from 'node:path'

const root = new URL('..', import.meta.url).pathname

/** A hardcoded colour, and what to reach for instead. */
const rules = [
  {
    // Tailwind's black/white scale. `text-black/55` is "secondary text" spelled
    // as a colour -- in dark mode it is near-black on a dark card.
    pattern:
      /\b(?:hover:|focus:|active:|group-hover:|disabled:)?(?:text|bg|border|divide|ring|fill|stroke)-(?:black|white)(?:\/\d+)?\b/g,
    hint: 'use a token: text-[rgb(var(--muted))], bg-[rgb(var(--surface-raised))], border-[rgb(var(--line))]/10',
  },
  {
    // A light literal in CSS. Anything at the top of the 0-255 range is a light
    // surface, and a light surface with no dark counterpart is the whole bug.
    pattern:
      /\b(?:background|background-color|color)\s*:\s*(?:#[fFeE][0-9a-fA-F]{2,5}\b|rgba?\(\s*2[0-5][0-9]\s*,|white\b)/g,
    hint: 'add a --surface-* token pair in :root and the dark block, then use rgb(var(--surface-x) / a)',
  },
]

/** Lines that are allowed to spell a colour out.
 *
 *  There is no list of exempt files here on purpose. A colour earns its
 *  exemption by saying why on its own line -- `/* not themeable: ... *\/` -- so
 *  the reason is read by whoever next touches that line rather than by whoever
 *  next opens this script. The two gradients below are matched literally because
 *  they are a matched pair: the body's specular sheen, which already has its own
 *  dark counterpart at 0.04 where the light one is 0.4. */
const allowed = [
  '/* not themeable:',
  'linear-gradient(180deg, rgba(255, 255, 255, 0.4), transparent 30%)',
  'linear-gradient(180deg, rgba(255, 255, 255, 0.04), transparent 32%)',
]

const files = globSync('src/**/*.{vue,css,ts}', { cwd: root })
let failures = 0

for (const file of files.sort()) {
  const lines = readFileSync(`${root}/${file}`, 'utf8').split('\n')
  lines.forEach((line, index) => {
    if (allowed.some((exempt) => line.includes(exempt))) return
    for (const { pattern, hint } of rules) {
      for (const match of line.matchAll(pattern)) {
        failures++
        console.error(
          `${relative(process.cwd(), `${root}/${file}`)}:${index + 1}  ${match[0].trim()}\n    ${hint}`,
        )
      }
    }
  })
}

if (failures > 0) {
  console.error(
    `\n${failures} hardcoded colour${failures === 1 ? '' : 's'}. ` +
      'A colour that is not a token does not have a dark mode.',
  )
  process.exit(1)
}
console.log(`theme tokens: ${files.length} files, no hardcoded colours`)
