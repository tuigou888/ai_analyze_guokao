<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import Settings from './Settings.vue'
import { api } from '../api'
import { auth } from '../auth'
const password=ref(''),busy=ref(false),error=ref(''),notice=ref(''),router=useRouter()
async function change(){busy.value=true;try{await api.changePassword(password.value);auth.admin=null;await router.replace('/admin/login')}catch(e){error.value=e.message}finally{busy.value=false}}
async function refresh(){busy.value=true;try{await api.refreshConcepts();notice.value='已更新现有标注的考点映射与题量'}catch(e){error.value=e.message}finally{busy.value=false}}
</script>
<template><div class="page-heading"><div><p class="eyebrow">独立管理员账户</p><h1>网站管理</h1><p class="muted">接口配置与已有标注维护</p></div></div><div class="notice info">本阶段完成网站开发与部署准备。全量蒸馏需部署验收后在服务器手动启动。</div><Settings /><div class="account-grid admin-extra"><section class="panel account-card"><h2>更新考点视图</h2><p class="muted">根据已有标注更新考点题量，保持考点编号稳定。</p><button :disabled="busy" @click="refresh">更新考点映射</button></section><form class="panel account-card" @submit.prevent="change"><h2>修改管理员密码</h2><label>新密码<input v-model="password" type="password" required minlength="8" autocomplete="new-password" /></label><button :disabled="busy" class="primary">修改并重新登录</button></form></div><p v-if="error" class="notice err" role="alert">{{ error }}</p><p v-if="notice" class="notice ok" role="status">{{ notice }}</p></template>
