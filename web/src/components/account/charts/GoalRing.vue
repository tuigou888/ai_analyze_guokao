<script setup>
import { computed } from 'vue'
const props=defineProps({value:Number,target:Number,unit:String,label:String})
const percent=computed(()=>Math.min(100,Math.max(0,props.value/props.target*100)))
const remaining=computed(()=>Math.max(0,props.target-props.value))
</script>
<template><div class="pc-goal" data-chart="goal"><svg viewBox="0 0 120 120" role="img" tabindex="0" :aria-label="`${label}：${value.toFixed(unit==='分钟'?1:0)} / ${target} ${unit}，完成 ${Math.round(percent)}%`"><circle class="pc-ring-track" cx="60" cy="60" r="48"/><circle class="pc-ring-value" cx="60" cy="60" r="48" pathLength="100" :stroke-dasharray="`${percent} 100`"/><text x="60" y="62" text-anchor="middle">{{ Math.round(percent) }}%</text></svg><div><h3>{{ label }}</h3><p class="pc-goal-count"><strong>{{ value.toFixed(unit==='分钟'?1:0) }}</strong> / {{ target }} {{ unit }}</p><p class="muted small">{{ remaining>0 ? `还差 ${remaining.toFixed(unit==='分钟'?1:0)} ${unit}` : `已达标${value>target ? `，超额 ${(value-target).toFixed(unit==='分钟'?1:0)} ${unit}` : ''}` }}</p></div></div></template>
