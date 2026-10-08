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
  <div class="settings-grid">
    <section class="card">
      <h2>LLM 接口</h2>
      <p class="muted small">模型接口参数保存在服务器，密钥读取时只返回掩码。</p>

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

      <div class="settings-row">
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
        <button class="primary" :disabled="saving || loading" @click="save">
          {{ saving ? '保存中…' : '保存' }}
        </button>
        <button class="ghost" :disabled="testing || loading" @click="testConn">
          {{ testing ? '测试中…' : '测试连接' }}
        </button>
      </div>

      <p v-if="notice" :class="['notice', 'settings-feedback', notice.kind]">{{ notice.text }}</p>

      <div v-if="testResult" :class="['notice', 'settings-feedback', testResult.ok ? 'ok' : 'err']">
        <template v-if="testResult.ok">
          连接成功，耗时 {{ testResult.latency_ms }} ms<br />
          <span class="mono">配置模型 {{ testResult.model_config }}</span><br />
          <span class="mono">回执模型 {{ testResult.model_response || '(回执未给出)' }}</span>
          <div v-if="testResult.warning" class="settings-warning">{{ testResult.warning }}</div>
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
        <small class="field-hint">预算配置项；当前网站不开放实时 AI 请求，此值尚未作为运行时预算闸门执行。</small>
      </label>

      <hr class="section-divider" />
      <h3>成本估算参考</h3>
      <table class="data-table settings-reference-table">
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
      <table class="data-table settings-reference-table">
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
.settings-grid {
  display: grid;
  gap: 18px;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 340px), 1fr));
  max-width: 1180px;
  margin: 0 auto;
}
.settings-grid label { margin-top: 16px; }
.settings-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr));
  gap: 14px;
}
.settings-actions {
  display: flex;
  gap: 10px;
  margin-top: 20px;
}
.settings-feedback { margin-top: 16px; }
.settings-warning { color: var(--warn); margin-top: 6px; }
.settings-reference-table td:first-child { color: var(--muted); width: 46%; }
</style>
