<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { forUser } from '../api'
import { auth } from '../auth'
import { toast } from '../toast'
import OverlayPanel from '../components/OverlayPanel.vue'
import Pagination from '../components/Pagination.vue'
import QuestionRows from '../components/QuestionRows.vue'

const route = useRoute()
const router = useRouter()
const api = forUser(auth.user?.id)
const page = Math.max(1, Math.min(100000, Math.trunc(Number(route.query.page) || 1)))
const size = 20
const data = ref(null)
const busy = ref(true)
const starting = ref(false)
const changing = ref(false)
const navigating = ref(false)
const loadError = ref('')
const startError = ref('')
const confirmationError = ref('')
const removal = ref(null)
const savedScroll = window.history.state?.scroll
let alive = true

async function changePage(nextPage, replace = false) {
  if (navigating.value) return
  navigating.value = true
  try {
    await router[replace ? 'replace' : 'push']({ path: '/favorites', query: nextPage > 1 ? { page: nextPage } : {} })
  } finally {
    navigating.value = false
  }
}

async function load() {
  busy.value = true
  loadError.value = ''
  try {
    const result = await api.favorites({ page, size })
    if (!alive) return
    const last = Math.max(1, Math.ceil(result.total / size))
    if (page > last) await changePage(last, true)
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
  startError.value = ''
  try {
    const spec = id ? { question_ids: [id] } : { limit: 20 }
    const session = await api.createSession('favorite', spec)
    if (alive) await router.push({ path: '/practice', query: { session: session.session_id } })
  } catch (e) {
    startError.value = e.message
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
async function remove() {
  if (!removal.value || changing.value || starting.value || navigating.value || busy.value || loadError.value) return
  changing.value = true
  confirmationError.value = ''
  try {
    await api.favoriteAction(removal.value.id, 'delete')
    if (!alive) return
    removal.value = null
    toast.ok('已取消收藏')
    await load()
  } catch (e) {
    confirmationError.value = e.message
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
      <h1>我的收藏</h1>
      <p v-if="data" class="muted">{{ data.total.toLocaleString() }} 道已收藏题目</p>
    </div>
    <button class="primary" :disabled="busy || starting || changing || navigating || !!loadError || !data?.total" @click="start()">
      {{ starting ? '正在组题…' : '练习收藏题' }}
    </button>
  </header>
  <p v-if="startError" class="notice err" role="alert">{{ startError }}</p>

  <section class="panel" aria-label="收藏题目" :aria-busy="busy || navigating">
    <p v-if="busy && !data" class="empty" role="status">正在加载收藏…</p>
    <div v-else-if="loadError" class="empty" role="alert">
      <p>收藏加载失败。{{ loadError }}</p>
      <button @click="load">重试</button>
    </div>
    <QuestionRows v-else-if="data?.items.length" :items="data.items" :busy="busy || starting || changing || navigating" @practice="start">
      <template #meta="{ question }">
        <span v-if="question.created_at">收藏于 <time :datetime="question.created_at">{{ question.created_at.slice(0, 10) }}</time></span>
      </template>
      <template #actions="{ question }">
        <button class="text-button" :disabled="busy || starting || changing || navigating" @click="requestRemoval(question)">取消收藏</button>
      </template>
    </QuestionRows>
    <div v-else class="empty">
      <h3>还没有收藏题目</h3>
      <p>可在错题本或答题页收藏题目。</p>
      <RouterLink to="/questions">去题库练习</RouterLink>
    </div>
    <Pagination v-if="data?.total && !loadError" :page="page" :size="size" :total="data.total"
      :busy="busy || changing || starting || navigating" @change="changePage" />
  </section>

  <OverlayPanel :open="!!removal" title="取消收藏" @close="closeRemoval">
    <p v-if="removal">确定取消收藏第 {{ removal.id }} 号题？</p>
    <p v-if="confirmationError" class="notice err" role="alert">取消收藏失败。{{ confirmationError }}</p>
    <div class="dialog-actions">
      <button :disabled="changing" @click="closeRemoval">保留收藏</button>
      <button class="danger" :disabled="changing || !removal" @click="remove">{{ changing ? '正在取消…' : '取消收藏' }}</button>
    </div>
  </OverlayPanel>
</template>
