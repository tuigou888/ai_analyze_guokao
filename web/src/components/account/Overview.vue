<script setup>
import { computed } from 'vue'
import GoalRing from './charts/GoalRing.vue'
import TrendChart from './charts/TrendChart.vue'
import ModuleChart from './charts/ModuleChart.vue'
import ActivityCalendar from './charts/ActivityCalendar.vue'

const props = defineProps({ dashboard: Object, profile: Object })
defineEmits(['goals'])
const accuracy = value => value == null ? '—' : `${value.toFixed(1)}%`
const goals = computed(() => [
  { label: '作答目标', row: '作答次数', value: props.dashboard.today.answered, target: props.profile.daily_questions, unit: '次', digits: 0 },
  { label: '记录时长', row: '记录分钟', value: props.dashboard.today.duration_ms / 60000, target: props.profile.daily_minutes, unit: '分钟', digits: 1 },
].map(goal => ({
  ...goal,
  percent: goal.target > 0 ? Math.min(100, Math.max(0, goal.value / goal.target * 100)) : 0,
})))
</script>

<template>
  <div class="pc-overview">
    <section class="pc-card pc-today">
      <div class="pc-card-heading">
        <h2>今日双目标</h2>
        <button type="button" class="text-button" @click="$emit('goals')">设置目标</button>
      </div>
      <div class="pc-goals">
        <GoalRing v-for="goal in goals" :key="goal.label" :label="goal.label" :value="goal.value"
          :target="goal.target" :unit="goal.unit" :digits="goal.digits" :percent="goal.percent" />
      </div>
      <details class="pc-table-details">
        <summary>查看今日目标数据表</summary>
        <div class="pc-table-scroll">
          <table class="data-table data-table--compact">
            <caption>今日实际完成量与设置目标</caption>
            <thead><tr><th scope="col">目标</th><th scope="col">实际</th><th scope="col">目标值</th><th scope="col">进度</th></tr></thead>
            <tbody>
              <tr v-for="goal in goals" :key="goal.label">
                <td>{{ goal.row }}</td>
                <td>{{ goal.value.toFixed(goal.digits) }}</td>
                <td>{{ goal.target }}</td>
                <td>{{ Math.round(goal.percent) }}%</td>
              </tr>
            </tbody>
          </table>
        </div>
      </details>
      <div class="pc-today-footer">
        <p class="muted">
          <span v-if="!dashboard.streak.practiced_today">今日尚未提交有效作答 · </span>
          连续练习 {{ dashboard.streak.current }} 天 · 最长 {{ dashboard.streak.longest }} 天
        </p>
        <RouterLink class="button primary" to="/questions">开始今日练习</RouterLink>
      </div>
    </section>

    <section aria-labelledby="pc-metric-title">
      <header class="pc-metric-heading">
        <h2 id="pc-metric-title">近 {{ dashboard.days }} 天学习统计</h2>
        <p class="muted small">{{ dashboard.start_date }} 至 {{ dashboard.end_date }}（北京时间）</p>
      </header>
      <div class="pc-counts">
        <article class="pc-card">
          <span>区间作答</span>
          <strong>{{ dashboard.period.answered.toLocaleString() }}<em> 次</em></strong>
          <small>较上期 {{ dashboard.period.answered - dashboard.previous.answered >= 0 ? '+' : '' }}{{ dashboard.period.answered - dashboard.previous.answered }} 次</small>
        </article>
        <article class="pc-card">
          <span>区间正确率</span>
          <strong>{{ accuracy(dashboard.period.accuracy) }}</strong>
          <small v-if="dashboard.period.accuracy == null">当前区间暂无有效作答</small>
          <small v-else>答对 {{ dashboard.period.correct }} 次 / 作答 {{ dashboard.period.answered }} 次</small>
        </article>
        <article class="pc-card">
          <span>区间记录时长</span>
          <strong>{{ (dashboard.period.duration_ms / 60000).toFixed(1) }}<em> 分钟</em></strong>
          <small>{{ dashboard.period.sessions }} 份已提交练习</small>
        </article>
        <article class="pc-card pc-lifetime">
          <span>历史累计作答</span>
          <strong>{{ dashboard.lifetime.answered.toLocaleString() }}<em> 次</em></strong>
          <small>累计 {{ dashboard.lifetime.sessions }} 份练习</small>
          <small v-if="dashboard.lifetime.accuracy != null">累计正确率 {{ accuracy(dashboard.lifetime.accuracy) }}</small>
        </article>
      </div>
      <p class="pc-caption">作答统计只计已提交练习中的非空作答。记录时长取自练习计时。</p>
    </section>

    <div class="pc-chart-pair">
      <TrendChart :items="dashboard.trend" />
      <ModuleChart :items="dashboard.modules" />
    </div>
    <ActivityCalendar :items="dashboard.activity" />
    <section class="pc-card">
      <h2>学习成就</h2>
      <p class="muted small">成就按本人真实作答计算，重复练习计为多次作答。</p>
      <div class="pc-achievements">
        <article v-for="item in dashboard.achievements" :key="item.id" :class="['pc-achievement', {'pc-achievement-unlocked':item.unlocked}]">
          <div>
            <h3>{{ item.name }}</h3>
            <p>{{ item.unlocked ? '已达成' : '积累中' }} · {{ item.current }} / {{ item.target }}</p>
            <progress :value="Math.min(item.current,item.target)" :max="item.target" :aria-label="`${item.name} ${item.current}/${item.target}`"/>
          </div>
        </article>
      </div>
    </section>
  </div>
</template>
