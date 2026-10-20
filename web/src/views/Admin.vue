<script setup>
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Settings from './Settings.vue'
import Users from './admin/Users.vue'
import Admins from './admin/Admins.vue'
import { api } from '../api'
import { auth } from '../auth'
import { toast } from '../toast'
const router = useRouter()
const isSuper = computed(() => auth.admin?.role === 'super')
const tabs = computed(() => [
  ...(isSuper.value ? [{ key: 'settings', label: '系统配置' }] : []),
  { key: 'users', label: '用户管理' },
  ...(isSuper.value ? [{ key: 'admins', label: '管理员账号' }] : []),
  { key: 'account', label: '我的账户' },
])
const tab = ref(isSuper.value ? 'settings' : 'users')
const current=ref(''),password=ref(''),busy=ref(false),error=ref(''),notice=ref('')
async function change(){busy.value=true;error.value='';try{await api.changePassword(current.value,password.value);auth.admin=null;await router.replace('/admin/login')}catch(e){error.value=e.message}finally{busy.value=false}}
async function refresh(){busy.value=true;error.value='';notice.value='';try{await api.refreshConcepts();notice.value='已更新现有标注的考点映射与题量'}catch(e){error.value=e.message}finally{busy.value=false}}
// 系统配置页的阶段性说明改为首次进入时右上角提示，不再常驻横幅。
let settingsNotified = false
watch(tab, v => {
  if (v === 'settings' && isSuper.value && !settingsNotified) {
    settingsNotified = true
    toast.info('本阶段完成网站开发与部署准备。全量蒸馏需部署验收后在服务器手动启动。')
  }
}, { immediate: true })
</script>
<template>
  <div class="page-heading"><div><p class="eyebrow">独立管理员账户</p><h1>网站管理</h1><p class="muted">接口配置与已有标注维护</p></div></div>
  <nav class="tabs" aria-label="管理页签">
    <button v-for="t in tabs" :key="t.key" :class="{ active: tab === t.key }" @click="tab = t.key">{{ t.label }}</button>
  </nav>
  <template v-if="tab === 'settings' && isSuper">
    <Settings />
  </template>
  <Users v-else-if="tab === 'users'" />
  <Admins v-else-if="tab === 'admins' && isSuper" />
  <template v-else-if="tab === 'account'">
    <div class="account-grid admin-extra">
      <section class="panel account-card"><h2>更新考点视图</h2><p class="muted">根据已有标注更新考点题量，保持考点编号稳定。</p><button :disabled="busy" @click="refresh">更新考点映射</button></section>
      <form class="panel account-card" @submit.prevent="change"><h2>修改管理员密码</h2><label>当前密码<input v-model="current" type="password" required autocomplete="current-password" /></label><label>新密码<input v-model="password" type="password" required minlength="8" autocomplete="new-password" /></label><button :disabled="busy" class="primary">修改并重新登录</button></form>
    </div>
    <p v-if="error" class="notice err" role="alert">{{ error }}</p>
    <p v-if="notice" class="notice ok" role="status">{{ notice }}</p>
  </template>
</template>
