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
  'llm.api_key': '',
  'llm.model': '',
  'llm.concurrency': '1',
  'llm.timeout_sec': '120',
  'llm.daily_budget_usd': '',
  'llm.price_per_1k': '',
  'mail.smtp_host': '',
  'mail.smtp_port': '',
  'mail.smtp_user': '',
  'mail.smtp_password': '',
  'mail.from': '',
  'user.daily_free_questions': '50',
  'user.daily_free_papers': '3',
})

const loading = ref(true)
const saving = ref(false)
const testing = ref(false)
const keyIsSet = ref(false)
const mailKeyIsSet = ref(false)
const mailTestTo = ref('')
const mailTesting = ref(false)

// 后端把已保存的密钥以掩码返回。提交时必须原样送回，
// 后端会用「等于掩码就保持原值」的规则避免把它写成字面量掩码。
const keyPlaceholder = computed(() => (keyIsSet.value ? '已保存（留空或保持不变即可）' : '尚未设置'))
const mailKeyPlaceholder = computed(() => (mailKeyIsSet.value ? '已保存（留空或保持不变即可）' : '尚未设置'))

async function load() {
  loading.value = true
  try {
    const { settings } = await api.getSettings()
    for (const k of Object.keys(form)) {
      const v = settings[k]
      if (v === undefined || v === null) continue
      if (k === 'llm.api_key') {
        keyIsSet.value = v === MASK
        form[k] = keyIsSet.value ? MASK : ''
        continue
      }
      if (k === 'mail.smtp_password') {
        mailKeyIsSet.value = v === MASK
        form[k] = mailKeyIsSet.value ? MASK : ''
        continue
      }
      form[k] = v
    }
  } catch (e) {
    toast.err('读取配置失败', { lines: [e.message] })
  } finally {
    loading.value = false
  }
}
onMounted(load)

const saveLabels = { llm: 'LLM 配置', mail: '邮件配置', quota: '用户额度', cost: '成本与预算' }

async function save(area) {
  saving.value = true
  try {
    const payload = { ...form }
    // 密钥仍是掩码时不要发出去，让后端保持原值。
    if (payload['llm.api_key'] === MASK) delete payload['llm.api_key']
    if (payload['mail.smtp_password'] === MASK) delete payload['mail.smtp_password']
    await api.saveSettings(payload)
    toast.ok(`${saveLabels[area] || '配置'}已保存`)
    await load()
  } catch (e) {
    toast.err(`${saveLabels[area] || '配置'}保存失败`, { lines: [e.message] })
  } finally {
    saving.value = false
  }
}

async function testConn() {
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
  if (!mailTestTo.value) return
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
  document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
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
    const line = 96
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
  <div class="settings-layout">
    <nav class="settings-nav" aria-label="系统配置分区">
      <a
        v-for="s in sections"
        :key="s.id"
        :href="`#${s.id}`"
        :class="{ active: active === s.id }"
        @click.prevent="jump(s.id)"
      >{{ s.label }}</a>
    </nav>

    <div class="settings-body">
      <section id="settings-llm" class="settings-section">
        <header class="settings-head">
          <h2>LLM 接口</h2>
          <p class="muted small">模型接口参数保存在服务器，密钥读取时只返回掩码。</p>
        </header>
        <div class="settings-fields">
          <label>
            <span>API 地址（OpenAI 兼容）</span>
            <input v-model="form['llm.base_url']" class="mono" placeholder="https://example.com/v1" />
            <small class="field-hint">填到 /v1 为止，客户端会自动拼 /chat/completions</small>
          </label>
          <label>
            <span>API Key</span>
            <input v-model="form['llm.api_key']" type="password" class="mono"
                   :placeholder="keyPlaceholder" autocomplete="off" />
            <small class="field-hint">留空表示不修改；填入新值会覆盖</small>
          </label>
          <label>
            <span>模型名</span>
            <input v-model="form['llm.model']" class="mono" placeholder="deepseek-flash" />
          </label>
          <label>
            <span>并发数</span>
            <input v-model="form['llm.concurrency']" type="number" min="1" max="32" />
            <small class="field-hint">2 核 4GB 服务器建议从 1 开始</small>
          </label>
          <label>
            <span>单请求超时（秒）</span>
            <input v-model="form['llm.timeout_sec']" type="number" min="10" />
            <small class="field-hint">生成类请求较慢</small>
          </label>
        </div>
        <div class="settings-actions">
          <button class="primary" :disabled="saving || loading" @click="save('llm')">
            {{ saving ? '保存中…' : '保存' }}
          </button>
          <button class="ghost" :disabled="testing || loading" @click="testConn">
            {{ testing ? '测试中…' : '测试连接' }}
          </button>
        </div>
      </section>

      <section id="settings-mail" class="settings-section">
        <header class="settings-head">
          <h2>邮件服务（注册验证码）</h2>
          <p class="muted small">配置 SMTP 后可发送真实邮件验证码；未配置时验证码只打印日志（开发降级）。</p>
        </header>
        <div class="settings-fields">
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
            <input v-model="form['mail.smtp_password']" type="password" class="mono"
                   :placeholder="mailKeyPlaceholder" autocomplete="off" />
            <small class="field-hint">留空表示不修改；填入新值会覆盖</small>
          </label>
          <label>
            <span>发件人</span>
            <input v-model="form['mail.from']" class="mono" placeholder="noreply@example.com" />
          </label>
        </div>
        <div class="settings-actions">
          <button class="primary" :disabled="saving || loading" @click="save('mail')">
            {{ saving ? '保存中…' : '保存' }}
          </button>
        </div>
        <div class="material settings-sub">
          <h3>发送测试邮件</h3>
          <div class="settings-inline">
            <input v-model="mailTestTo" type="email" placeholder="you@example.com" aria-label="收件人邮箱" />
            <button class="ghost" :disabled="mailTesting || !mailTestTo" @click="testMail">
              {{ mailTesting ? '发送中…' : '发送测试邮件' }}
            </button>
          </div>
        </div>
      </section>

      <section id="settings-quota" class="settings-section">
        <header class="settings-head">
          <h2>用户额度</h2>
          <p class="muted small">免费用户每日可用额度；会员不受限。留空或非正数会回落默认值。</p>
        </header>
        <div class="settings-fields">
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
        </div>
        <div class="settings-actions">
          <button class="primary" :disabled="saving || loading" @click="save('quota')">
            {{ saving ? '保存中…' : '保存' }}
          </button>
        </div>
      </section>

      <section id="settings-cost" class="settings-section">
        <header class="settings-head">
          <h2>成本与预算</h2>
          <p class="muted small">
            价格用于批次结束后的花费统计；不填则只记 token 数，不谎报为 0 成本。
          </p>
        </header>
        <div class="settings-fields">
          <label>
            <span>价格（美元 / 千 token，输入,输出）</span>
            <input v-model="form['llm.price_per_1k']" class="mono" placeholder="0.0005,0.0015" />
          </label>
          <label>
            <span>每日预算上限（美元）</span>
            <input v-model="form['llm.daily_budget_usd']" type="number" min="0" step="0.01" placeholder="留空不限制" />
            <small class="field-hint">预算配置项；当前网站不开放实时 AI 请求，此值尚未作为运行时预算闸门执行。</small>
          </label>
        </div>
        <div class="settings-actions">
          <button class="primary" :disabled="saving || loading" @click="save('cost')">
            {{ saving ? '保存中…' : '保存' }}
          </button>
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
              <tr><td>全量 27,449 题</td><td class="mono">约 30 小时有效跑批，限流与低并发会增加耗时</td></tr>
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
  grid-template-columns: 200px minmax(0, 1fr);
  gap: var(--space-8);
  max-width: 1180px;
  margin: 0 auto;
  align-items: start;
}

.settings-nav {
  position: sticky;
  top: var(--space-6);
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}
.settings-nav a {
  text-decoration: none;
  color: var(--color-text-muted);
  font-size: var(--font-size-label);
  padding: var(--space-2) var(--space-3);
  border-left: 3px solid transparent;
  border-radius: 0 var(--radius-small) var(--radius-small) 0;
  white-space: nowrap;
}
.settings-nav a:hover {
  color: var(--color-text);
  background: var(--color-nav-hover);
}
.settings-nav a.active {
  color: var(--color-nav-active-text);
  background: var(--color-nav-active);
  border-left-color: var(--color-primary);
  font-weight: 600;
}

.settings-section {
  scroll-margin-top: var(--space-6);
}
.settings-section + .settings-section {
  margin-top: var(--space-6);
  padding-top: var(--space-8);
  border-top: var(--border-width) solid var(--color-border);
}
.settings-head { margin-bottom: var(--space-5); }
.settings-head p { margin-bottom: 0; }

.settings-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4) var(--space-6);
}
.settings-actions {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-6);
}

.settings-sub { margin-top: var(--space-6); padding: var(--space-4) var(--space-5); }
.settings-sub h3 { margin-bottom: var(--space-3); }
.settings-inline {
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
}
.settings-inline input { flex: 1; min-width: 200px; }

.settings-fold {
  border: var(--border-width) solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface-subtle);
  padding: var(--space-3) var(--space-4);
}
.settings-fold + .settings-fold { margin-top: var(--space-4); }
.settings-fold summary {
  margin-bottom: 0;
  font-weight: 600;
  font-size: var(--font-size-label);
  color: var(--color-text-muted);
}
.settings-fold[open] summary { margin-bottom: var(--space-3); color: var(--color-text); }
.settings-fold .data-table { background: var(--color-surface); }
.settings-reference-table td:first-child { color: var(--color-text-muted); width: 42%; }

@media (max-width: 960px) {
  .settings-layout { grid-template-columns: 1fr; gap: var(--space-5); }
  .settings-nav {
    position: static;
    flex-direction: row;
    overflow-x: auto;
    border-bottom: var(--border-width) solid var(--color-border);
    padding-bottom: var(--space-2);
  }
  .settings-nav a {
    border-left: none;
    border-bottom: 3px solid transparent;
    border-radius: var(--radius-small) var(--radius-small) 0 0;
  }
  .settings-nav a.active { border-bottom-color: var(--color-primary); }
}
@media (max-width: 640px) {
  .settings-fields { grid-template-columns: 1fr; }
}
</style>
