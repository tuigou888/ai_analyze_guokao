<script setup>
import { onMounted,ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api'
import Pagination from '../components/Pagination.vue'
import QuestionRows from '../components/QuestionRows.vue'
const router=useRouter(),data=ref({items:[],total:0}),page=ref(1),busy=ref(false),starting=ref(false),error=ref('')
async function load(p=1){page.value=p;busy.value=true;error.value='';try{data.value=await api.favorites({page:p,size:20})}catch(e){error.value=e.message}finally{busy.value=false}}
async function start(id){starting.value=true;try{const s=await api.createSession('favorite',id?{question_ids:[id]}:{limit:20});await router.push(`/practice?session=${s.session_id}`)}catch(e){error.value=e.message}finally{starting.value=false}}
async function action(id,act){try{await api.favoriteAction(id,act);await load(page.value)}catch(e){error.value=e.message}}
onMounted(()=>load())
</script>
<template><div class="page-heading"><div><p class="eyebrow">收藏区 / SAVED</p><h1>把想再看的题，留在这里</h1><p class="muted">主动收藏与错题本分开记录：这里只放你手动收藏的题目。</p></div><button class="primary" :disabled="starting||!data.total" @click="start()">练习收藏题</button></div><p v-if="error" class="notice err" role="alert">{{ error }}</p><section class="panel"><div class="panel-heading"><h2>我的收藏 <span class="count">{{ data.total }}</span></h2></div><p v-if="busy" class="empty" role="status">正在加载收藏…</p><QuestionRows v-else-if="data.items.length" :items="data.items" :busy="starting" @practice="start"><template #meta="{question:q}"><span>收藏于 {{ (q.created_at||'').slice(0,10) }}</span></template><template #actions="{question:q}"><button class="text-button" @click="action(q.id,'delete')">取消收藏</button></template></QuestionRows><div v-else class="empty"><h3>还没有收藏题目</h3><p>在错题本或练习结果中点击「收藏」，题目会留存到这里。</p><RouterLink to="/questions">去题库练习</RouterLink></div><Pagination :page="page" :size="20" :total="data.total" :busy="busy" @change="load" /></section></template>