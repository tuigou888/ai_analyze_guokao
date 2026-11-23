// 探索性浏览器测试：未登录路由守卫、页面异常与失败请求、键盘可达性、缩放、快速连点
// 复用项目自带 browser.mjs（CDP 最小驱动），不引入新依赖。
import { browser, pause } from '/home/tuigou/works/play/apps/ai_analyze_guokao/web/scripts/browser.mjs'
import { writeFileSync } from 'node:fs'

const origin = 'http://127.0.0.1:8091'
const out = '/home/tuigou/works/play/apps/ai_analyze_guokao/tests/artifacts/ui'
const results = []
const page = await browser()

async function nav(path) { await page.goto(origin + path); await pause(500) }

// 1. 未登录访问受限路由：期望重定向到 /login 或显示登录提示而非白屏
const guardRoutes = ['/practice', '/account', '/wrongbook', '/admin']
for (const r of guardRoutes) {
  await nav(r)
  const state = await page.evaluate(() => ({ url: location.pathname + location.search, hasH1: !!document.querySelector('h1'), text: document.body.innerText.slice(0, 80), blank: document.body.innerText.trim().length < 5 }))
  results.push({ case: `未登录访问 ${r}`, ...state, pass: !state.blank })
  console.log(`未登录 ${r} -> ${JSON.stringify(state).slice(0, 140)}`)
}

// 2. 登录后各页面：未捕获异常 + HTTP>=400 资源 + 白屏检测
await nav('/login')
await page.evaluate(() => {
  const u = document.querySelector('input[type=text],input:not([type])'); const p = document.querySelector('input[type=password]')
  if (u) { u.value = 'qa-u1'; u.dispatchEvent(new Event('input', { bubbles: true })) }
  if (p) { p.value = 'QaUser-2026-tx8'; p.dispatchEvent(new Event('input', { bubbles: true })) }
})
await page.evaluate(() => { const b = [...document.querySelectorAll('button')].find(b => /登录/.test(b.textContent)); if (b) { b.focus(); b.click() } })
await pause(1200)

for (const p of ['/', '/questions', '/papers', '/concepts', '/practice', '/wrongbook', '/favorites', '/account']) {
  const before = page.errors.length
  await nav(p); await pause(600)
  const bad = await page.evaluate(() => performance.getEntriesByType('resource').filter(r => r.responseStatus && r.responseStatus >= 400).map(r => r.name.split('/').pop() + ':' + r.responseStatus))
  const blank = await page.evaluate(() => document.body.innerText.trim().length < 5)
  results.push({ case: `页面健康检查 ${p}`, exceptions: page.errors.length - before, badResources: bad, blank, pass: page.errors.length - before === 0 && bad.length === 0 && !blank })
  console.log(`${p}: exceptions=${page.errors.length - before} bad=${bad.length} blank=${blank}`)
}

// 3. 键盘可达性：登录页 Tab 焦点序列
await nav('/logout'); await nav('/login')
const pressTab = async () => {
  await page.command('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 })
  await page.command('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 })
  await pause(100)
}
await pressTab()
const focus1 = await page.evaluate(() => ({ tag: document.activeElement.tagName, type: document.activeElement.type || '', label: (document.activeElement.labels && document.activeElement.labels[0] && document.activeElement.labels[0].innerText) || document.activeElement.placeholder || '' }))
await pressTab()
const focus2 = await page.evaluate(() => ({ tag: document.activeElement.tagName, type: document.activeElement.type || '', label: (document.activeElement.labels && document.activeElement.labels[0] && document.activeElement.labels[0].innerText) || document.activeElement.placeholder || '' }))
results.push({ case: '登录页键盘 Tab 焦点', focus1, focus2, pass: ['INPUT', 'BUTTON', 'A'].includes(focus1.tag) && ['INPUT', 'BUTTON', 'A'].includes(focus2.tag) && !(focus1.tag === focus2.tag && focus1.label === focus2.label) })
console.log('Tab focus:', JSON.stringify({ focus1, focus2 }))

// 4. 未知路由处理
await nav('/no-such-page-tx9')
const nf = await page.evaluate(() => ({ text: document.body.innerText.slice(0, 100), blank: document.body.innerText.trim().length < 5 }))
results.push({ case: '未知路由处理', ...nf, pass: !nf.blank })
console.log('未知路由:', JSON.stringify(nf))

// 5. 200% 缩放溢出检查
for (const p of ['/', '/questions']) {
  await nav(p)
  await page.command('Emulation.setPageScaleFactor', { pageScaleFactor: 2 })
  await pause(250)
  const m = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, width: innerWidth }))
  results.push({ case: `200% 缩放溢出 ${p}`, ...m, pass: m.scroll <= m.width + 2 })
  console.log(`zoom 200% ${p}:`, JSON.stringify(m))
  await page.command('Emulation.setPageScaleFactor', { pageScaleFactor: 1 })
}

// 6. 搜索按钮快速连点 5 次
await nav('/questions')
for (let i = 0; i < 5; i++) {
  await page.evaluate(() => { const b = [...document.querySelectorAll('main button')].find(b => b.textContent.trim() === '搜索'); if (b) { b.focus(); b.click() } })
  await pause(30)
}
await pause(900)
const rapid = await page.evaluate(() => ({ rows: document.querySelectorAll('.question-row').length, err: !!document.querySelector('main [role=alert]') }))
results.push({ case: '搜索按钮快速连点', ...rapid, pass: !rapid.err })
console.log('快速连点:', JSON.stringify(rapid))

writeFileSync(out + '/explore_result.json', JSON.stringify({ results, browserExceptions: page.errors }, null, 1))
console.log('\n==== 探索结果汇总 ====')
for (const r of results) console.log(`${r.pass ? 'PASS' : 'FAIL'} ${r.case}`)
await page.close()
process.exit(results.every(r => r.pass) ? 0 : 1)
