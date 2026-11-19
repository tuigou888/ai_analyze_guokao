// Minimal Chrome DevTools driver for local UI regression; Node 22+, no test dependency.
import { spawn } from 'node:child_process'
import { mkdtempSync, rmSync, existsSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

export const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
export async function browser() {
  const executable = process.env.CHROME_PATH || ['/usr/bin/google-chrome', '/usr/bin/chromium', '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'].find(existsSync)
  if (!executable) throw new Error('Set CHROME_PATH to a Chrome/Chromium executable')
  const profile = mkdtempSync(join(tmpdir(), 'study-ui-browser-'))
  const child = spawn(executable, ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--no-first-run', '--no-default-browser-check', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] })
  let ws
  const pending = new Map(), listeners = new Map()
  let seq = 0
  const errors = []
  async function close() {
    ws?.close()
    child.kill()
    await pause(200)
    rmSync(profile, { recursive: true, force: true, maxRetries: 4, retryDelay: 100 })
  }
  try {
    const endpoint = await new Promise((resolve, reject) => {
      let buffer = ''
      const timeout = setTimeout(() => reject(new Error('Chrome startup timed out')), 15000)
      child.stderr.on('data', chunk => {
        buffer += chunk
        const match = buffer.match(/DevTools listening on (ws:\/\/\S+)/)
        if (match) { clearTimeout(timeout); resolve(match[1]) }
      })
      child.on('exit', code => { clearTimeout(timeout); reject(new Error(`Chrome exited: ${code}`)) })
    })
    ws = new WebSocket(endpoint)
    await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject })
    ws.onmessage = event => {
      const message = JSON.parse(event.data)
      if (message.id && pending.has(message.id)) {
        const item = pending.get(message.id)
        pending.delete(message.id); clearTimeout(item.timeout)
        if (message.error) item.reject(new Error(message.error.message))
        else item.resolve(message.result)
      } else for (const callback of listeners.get(message.method) || []) callback(message.params)
    }
    function send(method, params = {}, sessionId) {
      const id = ++seq
      return new Promise((resolve, reject) => {
        const timeout = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)) }, 20000)
        pending.set(id, { resolve, reject, timeout })
        ws.send(JSON.stringify({ id, method, params, ...(sessionId && { sessionId }) }))
      })
    }
    const { targetId } = await send('Target.createTarget', { url: 'about:blank' })
    const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true })
    const command = (method, params) => send(method, params, sessionId)
    function on(name, callback) { const items = listeners.get(name) || []; items.push(callback); listeners.set(name, items) }
    on('Runtime.exceptionThrown', e => errors.push(e.exceptionDetails?.text || 'Browser exception'))
    // Native confirmations default to cancel. Tests may explicitly install a confirm stub.
    on('Page.javascriptDialogOpening', () => command('Page.handleJavaScriptDialog', { accept: false }).catch(() => {}))
    await command('Page.enable'); await command('Runtime.enable')
    async function evaluate(fn, arg) {
      const result = await command('Runtime.evaluate', { expression: `(${fn.toString()})(${arg === undefined ? '' : JSON.stringify(arg)})`, awaitPromise: true, returnByValue: true })
      if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text)
      return result.result?.value
    }
    async function waitFor(fn, timeout = 10000) {
      const until = Date.now() + timeout
      while (Date.now() < until) { if (await evaluate(fn)) return; await pause(80) }
      throw new Error(`Condition timed out: ${fn.toString().slice(0, 100)}`)
    }
    return {
      errors, evaluate, command, waitFor, close,
      async goto(url) {
        await command('Page.navigate', { url })
        await waitFor(new Function(`return location.origin === ${JSON.stringify(new URL(url).origin)} && document.readyState === 'complete'`))
        await pause(250)
      },
      async viewport(width, height, mobile = width < 768) {
        await command('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile })
        await command('Emulation.setTouchEmulationEnabled', { enabled: mobile })
        await pause(80)
      },
      async screenshot(path) { const result = await command('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }); writeFileSync(path, Buffer.from(result.data, 'base64')) },
    }
  } catch (error) { await close(); throw error }
}
