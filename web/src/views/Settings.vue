<script setup>
import { ref, reactive, onMounted, onBeforeUnmount, computed } from 'vue'
import { api } from '../api'
import { toast } from '../toast'

const MASK = '••••••••'

const sections = [
  { id: 'settings-llm', label: 'LLM 接口' },
  { id: 'settings-mail', label: '邮件服务' },
  { id: 'settings-quota', label: '用户额度' },
  { id: 'settings-cost', label: '成本与预算' },
  { id: 'settings-reference', label: '参考约定' },
]
const active = ref(sections[0].id)

const form = reactive({
  'llm.base_url': '',
  'llm.model': '',
  'llm.concurrency': '1',
  'llm.timeout_sec': '120',
  'llm.daily_budget_usd': '',
  'llm.price_per_1k': '',
  'mail.smtp_host': '',
  'mail.smtp_port': '',
  'mail.smtp_user': '',
  'mail.from': '',
  'user.daily_free_questions': '50',
  'user.daily_free_papers': '3',
})

const loading = ref(true)
const loadError = ref('')
const saving = ref(false)
const testing = ref(false)
const keyIsSet = ref(false)
const mailKeyIsSet = ref(false)
const mailTestTo = ref('')
const mailTesting = ref(false)

const llmSecret = ref(null)
const mailSecret = ref(null)
const secretDirty = reactive({ llm: false, mail: false })
const baseline = ref('')
const values = computed(() => Object.fromEntries(Object.entries(form).map(([key, value]) => [key, String(value)])))
const dirty = computed(() => (baseline.value !== '' && JSON.stringify(values.value) !== baseline.value) || secretDirty.llm || secretDirty.mail)
const busy = computed(() => saving.value || testing.value || mailTesting.value)
defineExpose({ dirty, busy })

function applySettings(settings) {
  for (const key of Object.keys(form)) {
    if (settings[key] != null) form[key] = settings[key]
  }
  keyIsSet.value = settings['llm.api_key'] === MASK
  mailKeyIsSet.value = settings['mail.smtp_password'] === MASK
  baseline.value = JSON.stringify(values.value)
  if (llmSecret.value) llmSecret.value.value = ''
  if (mailSecret.value) mailSecret.value.value = ''
  secretDirty.llm = false
  secretDirty.mail = false
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const { settings } = await api.getSettings()
    applySettings(settings)
  } catch (e) {
    loadError.value = e.message
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function save() {
  if (busy.value || loading.value) return
  saving.value = true
  try {
    const payload = { ...values.value }
    const apiKey = llmSecret.value?.value
    const mailPassword = mailSecret.value?.value
    if (apiKey && apiKey !== MASK) payload['llm.api_key'] = apiKey
    if (mailPassword && mailPassword !== MASK) payload['mail.smtp_password'] = mailPassword
    const { settings } = await api.saveSettings(payload)
    applySettings(settings)
    toast.ok('系统配置已保存')
  } catch (e) {
    toast.err('配置保存失败', { lines: [e.message] })
  } finally {
    saving.value = false
  }
}

async function testConn() {
  if (busy.value || loading.value) return
  testing.value = true
  try {
    const r = await api.testLLM()
    const lines = [`配置模型 ${r.model_config}`, `回执模型 ${r.model_response || '(回执未给出)'}`]
    if (r.warning) {
      lines.push(`警告：${r.warning}`)
      toast.warn(`连接成功但有警告 · 耗时 ${r.latency_ms} ms`, { lines })
    } else {
      toast.ok(`连接成功 · 耗时 ${r.latency_ms} ms`, { lines, timeout: 8000 })
    }
  } catch (e) {
    toast.err('连接失败', { lines: [e.message] })
  } finally {
    testing.value = false
  }
}

async function testMail() {
  if (busy.value || loading.value || !mailTestTo.value) return
  mailTesting.value = true
  try {
    await api.testMail(mailTestTo.value)
    toast.ok('测试邮件已发送，请检查收件箱')
  } catch (e) {
    toast.err('测试邮件发送失败', { lines: [e.message] })
  } finally {
    mailTesting.value = false
  }
}

// 左侧锚点导航：点击平滑滚动，滚动经过时高亮当前分区。
// 参考线取视口顶部略下方，「最后一个越过参考线的分区」即当前所在分区。
let spyLock = 0
function jump(id) {
  const behavior = window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth'
  document.getElementById(id)?.scrollIntoView({ behavior, block: 'start' })
  active.value = id
  // 平滑滚动期间暂停 scroll-spy，避免中途经过的分区把高亮抢走。
  spyLock = Date.now() + 800
}

let ticking = false
function spy() {
  if (ticking) return
  ticking = true
  requestAnimationFrame(() => {
    ticking = false
    if (Date.now() < spyLock) return
    // 末尾分区不足一屏时永远不会越过参考线，滚到底强制高亮它。
    if (window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 2) {
      active.value = sections.at(-1).id
      return
    }
    const compact = window.matchMedia('(max-width: 960px)').matches
    const line = compact ? 140 : 96
    let current = sections[0].id
    for (const s of sections) {
      const el = document.getElementById(s.id)
      if (el && el.getBoundingClientRect().top <= line) current = s.id
    }
    active.value = current
  })
}

onMounted(() => window.addEventListener('scroll', spy, { passive: true }))
onBeforeUnmount(() => window.removeEventListener('scroll', spy))
</script>

<template>
  <p v-if="loading" class="card" role="status">正在读取系统配置…</p>
  <div v-else-if="loadError" class="notice err" role="alert">
    读取配置失败：{{ loadError }} <button type="button" @click="load">重试</button>
  </div>
  <div v-else class="settings-layout">
    <nav class="settings-nav" aria-label="系统配置分区">
      <a
        v-for="s in sections"
        :key="s.id"
        :href="`#${s.id}`"
        :class="{ active: active === s.id }"
        :aria-current="active === s.id ? 'location' : undefined"
        @click.prevent="jump(s.id)"
      >{{ s.label }}</a>
    </nav>

    <div class="settings-body">
      <section id="settings-llm" class="settings-section">
        <header class="settings-head">
          <h2>LLM 接口</h2>
          <p class="muted small">配置 OpenAI 兼容的模型服务。测试连接使用服务器上已保存的配置。</p>
        </header>
        <fieldset class="settings-group" :disabled="saving">
          <legend>连接地址</legend>
          <label>
            <span>API 地址（OpenAI 兼容）</span>
            <input v-model="form['llm.base_url']" class="mono" placeholder="https://example.com/v1" aria-describedby="settings-url-hint" />
            <small id="settings-url-hint" class="field-hint">填至 /v1，客户端会自动添加 /chat/completions。</small>
          </label>
        </fieldset>
        <fieldset class="settings-group" :disabled="saving">
          <legend>鉴权</legend>
          <label>
            <span>API Key</span>
            <input ref="llmSecret" type="password" class="mono" autocomplete="new-password"
                   :placeholder="keyIsSet ? '已保存' : '尚未设置'" aria-describedby="settings-api-key-hint"
                   @input="secretDirty.llm = Boolean($event.target.value)" />
            <small id="settings-api-key-hint" class="field-hint">留空保留已保存的密钥。填入新密钥后保存可替换。</small>
          </label>
        </fieldset>
        <fieldset class="settings-group" :disabled="saving">
          <legend>模型参数</legend>
          <label>
            <span>模型名</span>
            <input v-model="form['llm.model']" class="mono" placeholder="填写服务商提供的模型名" />
          </label>
        </fieldset>
        <fieldset class="settings-group" :disabled="saving">
          <legend>运行限制</legend>
          <div class="settings-fields">
            <label>
              <span>并发数</span>
              <input v-model="form['llm.concurrency']" type="number" min="1" max="32" />
              <small class="field-hint">2 核 4 GB 服务器建议从 1 开始。</small>
            </label>
            <label>
              <span>单请求超时（秒）</span>
              <input v-model="form['llm.timeout_sec']" type="number" min="10" />
            </label>
          </div>
        </fieldset>
        <div class="settings-actions">
          <button type="button" class="primary" :disabled="busy" @click="save">
            {{ saving ? '保存中…' : '保存配置' }}
          </button>
          <button type="button" :disabled="busy" @click="testConn">
            {{ testing ? '测试中…' : '测试连接' }}
          </button>
          <span v-if="dirty" class="muted small" role="status">有未保存更改</span>
        </div>
      </section>

      <section id="settings-mail" class="settings-section">
        <header class="settings-head">
          <h2>邮件服务（注册验证码）</h2>
          <p class="muted small">配置 SMTP 后可发送真实邮件验证码；未配置时验证码只打印日志（开发降级）。</p>
        </header>
        <fieldset class="settings-fields" :disabled="saving">
          <label>
            <span>SMTP 主机</span>
            <input v-model="form['mail.smtp_host']" class="mono" placeholder="smtp.example.com" />
          </label>
          <label>
            <span>SMTP 端口</span>
            <input v-model="form['mail.smtp_port']" class="mono" placeholder="465" />
          </label>
          <label>
            <span>SMTP 用户名</span>
            <input v-model="form['mail.smtp_user']" class="mono" placeholder="留空则不认证" />
          </label>
          <label>
            <span>SMTP 密码</span>
            <input ref="mailSecret" type="password" class="mono" autocomplete="new-password"
                   :placeholder="mailKeyIsSet ? '已保存' : '尚未设置'"
                   @input="secretDirty.mail = Boolean($event.target.value)" />
            <small class="field-hint">留空表示不修改；填入新值会覆盖</small>
          </label>
          <label>
            <span>发件人</span>
            <input v-model="form['mail.from']" class="mono" placeholder="noreply@example.com" />
          </label>
        </fieldset>
        <div class="settings-actions">
          <button type="button" class="primary" :disabled="busy" @click="save">
            {{ saving ? '保存中…' : '保存配置' }}
          </button>
          <span v-if="dirty" class="muted small" role="status">有未保存更改</span>
        </div>
        <div class="material settings-sub">
          <h3>发送测试邮件</h3>
          <form class="settings-inline" @submit.prevent="testMail">
            <input v-model="mailTestTo" type="email" required placeholder="you@example.com" aria-label="收件人邮箱" />
            <button class="ghost" type="submit" :disabled="busy || !mailTestTo">
              {{ mailTesting ? '发送中…' : '发送测试邮件' }}
            </button>
          </form>
        </div>
      </section>

      <section id="settings-quota" class="settings-section">
        <header class="settings-head">
          <h2>用户额度</h2>
          <p class="muted small">免费用户每日可用额度；会员不受限。留空或非正数会回落默认值。</p>
        </header>
        <fieldset class="settings-fields" :disabled="saving">
          <label>
            <span>每日免费题量</span>
            <input v-model="form['user.daily_free_questions']" type="number" min="1" />
            <small class="field-hint">默认 50 题</small>
          </label>
          <label>
            <span>每日免费试卷</span>
            <input v-model="form['user.daily_free_papers']" type="number" min="1" />
            <small class="field-hint">默认 3 卷</small>
          </label>
        </fieldset>
        <div class="settings-actions">
          <button type="button" class="primary" :disabled="busy" @click="save">
            {{ saving ? '保存中…' : '保存配置' }}
          </button>
          <span v-if="dirty" class="muted small" role="status">有未保存更改</span>
        </div>
      </section>

      <section id="settings-cost" class="settings-section">
        <header class="settings-head">
          <h2>成本与预算</h2>
          <p class="muted small">
            价格用于批次结束后的花费统计；不填则只记 token 数，不谎报为 0 成本。
          </p>
        </header>
        <fieldset class="settings-fields" :disabled="saving">
          <label>
            <span>价格（美元 / 千 token，输入,输出）</span>
            <input v-model="form['llm.price_per_1k']" class="mono" placeholder="0.0005,0.0015" />
          </label>
          <label>
            <span>每日预算上限（美元）</span>
            <input v-model="form['llm.daily_budget_usd']" type="number" min="0" step="0.01" placeholder="留空不限制" />
            <small class="field-hint">预算配置项；当前网站不开放实时 AI 请求，此值尚未作为运行时预算闸门执行。</small>
          </label>
        </fieldset>
        <div class="settings-actions">
          <button type="button" class="primary" :disabled="busy" @click="save">
            {{ saving ? '保存中…' : '保存配置' }}
          </button>
          <span v-if="dirty" class="muted small" role="status">有未保存更改</span>
        </div>
      </section>

      <section id="settings-reference" class="settings-section">
        <header class="settings-head">
          <h2>参考约定</h2>
          <p class="muted small">当前后端支持的配置键（也可用 <code>gk config set</code> 修改）与成本估算口径。</p>
        </header>
        <details class="settings-fold">
          <summary>成本估算参考</summary>
          <table class="data-table settings-reference-table">
            <tbody>
              <tr><td>单题标注</td><td class="mono">约 3.3k token（含重试，以批次统计为准）</td></tr>
              <tr><td>全量标注</td><td class="mono">耗时取决于实际题量、并发和服务限流</td></tr>
              <tr><td>MVP 50 题</td><td class="mono">按实际端点价格与 token 用量计算</td></tr>
            </tbody>
          </table>
        </details>
        <details class="settings-fold">
          <summary>接口约定（配置键一览）</summary>
          <table class="data-table settings-reference-table">
            <tbody>
              <tr><td class="mono">llm.base_url</td><td>OpenAI 兼容地址，含 /v1</td></tr>
              <tr><td class="mono">llm.api_key</td><td>密文存储</td></tr>
              <tr><td class="mono">llm.model</td><td>请求用的模型名</td></tr>
              <tr><td class="mono">llm.concurrency</td><td>批处理并发数</td></tr>
              <tr><td class="mono">llm.timeout_sec</td><td>单请求超时</td></tr>
              <tr><td class="mono">llm.price_per_1k</td><td>"输入,输出" 美元/千 token</td></tr>
              <tr><td class="mono">llm.daily_budget_usd</td><td>每日成本上限</td></tr>
              <tr><td class="mono">mail.smtp_host</td><td>SMTP 服务器地址</td></tr>
              <tr><td class="mono">mail.smtp_port</td><td>SMTP 端口（465=TLS）</td></tr>
              <tr><td class="mono">mail.smtp_user</td><td>SMTP 用户名（可选）</td></tr>
              <tr><td class="mono">mail.smtp_password</td><td>SMTP 密码（密文存储）</td></tr>
              <tr><td class="mono">mail.from</td><td>发件人地址</td></tr>
              <tr><td class="mono">user.daily_free_questions</td><td>免费用户每日题量（默认 50）</td></tr>
              <tr><td class="mono">user.daily_free_papers</td><td>免费用户每日试卷数（默认 3）</td></tr>
            </tbody>
          </table>
        </details>
      </section>
    </div>
  </div>
</template>

<style scoped>
.settings-layout {
  display: grid;
  grid-template-columns: 180px minmax(0, 920px);
  gap: var(--space-6);
  align-items: start;
  min-width: 0;
}

.settings-nav {
  position: sticky;
  top: calc(var(--topbar-height) + var(--space-4));
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-panel);
  background: var(--color-surface);
}

.settings-nav a {
  display: flex;
  align-items: center;
  flex: none;
  min-height: 44px;
  text-decoration: none;
  color: var(--color-text-muted);
  font-size: var(--font-size-control);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-control);
  white-space: nowrap;
}

.settings-nav a:hover { background: var(--color-nav-hover); }

.settings-nav a.active {
  color: var(--color-primary);
  background: var(--color-primary-soft);
  font-weight: 650;
}

.settings-body { min-width: 0; }

.settings-section {
  scroll-margin-top: calc(var(--topbar-height) + var(--space-4));
  border: 1px solid var(--color-border);
  border-radius: var(--radius-panel);
  background: var(--color-surface);
  padding: var(--space-6);
}

.settings-section + .settings-section { margin-top: var(--space-4); }
.settings-head { margin-bottom: var(--space-4); }
.settings-head p { margin-bottom: 0; }

.settings-section fieldset {
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.settings-group legend {
  width: 100%;
  border-top: 1px solid var(--color-border);
  padding: var(--space-3) 0 var(--space-2);
  font-size: var(--font-size-control);
  font-weight: 650;
}

.settings-section .settings-group + .settings-group { margin-top: var(--space-4); }

.settings-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
}

.settings-group > label { max-width: 600px; }
.settings-section label { min-width: 0; }
.settings-section input { min-width: 0; max-width: 100%; }

.settings-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-6);
}

.settings-sub { margin-top: var(--space-6); padding: var(--space-4); }
.settings-sub h3 { margin-bottom: var(--space-3); }

.settings-inline {
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.settings-inline input { flex: 1 1 220px; margin: 0; }

.settings-fold {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface-subtle);
  padding: var(--space-3) var(--space-4);
}

.settings-fold + .settings-fold { margin-top: var(--space-4); }
.settings-fold summary { font-size: var(--font-size-label); font-weight: 600; }
.settings-fold[open] summary { margin-bottom: var(--space-3); }
.settings-reference-table { background: var(--color-surface); table-layout: fixed; overflow-wrap: anywhere; }
.settings-reference-table td:first-child { color: var(--color-text-muted); width: 42%; }

@media (max-width: 960px) {
  .settings-layout { grid-template-columns: minmax(0, 1fr); gap: var(--space-3); }
  .settings-nav {
    top: var(--topbar-height);
    flex-direction: row;
    overflow-x: auto;
    z-index: 1;
  }
  .settings-section { scroll-margin-top: calc(var(--topbar-height) + 76px); }
}

@media (max-width: 640px) {
  .settings-section { padding: var(--space-4); }
  .settings-fields { grid-template-columns: minmax(0, 1fr); }
  .settings-inline button { width: 100%; }
}
</style>
