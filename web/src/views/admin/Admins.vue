<script setup>
import { onMounted, reactive, ref } from 'vue'
import { api } from '../../api'

const data = ref({ admins: [] })
const busy = ref(true)
const creating = ref(false)
const error = ref('')
const notice = ref('')
const form = reactive({ username: '', password: '', role: 'operator' })

function date(s){return s?new Date(s).toLocaleString('zh-CN',{hour12:false}):'—'}

async function load(){
  busy.value=true;error.value=''
  try{data.value=await api.adminList()}catch(e){error.value=e.message}finally{busy.value=false}
}
onMounted(load)

async function create(){
  error.value='';notice.value=''
  if(form.password.length<8){error.value='密码至少 8 位';return}
  creating.value=true
  try{
    const username=form.username.trim()
    await api.adminCreate(username,form.password,form.role)
    notice.value=`已创建管理员「${username}」`
    form.username='';form.password='';form.role='operator'
    await load()
  }catch(e){error.value=e.message}finally{creating.value=false}
}

async function act(fn,okText){
  if(busy.value)return
  busy.value=true;error.value='';notice.value=''
  try{await fn();await load();notice.value=okText}catch(e){error.value=e.message}finally{busy.value=false}
}

function resetPassword(a){
  const pw=window.prompt(`为管理员「${a.username}」设置新密码（至少 8 位）：`)
  if(pw===null)return
  if(pw.length<8){error.value='密码至少 8 位';return}
  act(()=>api.adminResetAdminPassword(a.id,pw),`已重置管理员「${a.username}」的密码`)
}

function remove(a){
  if(!window.confirm(`确定删除管理员「${a.username}」吗？此操作不可撤销。`))return
  act(()=>api.adminDelete(a.id),`已删除管理员「${a.username}」`)
}
</script>
<template>
  <p v-if="error" class="notice err" role="alert">{{ error }}</p>
  <p v-if="notice" class="notice ok" role="status">{{ notice }}</p>
  <section class="panel">
    <div class="panel-heading"><h2>新建管理员</h2><span class="muted">用户名 3-32 位字母、数字、下划线或短横线；密码至少 8 位。</span></div>
    <form class="admin-create-form" @submit.prevent="create">
      <label>用户名<input v-model="form.username" required minlength="3" maxlength="32" pattern="[A-Za-z0-9_-]{3,32}" autocomplete="off" /></label>
      <label>初始密码<input v-model="form.password" required minlength="8" type="password" autocomplete="new-password" /></label>
      <label>角色<select v-model="form.role"><option value="operator">运营管理员</option><option value="super">超级管理员</option></select></label>
      <button class="primary" :disabled="creating">{{ creating ? '创建中…' : '创建' }}</button>
    </form>
  </section>
  <section class="panel">
    <div class="panel-heading"><h2>管理员列表 <span class="count">{{ data.admins.length }}</span></h2><span class="muted">删除与重置口令由服务端兜底校验</span></div>
    <p v-if="busy" class="empty" role="status">正在加载管理员…</p>
    <div v-else-if="data.admins.length" class="admin-table-wrap">
      <table class="data-table">
        <thead><tr><th>ID</th><th>用户名</th><th>角色</th><th>创建</th><th>最近登录</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="a in data.admins" :key="a.id">
            <td class="mono">{{ a.id }}</td>
            <td>{{ a.username }}</td>
            <td><span class="tag">{{ a.role === 'super' ? '超级管理员' : '运营管理员' }}</span></td>
            <td>{{ date(a.created_at) }}</td>
            <td>{{ date(a.last_login_at) }}</td>
            <td class="admin-actions">
              <button class="text-button" :disabled="busy" @click="resetPassword(a)">重置口令</button>
              <button class="text-button" :disabled="busy" @click="remove(a)">删除</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else class="empty"><h3>暂无管理员</h3></div>
  </section>
</template>
<style scoped>
.admin-table-wrap { overflow-x: auto; }
.admin-actions { white-space: nowrap; }
.admin-actions button { margin-right: var(--space-1); }
.admin-create-form {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 180px), 1fr));
  gap: var(--space-4);
  align-items: end;
  padding: var(--space-4) var(--space-6) var(--space-6);
}
.admin-create-form label { margin: 0; }
</style>
