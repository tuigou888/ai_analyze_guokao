<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { forUser } from '../api'
import { auth } from '../auth'
import { paperAction, ownsPaperProgress } from '../papers/progress'
import ActionMenu from '../components/ActionMenu.vue'
import Filters from '../components/Filters.vue'
import Pagination from '../components/Pagination.vue'

const owner = auth.user?.id
const api = forUser(owner)
const route = useRoute()
const router = useRouter()
const size = 12
const keys = ['module', 'year', 'region', 'exam_type', 'variant']
const read = key => Array.isArray(route.query[key]) ? route.query[key][0] : route.query[key]
const applied = computed(() => Object.fromEntries(keys.map(key => [key, read(key) || ''])))
const draft = ref({ ...applied.value })
const page = computed(() => Math.max(1, Math.min(100000, Math.trunc(Number(read('page')) || 1))))
const items = ref([])
const visible = computed(() => items.value.slice((page.value - 1) * size, page.value * size))
const busy = ref(true)
const navigating = ref(false)
const starting = ref(null)
const loadError = ref('')
const startError = ref('')
const progress = ref({})
const progressBusy = ref(false)
const progressError = ref('')
let progressGeneration = 0
const ownsPage = () => alive && ownsPaperProgress(owner, auth.user?.id, auth.identityChanged)
const currentProgress = computed(() => ownsPage() ? progress.value : {})
const actionFor = paper => paperAction(paper.paper_id, currentProgress.value[paper.paper_id])

async function loadProgress() {
  const generation = ++progressGeneration
  const ids = visible.value.map(paper => paper.paper_id)
  progress.value = {}
  progressError.value = ''
  progressBusy.value = !!ids.length
  if (!ids.length) return
  try {
    const result = await api.paperProgress(ids)
    if (ownsPage() && generation === progressGeneration) progress.value = Object.fromEntries(result.items.map(item => [item.paper_id, item]))
  } catch (error) {
    if (ownsPage() && generation === progressGeneration) progressError.value = error.message
  } finally {
    if (alive && generation === progressGeneration) progressBusy.value = false
  }
}
let alive = true
let loadGeneration = 0

async function load(restoreScroll = false) {
  const generation = ++loadGeneration
  const origin = route.fullPath
  const savedScroll = restoreScroll && window.history.state?.scroll
  busy.value = true
  loadError.value = ''
  try {
    const result = await api.papers(applied.value)
    if (alive && generation === loadGeneration) items.value = result
  } catch (e) {
    if (ownsPage() && generation === loadGeneration) loadError.value = e.message
  } finally {
    if (alive && generation === loadGeneration) busy.value = false
  }
  if (!savedScroll) return
  await nextTick()
  if (alive && generation === loadGeneration && route.fullPath === origin) window.scrollTo(savedScroll)
}

async function changePage(nextPage, filters = applied.value) {
  if (busy.value || navigating.value || starting.value) return
  const query = Object.fromEntries(keys.filter(key => filters[key]).map(key => [key, filters[key]]))
  if (nextPage > 1) query.page = String(nextPage)
  navigating.value = true
  try {
    await router.push({ path: '/papers', query })
  } finally {
    navigating.value = false
  }
}

async function start(paper, fresh = false) {
  if (starting.value || navigating.value || progressBusy.value || progressError.value || !ownsPage()) return
  const action = actionFor(paper)
  if (!fresh && action.sessionId) {
    await router.push({ path: '/practice', query: { session: action.sessionId } })
    return
  }
  if (fresh && action.kind === 'resume' && !window.confirm('将新建一份试卷，现有草稿仍会保存在学习记录中。确定重新组卷吗？')) return
  starting.value = paper.paper_id
  startError.value = ''
  try {
    const session = await api.createSession('paperset', { paper_id: paper.paper_id })
    if (ownsPage()) await router.push({ path: '/practice', query: { session: session.session_id } })
  } catch (e) {
    if (ownsPage()) startError.value = e.message
  } finally {
    starting.value = null
  }
}

watch(keys.map(key => () => applied.value[key]), () => {
  if (route.path !== '/papers') return
  draft.value = { ...applied.value }
  load(true)
})
watch(() => [auth.user?.id, auth.identityChanged], () => {
  if (!ownsPage()) { progressGeneration++; progress.value = {}; progressBusy.value = false; progressError.value = '登录账户已变化，请重新进入试卷页' }
}, { flush: 'sync' })
watch(() => visible.value.map(paper => paper.paper_id).join(','), loadProgress, { flush: 'sync' })
onMounted(() => load(true))
onBeforeUnmount(() => { alive = false; progressGeneration++ })
</script>

<template>
  <header class="page-heading">
    <div>
      <h1>真题试卷</h1>
      <p class="muted">按原卷题序作答，提交后统一判分。</p>
    </div>
  </header>
  <Filters v-model="draft" :applied="applied" :busy="busy || navigating || !!starting" @apply="changePage(1, draft)" />
  <p v-if="startError" class="notice err" role="alert">{{ startError }}</p>
  <p v-if="progressError" class="notice err" role="alert">学习进度加载失败：{{ progressError }} <button :disabled="progressBusy" @click="loadProgress">重试进度</button> <RouterLink to="/records">打开学习记录</RouterLink></p>

  <section class="papers-section" aria-labelledby="paper-list-heading" :aria-busy="busy || navigating || progressBusy">
    <header class="list-section-heading">
      <h2 id="paper-list-heading">试卷列表</h2>
      <span v-if="!busy && !loadError" class="muted small">{{ items.length.toLocaleString() }} 份试卷</span>
    </header>
    <p v-if="busy" class="panel empty" role="status">正在加载试卷…</p>
    <div v-else-if="loadError" class="panel empty" role="alert">
      <p>试卷加载失败。{{ loadError }}</p>
      <button @click="load()">重试</button>
    </div>
    <div v-else-if="visible.length" class="paper-grid">
      <article v-for="paper in visible" :key="paper.paper_id" class="paper-card">
        <div class="paper-topline">
          <div class="paper-meta"><span v-if="paper.year" class="tag">{{ paper.year }}</span><span v-if="paper.region">{{ paper.region }}</span><span v-if="currentProgress[paper.paper_id]" :class="['status-badge', { resolved: actionFor(paper).kind === 'review' }]">{{ actionFor(paper).kind === 'resume' ? '进行中' : '已完成' }}</span></div>
          <ActionMenu v-if="currentProgress[paper.paper_id]" :label="paper.name + '的更多操作'" :disabled="!!starting || navigating || progressBusy || !!progressError || auth.identityChanged"><button role="menuitem" :disabled="!paper.question_count" @click="start(paper, true)">重新组卷</button></ActionMenu>
        </div>
        <h3 :title="paper.name">{{ paper.name }}</h3>
        <p v-if="paper.module || paper.exam_type || paper.variant" class="paper-meta">
          <span v-if="paper.module">{{ paper.module }}</span>
          <span v-if="paper.exam_type">{{ paper.exam_type }}</span>
          <span v-if="paper.variant">{{ paper.variant }}</span>
        </p>
        <footer class="paper-card-footer">
          <div class="paper-progress-copy"><span class="muted small">{{ paper.question_count }} 题</span><span v-if="currentProgress[paper.paper_id]" class="small">{{ actionFor(paper).kind === 'resume' ? '已保存作答' : '已提交作答' }} {{ actionFor(paper).answered }} / {{ actionFor(paper).total }} 题</span><progress v-if="actionFor(paper).kind === 'resume'" :value="actionFor(paper).answered" :max="actionFor(paper).total || 1" :aria-label="paper.name + '已保存作答进度'" /></div>
          <button :disabled="!!starting || navigating || progressBusy || !!progressError || auth.identityChanged || (!paper.question_count && actionFor(paper).kind === 'start')" @click="start(paper)">
            {{ starting === paper.paper_id ? '正在组卷…' : progressBusy ? '读取进度…' : progressError ? '进度不可用' : actionFor(paper).label }}
          </button>
        </footer>
      </article>
    </div>
    <div v-else class="panel empty">
      <h3>{{ items.length ? '此页没有试卷' : '没有找到试卷' }}</h3>
      <button v-if="items.length" @click="changePage(1)">返回第一页</button>
      <p v-else>试试更换地区、年份或其他筛选条件。</p>
    </div>
    <Pagination v-if="!busy && !loadError && items.length" :page="page" :size="size" :total="items.length" :busy="navigating || !!starting" @change="changePage" />
  </section>
</template>
