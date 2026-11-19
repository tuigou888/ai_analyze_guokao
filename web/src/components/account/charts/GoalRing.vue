<script setup>
import { computed } from 'vue'

const props = defineProps({ value: Number, target: Number, unit: String, label: String, digits: Number, percent: Number })
const completion = computed(() => `${Math.round(props.percent)}%`)
const remaining = computed(() => Math.max(0, props.target - props.value))
</script>

<template>
  <div class="pc-goal" data-chart="goal">
    <div class="pc-goal-label">
      <h3>{{ label }}</h3>
      <p class="pc-goal-count"><strong>{{ value.toFixed(digits) }}</strong> / {{ target }} {{ unit }}</p>
    </div>
    <progress :value="percent" max="100" tabindex="0" :aria-label="label"
      :aria-valuetext="`${value.toFixed(digits)} / ${target} ${unit}，完成 ${completion}`" />
    <p class="pc-goal-detail muted small">
      <span>{{ remaining > 0 ? `还差 ${remaining.toFixed(digits)} ${unit}` : `已达标${value > target ? `，超额 ${(value - target).toFixed(digits)} ${unit}` : ''}` }}</span>
      <span class="pc-goal-percent">{{ completion }}</span>
    </p>
  </div>
</template>
