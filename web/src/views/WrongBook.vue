<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { forUser } from '../api'
import { auth } from '../auth'
import { toast } from '../toast'
import ActionMenu from '../components/ActionMenu.vue'
import OverlayPanel from '../components/OverlayPanel.vue'
import Pagination from '../components/Pagination.vue'
import QuestionRows from '../components/QuestionRows.vue'

const route = useRoute()
const router = useRouter()
const api = forUser(auth.user?.id)
const tabs = [['pending', '待订正'], ['resolved', '已订正'], ['all', '全部']]
const queryStatus = route.query.status
const status = tabs.some(([key]) => key === queryStatus) ? queryStatus : 'pending'
const currentTab = tabs.find(([key]) => key === status)[1]
const page = Math.max(1, Math.min(100000, Math.trunc(Number(route.query.page) || 1)))
const size = 20
const data = ref(null)
const pendingCount = ref(null)
const busy = ref(true)
const starting = ref(false)
const changing = ref(false)
const navigating = ref(false)
const loadError = ref('')
const actionError = ref('')
const confirmationError = ref('')
const removal = ref(null)
const savedScroll = window.history.state?.scroll
let alive = true

function location(nextStatus = status, nextPage = 1) {
  const query = {}
  if (nextStatus !== 'pending') query.status = nextStatus
  if (nextPage > 1) query.page = nextPage
  return { path: '/wrongbook', query }
}

async function navigate(target, replace = false) {
  if (navigating.value) return
  navigating.value = true
  try {
    await router[replace ? 'replace' : 'push'](target)
  } finally {
    navigating.value = false
  }
}

async function load() {
  busy.value = true
  loadError.value = ''
  try {
    const list = api.wrongbook({ page, size, status })
    const pending = status === 'pending' ? list : api.wrongbook({ page: 1, size: 1, status: 'pending' })
    const [result, summary] = await Promise.all([list, pending])
    if (!alive) return
    pendingCount.value = summary.total
    const last = Math.max(1, Math.ceil(result.total / size))
    if (page > last) await navigate(location(status, last), true)
    else data.value = result
  } catch (e) {
    loadError.value = e.message
  } finally {
    busy.value = false
  }
}

async function start(id) {
  if (starting.value || changing.value || navigating.value) return
  starting.value = true
  actionError.value = ''
  try {
    const spec = id ? { question_ids: [id] } : { limit: 20 }
    const session = await api.createSession('wrongbook', spec)
    if (alive) await router.push({ path: '/practice', query: { session: session.session_id } })
  } catch (e) {
    actionError.value = e.message
  } finally {
    starting.value = false
  }
}

function requestRemoval(question) {
  confirmationError.value = ''
  removal.value = question
}
function closeRemoval() {
  if (!changing.value) removal.value = null
}
async function change(id, action) {
  if (changing.value || starting.value || navigating.value || busy.value || loadError.value) return
  changing.value = true
  actionError.value = ''
  confirmationError.value = ''
  try {
    await api.wrongAction(id, action)
    if (!alive) return
    toast.ok({ favorite: '已加入收藏', resolve: '已标记为已订正', unresolve: '已恢复为待订正', delete: '已从错题本移除' }[action])
    if (action === 'delete') removal.value = null
    if (action !== 'favorite') await load()
  } catch (e) {
    if (action === 'delete') confirmationError.value = e.message
    else actionError.value = e.message
  } finally {
    changing.value = false
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
      <h1>错题本</h1>
      <p v-if="pendingCount !== null" class="muted">{{ pendingCount.toLocaleString() }} 道待订正</p>
    </div>
    <button class="primary" :disabled="busy || navigating || starting || changing || !!loadError || !pendingCount" @click="start()">
      {{ starting ? '正在组题…' : '重练待订正题' }}
    </button>
  </header>
  <div class="tabs review-tabs" role="group" aria-label="错题状态">
    <button v-for="[key, name] in tabs" :key="key" :aria-pressed="status === key" :class="{ active: status === key }"
      :disabled="changing || starting || navigating" @click="navigate(location(key))">
      {{ name }}<template v-if="key === 'pending' && pendingCount !== null"> {{ pendingCount }}</template>
    </button>
  </div>
  <p v-if="actionError" class="notice err" role="alert">操作失败。{{ actionError }}</p>

  <section class="panel" :aria-busy="busy || navigating">
    <div class="panel-heading">
      <h2>{{ currentTab }}错题 <span v-if="data" class="count">{{ data.total }}</span></h2>
    </div>
    <p v-if="busy && !data" class="empty" role="status">正在加载错题…</p>
    <div v-else-if="loadError" class="empty" role="alert">
      <p>错题加载失败。{{ loadError }}</p>
      <button @click="load">重试</button>
    </div>
    <QuestionRows v-else-if="data?.items.length" :items="data.items" practice-label="重新练习" :busy="busy || starting || changing || navigating" @practice="start">
      <template #meta="{ question }">
        <span>答错 {{ question.wrong_count }} 次</span>
        <span :class="['status-badge', question.resolved ? 'resolved' : 'pending']">{{ question.resolved ? '已订正' : '待订正' }}</span>
      </template>
      <template #actions="{ question }">
        <button class="text-button" :disabled="busy || starting || changing || navigating" @click="change(question.id, 'favorite')">收藏</button>
        <ActionMenu :label="'题目 ' + question.id + ' 的更多操作'" :disabled="busy || starting || changing || navigating">
          <button role="menuitem" type="button" @click="change(question.id, question.resolved ? 'unresolve' : 'resolve')">{{ question.resolved ? '取消订正' : '标记已订正' }}</button>
          <button role="menuitem" type="button" class="danger" @click="requestRemoval(question)">移除</button>
        </ActionMenu>
      </template>
    </QuestionRows>
    <div v-else class="empty">
      <h3>{{ status === 'pending' ? '暂无待订正错题' : status === 'resolved' ? '暂无已订正错题' : '暂无错题记录' }}</h3>
      <RouterLink to="/questions">去题库练习</RouterLink>
    </div>
    <Pagination v-if="data?.total && !loadError" :page="page" :size="size" :total="data.total"
      :busy="busy || changing || starting || navigating" @change="navigate(location(status, $event))" />
  </section>

  <OverlayPanel :open="!!removal" title="移除错题" @close="closeRemoval">
    <p v-if="removal">确定将第 {{ removal.id }} 号题移出错题本？原作答记录和收藏会保留。</p>
    <p v-if="confirmationError" class="notice err" role="alert">移除失败。{{ confirmationError }}</p>
    <div class="dialog-actions">
      <button :disabled="changing" @click="closeRemoval">取消</button>
      <button class="danger" :disabled="changing || !removal" @click="change(removal.id, 'delete')">{{ changing ? '正在移除…' : '移除错题' }}</button>
    </div>
  </OverlayPanel>
</template>
