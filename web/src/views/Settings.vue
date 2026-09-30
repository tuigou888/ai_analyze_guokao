<script setup>
import { ref, reactive, onMounted, computed } from 'vue'
import { api } from '../api'

const MASK = '••••••••'

const form = reactive({
  'llm.base_url': '',
  'llm.api_key': '',
  'llm.model': '',
  'llm.concurrency': '1',
  'llm.timeout_sec': '120',
  'llm.daily_budget_usd': '',
  'llm.price_per_1k': '',
})

const loading = ref(true)
const saving = ref(false)
const testing = ref(false)
const notice = ref(null) // { kind: 'ok'|'err'|'warn', text }
const testResult = ref(null)
const keyIsSet = ref(false)

// 后端把已保存的密钥以掩码返回。提交时必须原样送回，
// 后端会用「等于掩码就保持原值」的规则避免把它写成字面量掩码。
const keyPlaceholder = computed(() => (keyIsSet.value ? '已保存（留空或保持不变即可）' : '尚未设置'))

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
      form[k] = v
    }
  } catch (e) {
    notice.value = { kind: 'err', text: '读取配置失败：' + e.message }
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function save() {
  saving.value = true
  notice.value = null
  try {
    const payload = { ...form }
    // 密钥仍是掩码时不要发出去，让后端保持原值。
    if (payload['llm.api_key'] === MASK) delete payload['llm.api_key']
    await api.saveSettings(payload)
    notice.value = { kind: 'ok', text: '已保存' }
    await load()
  } catch (e) {
    notice.value = { kind: 'err', text: '保存失败：' + e.message }
  } finally {
    saving.value = false
  }
}

async function testConn() {
  testing.value = true
  testResult.value = null
  try {
    testResult.value = { ok: true, ...(await api.testLLM()) }
  } catch (e) {
    testResult.value = { ok: false, error: e.message }
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <div class="grid">
    <section class="card">
      <h2>LLM 接口</h2>
      <p class="muted small">模型接口参数保存在服务器，密钥读取时只返回掩码。</p>

      <label>
        <span>API 地址（OpenAI 兼容）</span>
        <input v-model="form['llm.base_url']" class="mono" placeholder="https://example.com/v1" />
        <em>填到 /v1 为止，客户端会自动拼 /chat/completions</em>
      </label>

      <label>
        <span>API Key</span>
        <input v-model="form['llm.api_key']" type="password" class="mono"
               :placeholder="keyPlaceholder" autocomplete="off" />
        <em>留空表示不修改；填入新值会覆盖</em>
      </label>

      <label>
        <span>模型名</span>
        <input v-model="form['llm.model']" class="mono" placeholder="deepseek-flash" />
      </label>

      <div class="row">
        <label>
          <span>并发数</span>
          <input v-model="form['llm.concurrency']" type="number" min="1" max="32" />
          <em>2 核 4GB 服务器建议从 1 开始</em>
        </label>
        <label>
          <span>单请求超时（秒）</span>
          <input v-model="form['llm.timeout_sec']" type="number" min="10" />
          <em>生成类请求较慢</em>
        </label>
      </div>

      <div class="actions">
        <button class="primary" :disabled="saving || loading" @click="save">
          {{ saving ? '保存中…' : '保存' }}
        </button>
        <button class="ghost" :disabled="testing || loading" @click="testConn">
          {{ testing ? '测试中…' : '测试连接' }}
        </button>
      </div>

      <p v-if="notice" :class="['notice', notice.kind]">{{ notice.text }}</p>

      <div v-if="testResult" :class="['notice', testResult.ok ? 'ok' : 'err']">
        <template v-if="testResult.ok">
          连接成功，耗时 {{ testResult.latency_ms }} ms<br />
          <span class="mono">配置模型 {{ testResult.model_config }}</span><br />
          <span class="mono">回执模型 {{ testResult.model_response || '(回执未给出)' }}</span>
          <div v-if="testResult.warning" class="warn-line">{{ testResult.warning }}</div>
        </template>
        <template v-else>{{ testResult.error }}</template>
      </div>
    </section>

    <section class="card">
      <h2>成本与闸门</h2>
      <p class="muted small">
        价格用于批次结束后的花费统计；不填则只记 token 数，不谎报为 0 成本。
      </p>

      <label>
        <span>价格（美元 / 千 token，输入,输出）</span>
        <input v-model="form['llm.price_per_1k']" class="mono" placeholder="0.0005,0.0015" />
      </label>
      <label>
        <span>每日预算上限（美元）</span>
        <input v-model="form['llm.daily_budget_usd']" type="number" min="0" step="0.01" placeholder="留空不限制" />
        <em>预算配置项；当前网站不开放实时 AI 请求，此值尚未作为运行时预算闸门执行。</em>
      </label>

      <hr />
      <h3>成本估算参考</h3>
      <table>
        <tbody>
          <tr><td>单题标注</td><td class="mono">约 3.3k token（含重试，以批次统计为准）</td></tr>
          <tr><td>全量 27,449 题</td><td class="mono">约 30 小时有效跑批，限流与低并发会增加耗时</td></tr>
          <tr><td>MVP 50 题</td><td class="mono">按实际端点价格与 token 用量计算</td></tr>
        </tbody>
      </table>
    </section>

    <section class="card">
      <h2>接口约定</h2>
      <p class="muted small">当前后端支持的配置键（也可用 <code>gk config set</code> 修改）：</p>
      <table>
        <tbody>
          <tr><td class="mono">llm.base_url</td><td>OpenAI 兼容地址，含 /v1</td></tr>
          <tr><td class="mono">llm.api_key</td><td>密文存储</td></tr>
          <tr><td class="mono">llm.model</td><td>请求用的模型名</td></tr>
          <tr><td class="mono">llm.concurrency</td><td>批处理并发数</td></tr>
          <tr><td class="mono">llm.timeout_sec</td><td>单请求超时</td></tr>
          <tr><td class="mono">llm.price_per_1k</td><td>"输入,输出" 美元/千 token</td></tr>
          <tr><td class="mono">llm.daily_budget_usd</td><td>每日成本上限</td></tr>
        </tbody>
      </table>
    </section>
  </div>
</template>

<style scoped>
.grid {
  display: grid; gap: 18px;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 340px), 1fr));
  max-width: 1180px; margin: 0 auto;
}
.card { background: var(--panel); border: 1px solid var(--line); border-radius: 12px; padding: 22px; }
h2 { margin: 0 0 6px; font-size: 16px; }
h3 { margin: 0 0 8px; font-size: 14px; }
.small { font-size: 12.5px; }
.muted { color: var(--muted); }
hr { border: none; border-top: 1px solid var(--line); margin: 20px 0; }
label { display: block; margin-top: 16px; }
label span { display: block; font-size: 13px; margin-bottom: 6px; }
label em { display: block; font-size: 12px; color: var(--muted); margin-top: 5px; font-style: normal; }
input {
  width: 100%; padding: 9px 12px; background: var(--panel-2); color: var(--text);
  border: 1px solid var(--line); border-radius: 7px; font-size: 14px;
}
input:focus { outline: none; border-color: var(--accent); }
.row { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.actions { display: flex; gap: 10px; margin-top: 20px; }
button {
  padding: 9px 18px; border-radius: 7px; font-size: 14px; cursor: pointer; border: 1px solid transparent;
}
button.primary { background: var(--accent); color: #fff; }
button.ghost { background: transparent; color: var(--text); border-color: var(--line); }
button:disabled { opacity: .5; cursor: not-allowed; }
.notice { margin-top: 16px; padding: 11px 13px; border-radius: 7px; font-size: 13px; line-height: 1.7; }
.notice.ok { background: rgba(53, 201, 138, .12); border: 1px solid rgba(53, 201, 138, .35); }
.notice.err { background: rgba(242, 84, 91, .12); border: 1px solid rgba(242, 84, 91, .35); }
.notice.warn { background: rgba(240, 180, 41, .12); border: 1px solid rgba(240, 180, 41, .35); }
.warn-line { color: var(--warn); margin-top: 6px; }
table { width: 100%; border-collapse: collapse; font-size: 13px; }
td { padding: 7px 0; border-bottom: 1px solid var(--line); vertical-align: top; }
td:first-child { color: var(--muted); width: 46%; }
code { background: var(--panel-2); padding: 2px 6px; border-radius: 4px; font-family: var(--mono); font-size: 12px; }
</style>
