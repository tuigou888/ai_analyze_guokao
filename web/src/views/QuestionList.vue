<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api'
import { auth } from '../auth'
import Filters from '../components/Filters.vue'
import OverlayPanel from '../components/OverlayPanel.vue'
import Pagination from '../components/Pagination.vue'
import QuestionRows from '../components/QuestionRows.vue'

const route = useRoute()
const router = useRouter()
const size = 20
const keys = ['module', 'year', 'region', 'exam_type', 'variant', 'q', 'concept_id', 'paper_id', 'order']
const read = key => Array.isArray(route.query[key]) ? route.query[key][0] : route.query[key]
const initial = Object.fromEntries(keys.map(key => [key, read(key) || '']))
if (!Object.hasOwn(route.query, 'module') && !initial.concept_id && !initial.paper_id) {
  initial.module = auth.profile?.default_module || ''
}
const applied = ref(initial)
const draft = ref({ ...initial })
const counts = [10, 20, 50]
const routeCount = Number(read('limit'))
const owner = auth.user?.id
const storedCount = window.history.state?.questionPracticeCount
const historyCount = storedCount?.owner === owner ? storedCount?.value : null
const count = ref([historyCount, routeCount, auth.profile?.default_limit].find(value => counts.includes(value)) || 20)
const page = Math.max(1, Math.min(100000, Math.trunc(Number(read('page')) || 1)))
const data = ref(null)
const busy = ref(true)
const navigating = ref(false)
const starting = ref(false)
const loadError = ref('')
const startError = ref('')
const pendingChange = ref(null)
const savedScroll = window.history.state?.scroll
let alive = true

async function load() {
  busy.value = true
  loadError.value = ''
  try {
    data.value = await api.questions({ ...applied.value, page, size })
  } catch (e) {
    loadError.value = e.message
  } finally {
    busy.value = false
  }
}

async function changePage(nextPage, filters = applied.value) {
  if (busy.value || navigating.value || starting.value) return
  const query = Object.fromEntries(Object.entries(filters).filter(([, value]) => value))
  // An explicit empty module keeps “全部模块” selected over the profile default.
  query.module = filters.module || ''
  if (nextPage > 1) query.page = String(nextPage)
  if (count.value !== (auth.profile?.default_limit || 20)) query.limit = String(count.value)
  navigating.value = true
  try {
    const target = { path: '/questions', query }
    if (router.resolve(target).fullPath === route.fullPath) await load()
    else await router.push(target)
  } finally {
    navigating.value = false
  }
}

function selectCount(event) {
  count.value = Number(event.target.value)
  // Preserve the selection on this history entry without replacing the mounted editor.
  window.history.replaceState({
    ...window.history.state,
    questionPracticeCount: { owner, value: count.value },
  }, '')
}

function hasDraft(except) {
  return keys.some(field => field !== except && String(draft.value[field] || '') !== String(applied.value[field] || ''))
}

function requestPage(nextPage) {
  if (nextPage !== page && hasDraft()) pendingChange.value = { page: nextPage }
  else changePage(nextPage)
}

function requestClearScope(key) {
  if (hasDraft(key)) pendingChange.value = { scope: key }
  else clearScope(key)
}

function clearScope(key) {
  const filters = { ...applied.value, [key]: '' }
  draft.value = { ...filters }
  return changePage(1, filters)
}

function confirmChange() {
  const change = pendingChange.value
  if (!change) return
  pendingChange.value = null
  if (change.scope) clearScope(change.scope)
  else changePage(change.page)
}

async function start(id) {
  if (starting.value || navigating.value) return
  starting.value = true
  startError.value = ''
  try {
    const spec = id ? { question_ids: [id] } : { ...applied.value, limit: count.value }
    const session = await api.createSession('single', spec)
    if (alive) await router.push({ path: '/practice', query: { session: session.session_id } })
  } catch (e) {
    startError.value = e.message
  } finally {
    starting.value = false
  }
}

onMounted(async () => {
  await load()
  await nextTick()
  if (alive && savedScroll) window.scrollTo(savedScroll)
})
onBeforeUnmount(() => { alive = false })
</script>

<template>
  <header class="page-heading">
    <div>
      <h1>题库练习</h1>
      <p v-if="data" class="muted">{{ data.total.toLocaleString() }} 道真题</p>
    </div>
    <div class="session-controls">
      <label>本次题量
        <select :value="count" :disabled="starting || navigating" @change="selectCount">
          <option v-for="n in counts" :key="n" :value="n">{{ n }} 题</option>
        </select>
      </label>
      <button class="primary" :disabled="starting || busy || navigating || !!loadError || !data?.total" @click="start()">
        {{ starting ? '正在组题…' : '开始专项练习' }}
      </button>
    </div>
  </header>

  <Filters v-model="draft" :applied="applied" :busy="busy || navigating || starting" search @apply="changePage(1, draft)" />

  <div v-if="applied.concept_id || applied.paper_id" class="question-scope" aria-label="关联题目范围">
    <button v-if="applied.concept_id" class="filter-chip" :disabled="busy || navigating || starting" aria-label="清除关联考点范围" @click="requestClearScope('concept_id')">所选考点真题 <span aria-hidden="true">×</span></button>
    <button v-if="applied.paper_id" class="filter-chip" :disabled="busy || navigating || starting" aria-label="清除关联试卷范围" @click="requestClearScope('paper_id')">所选试卷真题 <span aria-hidden="true">×</span></button>
  </div>

  <OverlayPanel :open="!!pendingChange" :title="pendingChange?.scope ? '清除关联范围' : '确认翻页'" @close="pendingChange = null">
    <p>{{ pendingChange?.scope ? '清除关联范围' : '翻页' }}会舍弃尚未应用的筛选修改。</p>
    <div class="dialog-actions">
      <button @click="pendingChange = null">保留草稿</button>
      <button class="primary" @click="confirmChange">{{ pendingChange?.scope ? '清除范围' : '继续翻页' }}</button>
    </div>
  </OverlayPanel>

  <p v-if="startError" class="notice err" role="alert">{{ startError }}</p>
  <section class="panel" :aria-busy="busy || navigating">
    <div class="panel-heading">
      <h2>真题列表</h2>
      <span class="muted small">{{ applied.module || '全部模块' }} <span aria-hidden="true">·</span> 每页 {{ size }} 题</span>
    </div>
    <p v-if="busy" class="empty" role="status">正在查找题目…</p>
    <div v-else-if="loadError" class="empty" role="alert">
      <p>题目加载失败。{{ loadError }}</p>
      <button @click="load">重试</button>
    </div>
    <QuestionRows v-else-if="data.items.length" :items="data.items" :busy="starting" @practice="start" />
    <div v-else class="empty">
      <h3>{{ data.total ? '此页没有题目' : '没有找到对应题目' }}</h3>
      <p>{{ data.total ? '返回第一页继续浏览。' : '试试缩短关键词，或减少筛选条件。' }}</p>
      <button v-if="data.total" @click="requestPage(1)">返回第一页</button>
    </div>
    <Pagination v-if="!busy && !loadError && data.total" :page="page" :total="data.total" :size="size" :busy="navigating || starting" @change="requestPage" />
  </section>
</template>
