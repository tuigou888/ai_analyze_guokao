<script setup>
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { api } from '../../api'
import Pagination from '../../components/Pagination.vue'
import OverlayPanel from '../../components/OverlayPanel.vue'

const PAGE_SIZE = 20
const filters = reactive({ status: '', role: '', q: '' })
let applied = { ...filters }
const data = ref(null)
const page = ref(1)
const loading = ref(true)
const updating = ref(false)
const error = ref('')
const notice = ref('')
const user = ref(null)
const operation = ref('')
const expires = ref('')
const password = ref('')
const actionError = ref('')
const editor = ref(null)
const search = ref(null)
let opener

const busy = computed(() => updating.value)
const dirty = computed(() => Boolean(expires.value || password.value))
defineExpose({ dirty, busy })

function date(value) {
  return value ? new Date(value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) : '—'
}
function vipExpires(item) {
  return item.vip_expires_at || (item.role === 'vip' ? '长期' : '—')
}

async function load(target = 1) {
  loading.value = true
  error.value = ''
  try {
    let result = await api.adminUsers({ page: target, ...applied })
    const last = Math.max(1, Math.ceil(result.total / PAGE_SIZE))
    if (target > last) {
      target = last
      result = await api.adminUsers({ page: target, ...applied })
    }
    page.value = target
    data.value = { users: result.users || [], total: result.total || 0 }
  } catch (e) {
    error.value = e.message
    data.value = null
    page.value = target
  } finally {
    loading.value = false
  }
}

function query() {
  if (loading.value || updating.value) return
  applied = { ...filters, q: filters.q.trim() }
  notice.value = ''
  load()
}
onMounted(() => load())

function open(event, item) {
  opener = event.currentTarget
  user.value = item
  operation.value = ''
  expires.value = ''
  password.value = ''
  actionError.value = ''
}

function clearAction() {
  user.value = null
  operation.value = ''
  expires.value = ''
  password.value = ''
  actionError.value = ''
}

function close() {
  if (!user.value) {
    if (opener?.isConnected) opener.focus()
    else search.value?.focus()
    return
  }
  if (updating.value) return
  if (dirty.value && !window.confirm('确定放弃此操作的输入吗？')) return
  clearAction()
}

async function edit(mode) {
  operation.value = mode
  actionError.value = ''
  await nextTick()
  editor.value?.querySelector('input, button')?.focus()
}

async function act(request, success) {
  if (updating.value) return
  updating.value = true
  actionError.value = ''
  notice.value = ''
  try {
    await request()
    notice.value = success
    await load(page.value)
    clearAction()
  } catch (e) {
    actionError.value = e.message
  } finally {
    updating.value = false
  }
}

function toggleStatus(item) {
  const banned = item.status === 'banned'
  const action = banned ? '解封' : '封禁'
  const consequence = banned ? '' : '封禁会立即结束该用户的所有登录会话。'
  if (!window.confirm(`确定${action}用户「${item.username}」吗？${consequence}`)) return
  act(() => api.adminSetUserStatus(item.id, banned ? 'active' : 'banned'), `已${action}用户「${item.username}」`)
}

function clearVip(item) {
  if (!window.confirm(`确定取消用户「${item.username}」的会员吗？取消后按免费用户额度使用。`)) return
  act(() => api.adminSetUserRole(item.id, 'free'), `已取消用户「${item.username}」的会员`)
}

function submitVip() {
  const item = user.value
  act(() => api.adminSetUserRole(item.id, 'vip', expires.value), `已将用户「${item.username}」设为会员`)
}

function resetPassword() {
  const item = user.value
  const bytes = new TextEncoder().encode(password.value).length
  if (bytes < 8 || bytes > 72) {
    actionError.value = '新密码需 8–72 字节'
    return
  }
  act(() => api.adminResetUserPassword(item.id, password.value), `已重置用户「${item.username}」的密码`)
}
</script>

<template>
  <form class="card admin-filter" @submit.prevent="query">
    <label class="admin-search">关键词
      <input ref="search" v-model="filters.q" type="search" placeholder="搜索用户名或昵称" maxlength="120" />
    </label>
    <label>状态
      <select v-model="filters.status">
        <option value="">全部状态</option><option value="active">正常</option><option value="banned">已封禁</option>
      </select>
    </label>
    <label>角色
      <select v-model="filters.role">
        <option value="">全部角色</option><option value="free">免费</option><option value="vip">会员</option>
      </select>
    </label>
    <button class="primary" type="submit" :disabled="loading || updating">查询</button>
  </form>

  <p v-if="error" class="notice err" role="alert">
    {{ error }} <button :disabled="loading || updating" @click="load(page)">重试</button>
  </p>
  <p v-if="notice" class="notice ok" role="status">{{ notice }}</p>

  <section class="panel" :aria-busy="loading">
    <div class="panel-heading">
      <h2 id="admin-users">用户列表 <span v-if="data" class="count">{{ data.total.toLocaleString() }}</span></h2>
      <span v-if="data?.total" class="muted">每页 {{ PAGE_SIZE }} 条</span>
    </div>
    <p v-if="loading" class="admin-loading" role="status">正在加载用户列表…</p>
    <template v-if="data?.users.length">
      <div class="admin-table-wrap" tabindex="0" role="region" aria-labelledby="admin-users">
        <table class="data-table admin-table">
          <thead><tr><th scope="col">用户</th><th scope="col">昵称</th><th scope="col">角色</th><th scope="col">状态</th><th scope="col">会员到期</th><th scope="col">注册</th><th scope="col">最近登录</th><th scope="col">操作</th></tr></thead>
          <tbody>
            <tr v-for="item in data.users" :key="item.id">
              <td class="admin-table-identity"><strong>{{ item.username }}</strong><small>编号 {{ item.id }}</small></td>
              <td>{{ item.nickname || '—' }}</td>
              <td><span class="tag">{{ item.role === 'vip' ? '会员' : '免费' }}</span></td>
              <td><span :class="['tag', item.status === 'banned' ? 'tag-banned' : 'tag-active']">{{ item.status === 'banned' ? '已封禁' : '正常' }}</span></td>
              <td>{{ vipExpires(item) }}</td>
              <td>{{ date(item.created_at) }}</td>
              <td>{{ date(item.last_login_at) }}</td>
              <td class="admin-actions">
                <button type="button" class="text-button" :aria-label="`管理用户 ${item.username}`"
                  :disabled="loading || updating" @click="open($event, item)">管理</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <Pagination v-if="data.total > PAGE_SIZE" :page="page" :size="PAGE_SIZE" :total="data.total"
        :busy="loading || updating" @change="load" />
    </template>
    <div v-else-if="data && !loading" class="empty"><h3>没有符合条件的用户</h3><p>试试调整筛选条件或关键词。</p></div>
  </section>

  <OverlayPanel :open="!!user" title="管理用户" @close="close">
    <div v-if="user" class="admin-operation">
      <p class="muted">用户「{{ user.username }}」 · {{ user.role === 'vip' ? '会员' : '免费' }} · {{ user.status === 'banned' ? '已封禁' : '正常' }}</p>
      <p v-if="actionError" class="notice err" role="alert">{{ actionError }}</p>

      <form v-if="operation === 'vip'" ref="editor" @submit.prevent="submitVip">
        <h3>设为会员</h3>
        <label>会员到期日期
          <input v-model="expires" type="date" :disabled="updating" aria-describedby="admin-vip-hint" />
          <small id="admin-vip-hint" class="field-hint">留空表示长期有效。填写的日期需晚于今天。</small>
        </label>
        <div class="dialog-actions">
          <button type="button" :disabled="updating" @click="close">取消</button>
          <button class="primary" type="submit" :disabled="updating">{{ updating ? '提交中…' : '确定设为会员' }}</button>
        </div>
      </form>

      <form v-else-if="operation === 'password'" ref="editor" @submit.prevent="resetPassword">
        <h3>重置密码</h3>
        <p class="muted small">重置后，该用户需在所有设备重新登录。</p>
        <label>新密码
          <input v-model="password" type="password" required :disabled="updating" autocomplete="new-password"
            aria-describedby="admin-user-password-hint" />
          <small id="admin-user-password-hint" class="field-hint">8–72 字节。</small>
        </label>
        <div class="dialog-actions">
          <button type="button" :disabled="updating" @click="close">取消</button>
          <button class="danger" type="submit" :disabled="updating">{{ updating ? '提交中…' : '确定重置密码' }}</button>
        </div>
      </form>

      <div v-else class="admin-operation-actions">
        <button v-if="user.role !== 'vip'" class="primary" :disabled="updating" @click="edit('vip')">设为会员</button>
        <button v-else :disabled="updating" @click="clearVip(user)">取消会员</button>
        <button :disabled="updating" @click="edit('password')">重置密码</button>
        <button :class="{ danger: user.status !== 'banned' }" :disabled="updating" @click="toggleStatus(user)">
          {{ user.status === 'banned' ? '解封' : '封禁' }}
        </button>
      </div>
    </div>
  </OverlayPanel>
</template>
