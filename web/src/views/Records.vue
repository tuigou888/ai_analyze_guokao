<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api'
import Pagination from '../components/Pagination.vue'

const route = useRoute()
const router = useRouter()
const size = 20
const parsedPage = Number(route.query.page)
const page = Number.isSafeInteger(parsedPage) && parsedPage > 0 ? parsedPage : 1
const stats = ref(null)
const data = ref(null)
const busy = ref(true)
const navigating = ref(false)
const statsError = ref('')
const recordsError = ref('')
const savedScroll = window.history.state?.scroll
let alive = true
const kinds = { single: '专项练习', paperset: '真题试卷', concept: '考点练习', wrongbook: '错题复练', favorite: '收藏练习' }
const date = value => new Date(value).toLocaleString('zh-CN', { hour12: false })

async function changePage(nextPage) {
  if (navigating.value) return
  navigating.value = true
  try {
    await router.push({ path: '/records', query: nextPage > 1 ? { page: nextPage } : {} })
  } finally {
    navigating.value = false
  }
}

async function load() {
  busy.value = true
  statsError.value = ''
  recordsError.value = ''
  try {
    const [overview, records] = await Promise.allSettled([api.stats(), api.records({ limit: size, offset: (page - 1) * size })])
    if (overview.status === 'fulfilled') stats.value = overview.value
    else statsError.value = overview.reason.message
    if (records.status === 'fulfilled') data.value = records.value
    else recordsError.value = records.reason.message
  } finally {
    busy.value = false
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
      <h1>学习记录</h1>
      <p class="muted">继续未完成的练习，或查看已提交的复盘。</p>
    </div>
    <RouterLink class="button primary" to="/questions">开始新练习</RouterLink>
  </header>

  <section class="records-overview" aria-labelledby="history-heading" :aria-busy="busy">
    <header class="list-section-heading">
      <h2 id="history-heading">历史累计</h2>
      <RouterLink class="records-favorites" to="/favorites">我的收藏<template v-if="stats && !statsError"> {{ stats.favorites || 0 }} 题</template></RouterLink>
    </header>
    <div v-if="statsError" class="panel empty" role="alert">
      <p>累计数据加载失败。{{ statsError }}</p>
      <button :disabled="busy" @click="load">重试</button>
    </div>
    <p v-else-if="!stats" class="panel empty" role="status">正在加载累计数据…</p>
    <div v-else class="stats-grid">
      <article><span>累计作答</span><strong>{{ stats.answered }} <small>次</small></strong></article>
      <article><span>作答正确率</span><strong>{{ stats.answered ? Math.round(stats.correct / stats.answered * 100) : 0 }}<small>%</small></strong></article>
      <article><span>待订正题</span><strong>{{ stats.wrong }} <small>题</small></strong></article>
      <article><span>完成练习</span><strong>{{ stats.sessions }} <small>份</small></strong></article>
    </div>
  </section>

  <section class="panel" aria-labelledby="practice-history-heading" :aria-busy="busy || navigating">
    <div class="panel-heading"><h2 id="practice-history-heading">练习记录</h2></div>
    <p v-if="busy && !data" class="empty" role="status">正在加载练习记录…</p>
    <div v-else-if="recordsError" class="empty" role="alert">
      <p>练习记录加载失败。{{ recordsError }}</p>
      <button :disabled="busy" @click="load">重试</button>
    </div>
    <div v-else-if="data?.items.length" class="record-list">
      <article v-for="record in data.items" :key="record.session_id">
        <div>
          <div class="record-heading">
            <h3>{{ kinds[record.kind] || '练习' }}</h3>
            <span :class="['status-badge', { resolved: record.submitted_at }]">{{ record.submitted_at ? '已完成' : '进行中' }}</span>
          </div>
          <p class="muted">
            <time v-if="record.started_at" :datetime="record.started_at">{{ date(record.started_at) }} 开始<span aria-hidden="true"> · </span></time>
            共 {{ record.total }} 题<template v-if="record.submitted_at"><span aria-hidden="true"> · </span>答对 {{ record.correct }} 题<span aria-hidden="true"> · </span>{{ Math.round(record.duration_ms / 60000) }} 分钟</template>
          </p>
        </div>
        <RouterLink class="button" :to="{ path: '/practice', query: { session: record.session_id } }">{{ record.submitted_at ? '查看复盘' : '继续练习' }}</RouterLink>
      </article>
    </div>
    <div v-else class="empty">
      <h3>{{ data?.total ? '此页没有练习记录' : '暂无练习记录' }}</h3>
      <button v-if="data?.total" @click="changePage(1)">返回第一页</button>
      <RouterLink v-else to="/questions">去题库练习</RouterLink>
    </div>
    <Pagination v-if="data?.total && !recordsError" :page="page" :size="size" :total="data.total" :busy="busy || navigating" @change="changePage" />
  </section>
</template>
