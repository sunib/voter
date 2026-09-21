// Renders dev/theme-preview in a real browser under both colour schemes,
// measures WCAG contrast on the surfaces a participant actually reads, and
// writes a screenshot of each so somebody can look at it.
//
// Computed styles, not token arithmetic: the 2026-09-17 defects were all cases
// where the tokens were fine and something downstream had opted out of them, and
// only the cascade knows that. Anything under 4.5:1 fails.
//
// Usage: npm run theme:shoot [-- --out <dir>]

import { chromium } from 'playwright'
import { createServer } from 'vite'
import { mkdirSync } from 'node:fs'

const MINIMUM = 4.5
const outIndex = process.argv.indexOf('--out')
const outDir = outIndex > -1 ? process.argv[outIndex + 1] : 'theme-shots'
mkdirSync(outDir, { recursive: true })

const server = await createServer({ server: { port: 0 }, logLevel: 'error' })
await server.listen()
const { port } = server.httpServer.address()
const url = `http://localhost:${port}/dev/theme-preview/index.html`

const measure = () => {
  const parse = (value) => {
    const [r, g, b, a = 1] = value.match(/[\d.]+/g).map(Number)
    return { r, g, b, a }
  }
  // The effective background: walk up compositing every translucent layer, the
  // way the screen does. A surface that is 80% white over a dark card is neither.
  const backdrop = (el) => {
    let layers = []
    for (let node = el; node; node = node.parentElement) {
      const bg = parse(getComputedStyle(node).backgroundColor)
      if (bg.a > 0) layers.push(bg)
      if (bg.a === 1) break
    }
    let out = layers.pop() ?? { r: 255, g: 255, b: 255, a: 1 }
    while (layers.length) {
      const top = layers.pop()
      out = {
        r: top.r * top.a + out.r * (1 - top.a),
        g: top.g * top.a + out.g * (1 - top.a),
        b: top.b * top.a + out.b * (1 - top.a),
        a: 1,
      }
    }
    return out
  }
  const luminance = ({ r, g, b }) => {
    const channel = (c) => {
      const s = c / 255
      return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
    }
    return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
  }
  const results = []
  for (const el of document.querySelectorAll('[data-probe]')) {
    const fgRaw = parse(getComputedStyle(el).color)
    const bg = backdrop(el)
    // Text can be translucent too; composite it onto its own backdrop first.
    const fg =
      fgRaw.a === 1
        ? fgRaw
        : {
            r: fgRaw.r * fgRaw.a + bg.r * (1 - fgRaw.a),
            g: fgRaw.g * fgRaw.a + bg.g * (1 - fgRaw.a),
            b: fgRaw.b * fgRaw.a + bg.b * (1 - fgRaw.a),
          }
    const [hi, lo] = [luminance(fg), luminance(bg)].sort((a, b) => b - a)
    results.push({
      probe: el.dataset.probe,
      ratio: Number(((hi + 0.05) / (lo + 0.05)).toFixed(2)),
    })
  }
  return results
}

const browser = await chromium.launch()
let failures = 0
for (const colorScheme of ['light', 'dark']) {
  const page = await browser.newPage({
    colorScheme,
    viewport: { width: 420, height: 900 },
  })
  await page.goto(url, { waitUntil: 'networkidle' })
  await page.screenshot({
    path: `${outDir}/${colorScheme}.png`,
    fullPage: true,
  })
  const results = await page.evaluate(measure)
  console.log(`\n  ${colorScheme}`)
  for (const { probe, ratio } of results) {
    const ok = ratio >= MINIMUM
    if (!ok) failures++
    console.log(
      `    ${ok ? 'ok  ' : 'FAIL'}  ${String(ratio).padStart(6)}:1  ${probe}`,
    )
  }
  await page.close()
}
await browser.close()
await server.close()

console.log(`\n  screenshots in ${outDir}/`)
if (failures > 0) {
  console.error(`\n  ${failures} surface(s) below ${MINIMUM}:1.`)
  process.exit(1)
}
