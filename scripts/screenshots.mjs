// README screenshots and a browser smoke test, against a demo instance (DEMO_DATA=true seeds eighteen watches
// with months of changes):
//
//   DATA_DIR=/tmp/lookout-demo DEMO_DATA=true AUTH_MODE=none PORT=18224 ./pocket-lookout
//   CDP=http://127.0.0.1:19291 BASE=http://127.0.0.1:18224 node scripts/screenshots.mjs   # any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                                  # checks only
//   OUT=/tmp/shots … node scripts/screenshots.mjs                                           # somewhere else than docs/screenshots
// playwright-core comes from web/node_modules, or set PW_FROM to a package.json next to an install of it.
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(process.env.PW_FROM || new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:18224'
const shots = process.env.SHOTS !== '0'
const dir = process.env.OUT ? process.env.OUT.replace(/\/?$/, '/') : new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: phone ? 3 : 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light',
    userAgent: phone ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) errors.push(m.text()) })
  await page.goto(base + '/login')
  return { ctx, page }
}
async function go(page, path, wait = 1200) {
  await page.goto(base + path, { waitUntil: 'networkidle' })
  // headless Chrome freezes CSS fade-ins: show everything at once
  await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; animation: none !important; }' })
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(wait)
}
async function shot(page, name) { if (shots) await page.screenshot({ path: dir + name + '.png' }) }
const api = (page, p) => page.evaluate(u => fetch(u).then(r => r.json()), p)

const { ctx: c0, page: p0 } = await session()
const watches = await api(p0, '/api/v1/watches')
assert.ok(watches.length >= 15, 'demo watches')
const byTitle = t => watches.find(w => w.title.startsWith(t))
const visa = byTitle('Visa'), flat = byTitle('2-bedroom'), status = byTitle('Hetzner'), shop = byTitle('AirPods')
const flatChanges = await api(p0, `/api/v1/watches/${flat.id}/changes`)
const shopChanges = await api(p0, `/api/v1/watches/${shop.id}/changes`)
await c0.close()

// ---- desktop ----
{
  const { ctx, page } = await session()
  await go(page, '/')
  assert.ok(await page.getByText('Visa appointments').first().isVisible(), 'watch list renders')
  await shot(page, 'desktop-watches')
  await go(page, '/changes')
  await shot(page, 'desktop-changes')
  await go(page, `/changes/${flatChanges[0].id}`)
  await shot(page, 'desktop-change')
  await go(page, `/watches/${visa.id}`)
  await shot(page, 'desktop-watch')
  await go(page, `/watches/${flat.id}/edit`, 1800)
  await shot(page, 'desktop-edit')
  // the picture compare
  await page.evaluate(() => localStorage.setItem('plk-diff-view', 'picture'))
  await go(page, `/changes/${shopChanges[0].id}`, 1500)
  await shot(page, 'desktop-picture')
  await page.evaluate(() => localStorage.removeItem('plk-diff-view'))
  await go(page, `/watches/${status.id}?tab=checks`)
  await shot(page, 'desktop-checks')
  await go(page, '/settings/notifications')
  await shot(page, 'desktop-settings')
  await ctx.close()
}
// ---- desktop, dark ----
{
  const { ctx, page } = await session({ dark: true })
  await go(page, '/')
  await shot(page, 'desktop-dark')
  await ctx.close()
}
// ---- phone ----
{
  const { ctx, page } = await session({ phone: true })
  await go(page, '/')
  await shot(page, 'phone-watches')
  await go(page, `/changes/${flatChanges[0].id}`)
  await shot(page, 'phone-change')
  await go(page, '/changes')
  await shot(page, 'phone-changes')
  await go(page, `/watches/${visa.id}`)
  await shot(page, 'phone-watch')
  await go(page, '/add')
  await shot(page, 'phone-add')
  await ctx.close()
}
await browser.close().catch(() => {})
if (errors.length) { console.error(errors.join('\n')); process.exit(1) }
console.log('ok')
