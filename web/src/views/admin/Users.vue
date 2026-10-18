<script setup>
import { onMounted, reactive, ref } from 'vue'
import { api } from '../../api'
import Pagination from '../../components/Pagination.vue'

const filters = reactive({ status: '', role: '', q: '' })
const data = ref({ users: [], total: 0 })
const page = ref(1)
const busy = ref(true)
const error = ref('')
const notice = ref('')
const vip = ref(null)

function date(s){return s?new Date(s).toLocaleString('zh-CN',{hour12:false}):'—'}
function vipExpires(u){if(u.vip_expires_at)return u.vip_expires_at;return u.role==='vip'?'长期':'—'}

async function load(p=1){
  page.value=p;busy.value=true;error.value=''
  try{
    const r=await api.adminUsers({page:p,status:filters.status,role:filters.role,q:filters.q.trim()})
    data.value={users:r.users||[],total:r.total||0}
  }catch(e){error.value=e.message}finally{busy.value=false}
}
onMounted(()=>load())

async function act(fn,okText){
  if(busy.value)return
  busy.value=true;error.value='';notice.value=''
  try{await fn();await load(page.value);notice.value=okText}catch(e){error.value=e.message}finally{busy.value=false}
}

function toggleStatus(u){
  const next=u.status==='banned'?'active':'banned'
  if(!window.confirm(`确定${next==='banned'?'封禁':'解封'}用户「${u.username}」吗？`))return
  act(()=>api.adminSetUserStatus(u.id,next),`已${next==='banned'?'封禁':'解封'}用户「${u.username}」`)
}

function openVip(u){vip.value={user:u,expires:'',busy:false,error:''}}
async function submitVip(){
  const v=vip.value
  if(!v||v.busy)return
  v.busy=true;v.error=''
  try{
    await api.adminSetUserRole(v.user.id,'vip',v.expires)
    notice.value=`已将用户「${v.user.username}」设为会员`
    vip.value=null
    await load(page.value)
  }catch(e){v.error=e.message;v.busy=false}
}

function clearVip(u){
  if(!window.confirm(`确定取消用户「${u.username}」的会员吗？取消后按免费用户额度使用。`))return
  act(()=>api.adminSetUserRole(u.id,'free'),`已取消用户「${u.username}」的会员`)
}

function resetPassword(u){
  const pw=window.prompt(`为用户「${u.username}」设置新密码（至少 8 位）：`)
  if(pw===null)return
  if(pw.length<8){error.value='密码至少 8 位';return}
  act(()=>api.adminResetUserPassword(u.id,pw),`已重置用户「${u.username}」的密码`)
}
</script>
<template>
  <form class="filters" @submit.prevent="load(1)">
    <label>状态<select v-model="filters.status"><option value="">全部状态</option><option value="active">正常</option><option value="banned">已封禁</option></select></label>
    <label>角色<select v-model="filters.role"><option value="">全部角色</option><option value="free">免费</option><option value="vip">会员</option></select></label>
    <label class="search-field">关键词<input v-model="filters.q" placeholder="搜索用户名或昵称" maxlength="120" /></label>
    <button class="primary" type="submit" :disabled="busy">查询</button>
  </form>
  <p v-if="error" class="notice err" role="alert">{{ error }} <button :disabled="busy" @click="load(page)">重试</button></p>
  <p v-if="notice" class="notice ok" role="status">{{ notice }}</p>
  <section class="panel">
    <div class="panel-heading"><h2>用户列表 <span class="count">{{ data.total.toLocaleString() }}</span></h2><span class="muted">每页 20 条</span></div>
    <p v-if="busy" class="empty" role="status">正在加载用户列表…</p>
    <template v-else-if="data.users.length">
      <div class="admin-table-wrap">
        <table class="data-table">
          <thead><tr><th>ID</th><th>用户名</th><th>昵称</th><th>角色</th><th>状态</th><th>会员到期</th><th>注册</th><th>最近登录</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="u in data.users" :key="u.id" :class="{ 'admin-banned': u.status === 'banned' }">
              <td class="mono">{{ u.id }}</td>
              <td>{{ u.username }}</td>
              <td>{{ u.nickname || '—' }}</td>
              <td><span class="tag">{{ u.role === 'vip' ? '会员' : '免费' }}</span></td>
              <td><span :class="['tag', u.status === 'banned' ? 'tag-banned' : '']">{{ u.status === 'banned' ? '已封禁' : '正常' }}</span></td>
              <td>{{ vipExpires(u) }}</td>
              <td>{{ date(u.created_at) }}</td>
              <td>{{ date(u.last_login_at) }}</td>
              <td class="admin-actions">
                <button class="text-button" :disabled="busy" @click="toggleStatus(u)">{{ u.status === 'banned' ? '解封' : '封禁' }}</button>
                <button v-if="u.role !== 'vip'" class="text-button" :disabled="busy" @click="openVip(u)">设为会员</button>
                <button v-else class="text-button" :disabled="busy" @click="clearVip(u)">取消会员</button>
                <button class="text-button" :disabled="busy" @click="resetPassword(u)">重置密码</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <Pagination :page="page" :size="20" :total="data.total" :busy="busy" @change="load" />
    </template>
    <div v-else class="empty"><h3>没有符合条件的用户</h3><p>试试调整筛选条件或关键词。</p></div>
  </section>
  <div v-if="vip" class="admin-modal-mask" @click.self="vip = null">
    <form class="panel admin-modal" @submit.prevent="submitVip">
      <div class="panel-heading"><h2>设为会员</h2></div>
      <div class="admin-modal-body">
        <p class="muted">用户「{{ vip.user.username }}」。到期日期留空表示长期有效。</p>
        <label>会员到期日期<input v-model="vip.expires" type="date" /></label>
        <p v-if="vip.error" class="notice err" role="alert">{{ vip.error }}</p>
        <div class="admin-modal-actions">
          <button type="button" :disabled="vip.busy" @click="vip = null">取消</button>
          <button class="primary" :disabled="vip.busy">{{ vip.busy ? '提交中…' : '确定' }}</button>
        </div>
      </div>
    </form>
  </div>
</template>
<style scoped>
.admin-table-wrap { overflow-x: auto; }
.admin-actions { white-space: nowrap; }
.admin-actions button { margin-right: var(--space-1); }
.admin-banned td { color: var(--color-text-muted); background: var(--color-danger-soft); }
.tag-banned { background: var(--color-danger-surface); color: var(--color-danger); }
.admin-modal-mask {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  background: rgba(0, 0, 0, .4);
}
.admin-modal { width: min(100%, 400px); margin: 0; }
.admin-modal-body { padding: var(--space-4) var(--space-6) var(--space-6); }
.admin-modal-body label { display: block; margin-top: var(--space-4); }
.admin-modal-actions { display: flex; justify-content: flex-end; gap: var(--space-2); margin-top: var(--space-6); }
</style>
