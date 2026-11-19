<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api'
import RichText from '../components/RichText.vue'
import QuestionRows from '../components/QuestionRows.vue'

const route = useRoute()
const router = useRouter()
const concept = ref(null)
const loadError = ref('')
const startError = ref('')
const busy = ref(true)
const starting = ref(false)
const originQuery = Object.fromEntries(['module', 'secondary', 'q']
  .filter(key => typeof route.query[key] === 'string' && route.query[key])
  .map(key => [key, route.query[key]]))
const back = { path: '/concepts', query: originQuery }
const fields = [
  ['question_model', '问法模型'],
  ['template', '母题抽象'],
  ['key_features', '识别特征'],
  ['boundary', '适用边界'],
  ['typical_ask', '典型提问'],
]
const visibleFields = computed(() => fields.filter(([key]) => concept.value?.card?.[key]?.length))
let alive = true

async function start(id) {
  if (starting.value) return
  starting.value = true
  startError.value = ''
  try {
    const spec = id ? { question_ids: [id] } : { concept_id: concept.value.id, limit: 20 }
    const session = await api.createSession(id ? 'single' : 'concept', spec)
    if (alive) await router.push({ path: '/practice', query: { session: session.session_id } })
  } catch (e) {
    startError.value = e.message
  } finally {
    starting.value = false
  }
}
async function load() {
  busy.value = true
  loadError.value = ''
  try {
    concept.value = await api.concept(route.params.id)
  } catch (e) {
    loadError.value = e.message
  } finally {
    busy.value = false
  }
}
onMounted(load)
onBeforeUnmount(() => { alive = false })
</script>

<template>
  <RouterLink class="back-link" :to="back">← 返回考点地图</RouterLink>
  <p v-if="busy" class="panel empty" role="status">正在加载考点…</p>
  <div v-else-if="loadError" class="panel empty" role="alert">
    <p>考点加载失败。{{ loadError }}</p>
    <button @click="load">重试</button>
  </div>
  <template v-else-if="concept">
    <header class="page-heading">
      <div>
        <h1>{{ concept.name }}</h1>
        <p class="muted">{{ [concept.module, concept.secondary].filter(Boolean).join(' / ') }} <span aria-hidden="true">·</span> 关联真题 {{ concept.question_count }} 道</p>
      </div>
      <button class="primary" :disabled="starting || !concept.question_count" @click="start()">{{ starting ? '正在组题…' : '围绕考点练习' }}</button>
    </header>
    <p v-if="startError" class="notice err" role="alert">{{ startError }}</p>

    <section class="panel concept-notes">
      <div class="panel-heading"><h2>考点归纳</h2></div>
      <div v-if="visibleFields.length" class="concept-detail-grid">
        <section v-for="[key, title] in visibleFields" :key="key" class="concept-detail-card">
          <h3>{{ title }}</h3>
          <ul v-if="Array.isArray(concept.card[key])">
            <li v-for="(value, i) in concept.card[key]" :key="i"><RichText :text="value" /></li>
          </ul>
          <RichText v-else :text="concept.card[key]" />
        </section>
      </div>
      <p v-else class="empty">暂无归纳内容。</p>
      <p v-if="concept.card?.model || concept.card?.run_id" class="concept-provenance muted small">
        标注来源 {{ [concept.card.model, concept.card.run_id].filter(Boolean).join(' · ') }}。题目解法见作答后的解析。
      </p>
    </section>

    <section class="panel related">
      <div class="panel-heading"><h2>易混考点与区分信号</h2></div>
      <article v-for="(item, i) in concept.related" :key="i">
        <RouterLink v-if="item.concept_id" :to="{ path: '/concepts/' + item.concept_id, query: originQuery }">{{ item.name }}</RouterLink>
        <strong v-else>{{ item.name }}</strong>
        <RichText :text="item.signal" />
      </article>
      <p v-if="!concept.related?.length" class="empty">暂无易混考点记录。</p>
    </section>

    <section class="panel">
      <div class="panel-heading">
        <h2>关联真题</h2>
        <RouterLink :to="{ path: '/questions', query: { concept_id: concept.id } }">查看全部真题</RouterLink>
      </div>
      <QuestionRows v-if="concept.sample_questions?.length" :items="concept.sample_questions" :busy="starting" @practice="start" />
      <p v-else class="empty">没有关联真题。</p>
    </section>
  </template>
</template>
