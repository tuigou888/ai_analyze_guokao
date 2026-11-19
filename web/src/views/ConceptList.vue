<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api'

const route = useRoute()
const router = useRouter()
const desktop = window.matchMedia('(min-width: 900px)')
const directory = ref(null)
const items = ref([])
const busy = ref(true)
const error = ref('')
const stringQuery = key => typeof route.query[key] === 'string' ? route.query[key] : ''
const selectedModule = computed(() => stringQuery('module'))
const selectedSecondary = computed(() => stringQuery('secondary'))
const keyword = computed(() => stringQuery('q'))
const draft = ref(keyword.value)
const savedScroll = window.history.state?.scroll
let alive = true

const directoryGroups = computed(() => {
  const modules = new Map()
  for (const item of items.value) {
    if (!modules.has(item.module)) modules.set(item.module, new Map())
    const sections = modules.get(item.module)
    if (!sections.has(item.secondary)) sections.set(item.secondary, [])
    sections.get(item.secondary).push(item)
  }
  return modules
})

const groups = computed(() => {
  const result = []
  for (const [module, sections] of directoryGroups.value) {
    if (selectedModule.value && module !== selectedModule.value) continue
    for (const [secondary, concepts] of sections) {
      if (selectedSecondary.value && secondary !== selectedSecondary.value) continue
      const matches = concepts.filter(concept => concept.name.includes(keyword.value))
      if (matches.length) result.push({ module, secondary, concepts: matches })
    }
  }
  return result
})
const matchCount = computed(() => groups.value.reduce((total, group) => total + group.concepts.length, 0))
const heading = computed(() => [selectedModule.value, selectedSecondary.value].filter(Boolean).join(' / ') || '全部考点')

function selection(module = '', secondary = '', q = keyword.value) {
  return {
    ...(module && { module }),
    ...(secondary && { secondary }),
    ...(q && { q }),
  }
}
function link(module, secondary) {
  return { path: '/concepts', query: selection(module, secondary) }
}
function search(value = draft.value) {
  draft.value = value.trim()
  return router.push({ path: '/concepts', query: selection(selectedModule.value, selectedSecondary.value, draft.value) })
}
function syncDirectory() {
  if (directory.value) directory.value.open = desktop.matches
}
async function load() {
  busy.value = true
  error.value = ''
  try {
    items.value = await api.concepts({})
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}

watch(keyword, value => {
  if (route.path === '/concepts') draft.value = value
})
watch([selectedModule, selectedSecondary], () => {
  if (!desktop.matches && route.path === '/concepts') syncDirectory()
})
onMounted(async () => {
  desktop.addEventListener('change', syncDirectory)
  await load()
  await nextTick()
  if (alive && savedScroll) window.scrollTo(savedScroll)
})
onBeforeUnmount(() => {
  alive = false
  desktop.removeEventListener('change', syncDirectory)
})
</script>

<template>
  <header class="page-heading">
    <div>
      <h1>考点地图</h1>
      <p class="muted">
        <template v-if="!busy && !error">{{ items.length }} 个已归纳考点 <span aria-hidden="true">·</span> </template>仅展示已有标注覆盖的考点
      </p>
    </div>
  </header>
  <p v-if="busy" class="panel empty" role="status">正在加载考点…</p>
  <div v-else-if="error" class="panel empty" role="alert">
    <p>考点加载失败。{{ error }}</p>
    <button @click="load">重试</button>
  </div>
  <div v-else class="concept-map">
    <details ref="directory" class="panel concept-directory" :open="desktop.matches">
      <summary>知识目录 <span>{{ selectedModule || '全部模块' }}</span></summary>
      <nav aria-label="知识目录">
        <h2>知识目录</h2>
        <RouterLink :to="link()" :aria-current="!selectedModule && !selectedSecondary ? 'page' : undefined">全部考点</RouterLink>
        <section v-for="[module, sections] of directoryGroups" :key="module">
          <RouterLink :to="link(module)" :aria-current="selectedModule === module && !selectedSecondary ? 'page' : undefined">{{ module }}</RouterLink>
          <ul>
            <li v-for="[secondary, concepts] of sections" :key="secondary">
              <RouterLink v-if="secondary" :to="link(module, secondary)" :aria-current="selectedModule === module && selectedSecondary === secondary ? 'page' : undefined">
                {{ secondary }} <small>{{ concepts.length }}</small>
              </RouterLink>
            </li>
          </ul>
        </section>
      </nav>
    </details>

    <section class="panel concept-results" aria-labelledby="concept-results-heading">
      <div class="panel-heading">
        <h2 id="concept-results-heading">{{ heading }}</h2>
        <span class="muted small">{{ matchCount }} 个考点</span>
      </div>
      <form class="concept-search" @submit.prevent="search()">
        <label class="sr-only" for="concept-keyword">考点名称</label>
        <input id="concept-keyword" v-model="draft" type="search" placeholder="搜索考点名称" maxlength="120" />
        <button type="submit">搜索</button>
        <button v-if="keyword" type="button" class="text-button" @click="search('')">清除</button>
      </form>
      <section v-for="group in groups" :key="group.module + '/' + group.secondary" class="concept-group">
        <h3 v-if="!selectedSecondary">{{ selectedModule ? group.secondary : [group.module, group.secondary].filter(Boolean).join(' / ') }}</h3>
        <article v-for="concept in group.concepts" :key="concept.id" class="concept-row">
          <div>
            <h4>{{ concept.name.startsWith(concept.secondary + '-') ? concept.name.slice(concept.secondary.length + 1) : concept.name }}</h4>
            <p class="muted small">关联真题 {{ concept.question_count }} 道</p>
            <div v-if="concept.answered" class="mastery">
              <span class="small muted">已答 {{ concept.answered }} 次 <span aria-hidden="true">·</span> 正确率 {{ Math.round(concept.mastery * 100) }}%</span>
              <div class="progress" aria-hidden="true"><span :style="{ width: Math.round(concept.mastery * 100) + '%' }" /></div>
            </div>
          </div>
          <RouterLink class="button" :to="{ path: '/concepts/' + concept.id, query: selection(selectedModule, selectedSecondary) }">查看考点</RouterLink>
        </article>
      </section>
      <div v-if="!matchCount" class="empty">
        <h3>{{ items.length ? '没有找到匹配考点' : '暂无归纳考点' }}</h3>
        <p v-if="items.length">试试更换关键词或分类。</p>
        <RouterLink v-else to="/questions">去题库练习</RouterLink>
      </div>
    </section>
  </div>
</template>
