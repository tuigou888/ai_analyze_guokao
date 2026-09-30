<script setup>
import RichText from './RichText.vue'
defineProps({items:{type:Array,default:()=>[]},busy:Boolean})
defineEmits(['practice'])
</script>
<template><div class="question-rows"><article v-for="q in items" :key="q.id" class="question-row"><div class="question-row-main"><div class="row-meta"><span class="tag">{{ q.module }}</span><span>{{ { single:'单选题',multi:'多选题',judge:'判断题',other:'暂无答案' }[q.answer_type] }}</span><span class="mono">#{{ q.id }}</span><span v-if="q.has_label" class="label-mark">已归纳考点</span><slot name="meta" :question="q" /></div><RichText class="brief-text" :text="q.stem" /></div><div class="row-actions"><button :disabled="q.answer_type==='other'||busy" @click="$emit('practice',q.id)">{{ q.answer_type==='other' ? '暂不可练习' : '开始练习' }} <span aria-hidden="true">↗</span></button><slot name="actions" :question="q" /></div></article></div></template>
