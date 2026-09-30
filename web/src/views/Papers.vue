<script setup>
import { onMounted,ref,computed } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api'
import Filters from '../components/Filters.vue'
import Pagination from '../components/Pagination.vue'
const filters=ref({}),items=ref([]),busy=ref(false),starting=ref(0),error=ref(''),page=ref(1),router=useRouter()
const visible=computed(()=>items.value.slice((page.value-1)*12,page.value*12))
async function load(){busy.value=true;error.value='';page.value=1;try{items.value=await api.papers(filters.value)}catch(e){error.value=e.message}finally{busy.value=false}}
async function start(p){starting.value=p.paper_id;try{const s=await api.createSession('paperset',{paper_id:p.paper_id});await router.push(`/practice?session=${s.session_id}`)}catch(e){error.value=e.message}finally{starting.value=0}}
onMounted(load)
</script>
<template><div class="page-heading"><div><p class="eyebrow">真题试卷 / PAPERS</p><h1>在一套题里，检验理解</h1><p class="muted">保留原卷模块与题序。完成后统一判分，错题自动收录。</p></div></div><Filters v-model="filters" @apply="load" /><p v-if="error" class="notice err" role="alert">{{ error }}</p><p v-if="busy" class="empty" role="status">正在加载试卷…</p><div v-else class="paper-grid"><article v-for="p in visible" :key="p.paper_id" class="paper-card"><div class="row-meta"><span class="tag">{{ p.module }}</span><span>{{ p.region }}</span></div><span class="paper-year">{{ p.year }}</span><h2>{{ p.name }}</h2><p class="muted">{{ p.exam_type }} · {{ p.variant || '通用卷' }} · {{ p.question_count }} 题</p><button :disabled="!!starting||p.question_count===0" @click="start(p)">{{ starting===p.paper_id ? '正在组卷…' : '开始作答' }} <span aria-hidden="true">↗</span></button></article></div><div v-if="!busy&&!items.length" class="empty">没有找到试卷，请调整筛选条件。</div><Pagination :page="page" :size="12" :total="items.length" :busy="busy" @change="page=$event" /></template>
