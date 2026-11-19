<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { forUser } from '../../api'
import { auth } from '../../auth'

const pageUserID = auth.user?.id
const api = forUser(pageUserID)
defineProps({ review: Object, profile: Object, days: Number })
const router = useRouter(), busy = ref(''), error = ref('')
const kindName = { single: '专项练习', concept: '考点练习', wrongbook: '错题复练', paperset: '真题练习', favorite: '收藏练习' }

async function start(kind, spec, key) {
  if (busy.value) return
  busy.value = key
  error.value = ''
  try {
    const session = kind === 'priority' ? await api.prioritizedWrongbook() : await api.createSession(kind, spec)
    if (auth.user?.id === pageUserID && !auth.identityChanged) await router.push(`/practice?session=${session.session_id}`)
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <div class="pc-review">
    <p v-if="error" class="notice err" role="alert">{{ error }}</p>
    <div class="pc-review-tasks">
      <section class="pc-card">
        <div class="pc-card-heading">
          <h2>接着上次练</h2>
          <RouterLink to="/records" class="pc-text-link">学习记录</RouterLink>
        </div>
        <div v-if="review.unfinished.length" class="pc-resume-list">
          <article v-for="item in review.unfinished" :key="item.session_id">
            <div>
              <h3>{{ kindName[item.kind] || '练习' }} · {{ item.total }} 题</h3>
              <p class="muted small">已保存 {{ item.saved_answers }} 次作答 · {{ item.started_at.slice(0,10) }} 开始</p>
            </div>
            <RouterLink class="button" :to="`/practice?session=${item.session_id}`">继续练习</RouterLink>
          </article>
        </div>
        <div v-else class="pc-empty">
          <p>暂无未完成练习。</p>
          <RouterLink class="button" to="/questions">前往题库</RouterLink>
        </div>
      </section>

      <section class="pc-card pc-review-priority">
        <div class="pc-card-heading">
          <h2>错题优先复练</h2>
          <RouterLink to="/wrongbook" class="pc-text-link">错题本</RouterLink>
        </div>
        <p class="muted">待订正 {{ review.wrong }} 题。按累计答错次数、最近答错时间选最多 20 题。</p>
        <p class="muted small">手工订正状态与复练成绩分别记录。</p>
        <button v-if="review.wrong_candidates.length" class="primary" :disabled="!!busy" @click="start('priority', {}, 'priority')">
          {{ busy === 'priority' ? '正在组题…' : '开始优先错题复练' }}
        </button>
        <p v-else class="pc-empty">暂无待复练错题。</p>
        <details v-if="review.wrong_candidates.length">
          <summary>本次优先题目（{{ review.wrong_candidates.length }}）</summary>
          <ul>
            <li v-for="item in review.wrong_candidates" :key="item.question_id">
              题 {{ item.question_id }} · {{ item.module || '未分类' }} · 累计答错 {{ item.wrong_count }} 次
            </li>
          </ul>
        </details>
      </section>
    </div>

    <div class="pc-chart-pair">
      <section class="pc-card">
        <h2>模块补强建议</h2>
        <p class="muted small">最近 {{ days }} 天至少 5 次作答的模块，按正确率由低到高。</p>
        <article v-for="item in review.weak_modules" :key="item.module" class="pc-suggestion">
          <div><h3>{{ item.module }}</h3><p>{{ item.accuracy.toFixed(1) }}% · {{ item.answered }} 次作答</p></div>
          <button :disabled="!!busy" @click="start('single', { module: item.module, limit: profile.default_limit }, item.module)">练习此模块</button>
        </article>
        <p v-if="!review.weak_modules.length" class="pc-empty">当前区间样本不足。<RouterLink to="/questions">补充模块练习</RouterLink></p>
      </section>

      <section class="pc-card">
        <h2>考点补强建议</h2>
        <p class="muted small">已有标注覆盖 {{ review.label_coverage == null ? '暂无' : review.label_coverage.toFixed(1) + '%' }}（{{ review.labeled_answered }} 次作答）。建议限于已标注考点。</p>
        <p v-if="review.unmapped_labeled_answered > 0" class="notice warn">{{ review.unmapped_labeled_answered }} 次标注作答暂无关联考点，可先做模块练习。</p>
        <article v-for="item in review.weak_concepts" :key="item.concept_id" class="pc-suggestion">
          <div><h3>{{ item.name }}</h3><p>{{ item.module }} · {{ item.accuracy.toFixed(1) }}% · {{ item.answered }} 次作答</p></div>
          <button :disabled="!!busy" @click="start('concept', { concept_id: item.concept_id, limit: profile.default_limit }, 'concept' + item.concept_id)">复练此考点</button>
        </article>
        <p v-if="!review.weak_concepts.length" class="pc-empty">暂无达到 5 次样本的考点。<RouterLink to="/concepts">查看考点地图</RouterLink></p>
      </section>
    </div>

    <section class="pc-card pc-review-links">
      <div><h2>收藏题目</h2><p class="muted">已收藏 {{ review.favorites }} 题</p></div>
      <RouterLink class="button" to="/favorites">查看我的收藏</RouterLink>
    </section>
  </div>
</template>
