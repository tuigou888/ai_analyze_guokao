<script setup>
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, query } from '../api'
import { auth } from '../auth'
import Filters from '../components/Filters.vue'
import Pagination from '../components/Pagination.vue'
import QuestionRows from '../components/QuestionRows.vue'
const route=useRoute(),router=useRouter()
const filters=ref({module:Object.hasOwn(route.query,'module') ? route.query.module : auth.profile?.default_module || '',year:'',region:'',exam_type:'',variant:'',q:'',...route.query})
const data=ref({items:[],total:0}),busy=ref(false),starting=ref(false),error=ref(''),page=ref(Number(route.query.page)||1),count=ref([10,20,50].includes(Number(route.query.limit)) ? Number(route.query.limit) : auth.profile?.default_limit || 20)
async function load(p=1){busy.value=true;error.value='';page.value=p;try{data.value=await api.questions({...filters.value,page:p,size:20})}catch(e){error.value=e.message}finally{busy.value=false}}
async function apply(){const values=new URLSearchParams(query({...filters.value,page:1}));values.set('module',filters.value.module||'');await router.replace(`/questions?${values}`);await load()}
async function start(id){starting.value=true;error.value='';try{const s=await api.createSession('single',id ? {question_ids:[id]} : {...filters.value,limit:count.value});await router.push(`/practice?session=${s.session_id}`)}catch(e){error.value=e.message}finally{starting.value=false}}
onMounted(()=>load(page.value))
</script>
<template><div class="page-heading"><div><p class="eyebrow">题库练习 / QUESTION BANK</p><h1>从一道真题开始</h1><p class="muted">按模块找到今天要练的题。先独立作答，再对照解析。</p></div><div class="session-controls"><label>本次题量<select v-model="count"><option :value="10">10 题</option><option :value="20">20 题</option><option :value="50">50 题</option></select></label><button class="primary" :disabled="starting||busy||!data.total" @click="start()">{{ starting ? '正在组题…' : '开始专项练习' }}</button></div></div>
<Filters v-model="filters" search @apply="apply" /><p v-if="error" class="notice err" role="alert">{{ error }} <button @click="load(page)">重试</button></p>
<section class="panel"><div class="panel-heading"><h2>真题列表 <span class="count">{{ data.total.toLocaleString() }}</span></h2><span class="muted">{{ filters.module || '全部模块' }}</span></div><p v-if="busy" class="empty" role="status">正在查找题目…</p><QuestionRows v-else-if="data.items.length" :items="data.items" :busy="starting" @practice="start" /><div v-else class="empty"><h3>没有找到对应题目</h3><p>试试缩短关键词，或减少筛选条件。</p></div><Pagination :page="page" :total="data.total" :size="20" :busy="busy" @change="load" /></section></template>
