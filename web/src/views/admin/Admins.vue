<script setup>
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../../api'
import { auth } from '../../auth'
import OverlayPanel from '../../components/OverlayPanel.vue'

const router = useRouter()
const admins = ref([])
const loading = ref(true)
const creating = ref(false)
const updating = ref(false)
const error = ref('')
const createError = ref('')
const actionError = ref('')
const notice = ref('')
const form = reactive({ username: '', password: '', role: 'operator' })
const target = ref(null)
const resetting = ref(false)
const password = ref('')
const passwordInput = ref(null)
const table = ref(null)
let opener

const busy = computed(() => creating.value || updating.value)
const dirty = computed(() => Boolean(form.username || form.password || form.role !== 'operator' || password.value))
defineExpose({ dirty, busy })

function date(value) {
  return value ? new Date(value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) : '—'
}
function validPassword(value) {
  const bytes = new TextEncoder().encode(value).length
  return bytes >= 8 && bytes <= 72
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await api.adminList()
    admins.value = data.admins || []
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function create() {
  if (busy.value) return
  createError.value = ''
  notice.value = ''
  if (!validPassword(form.password)) {
    createError.value = '初始密码需 8–72 字节'
    return
  }

  creating.value = true
  try {
    const username = form.username.trim()
    await api.adminCreate(username, form.password, form.role)
    notice.value = `已创建管理员「${username}」`
    form.username = ''
    form.password = ''
    form.role = 'operator'
    await load()
  } catch (e) {
    createError.value = e.message
  } finally {
    creating.value = false
  }
}

function open(event, item) {
  opener = event.currentTarget
  target.value = item
  resetting.value = false
  password.value = ''
  actionError.value = ''
}

function clearAction() {
  target.value = null
  resetting.value = false
  password.value = ''
  actionError.value = ''
}

function close() {
  if (!target.value) {
    if (opener?.isConnected) opener.focus()
    else table.value?.focus()
    return
  }
  if (updating.value) return
  if (password.value && !window.confirm('确定放弃新密码输入吗？')) return
  clearAction()
}

async function editPassword() {
  resetting.value = true
  actionError.value = ''
  await nextTick()
  passwordInput.value?.focus()
}

async function act(request, success, currentAccount = false) {
  if (busy.value) return
  updating.value = true
  notice.value = ''
  actionError.value = ''
  try {
    await request()
    if (currentAccount) {
      clearAction()
      auth.admin = null
      await router.replace('/admin/login')
      return
    }
    notice.value = success
    await load()
    clearAction()
  } catch (e) {
    actionError.value = e.message
  } finally {
    updating.value = false
  }
}

function resetPassword() {
  const item = target.value
  if (!validPassword(password.value)) {
    actionError.value = '新密码需 8–72 字节'
    return
  }
  act(() => api.adminResetAdminPassword(item.id, password.value),
    `已重置管理员「${item.username}」的口令`, item.username === auth.admin?.username)
}

function remove() {
  const item = target.value
  if (item.username === auth.admin?.username) return
  if (!window.confirm(`确定删除管理员「${item.username}」吗？此操作不可撤销，并将结束该账号的所有会话。`)) return
  act(() => api.adminDelete(item.id), `已删除管理员「${item.username}」`)
}
</script>

<template>
  <section class="panel admin-narrow">
    <div class="panel-heading"><h2>新建管理员</h2></div>
    <form class="admin-create-form" @submit.prevent="create">
      <fieldset :disabled="busy">
        <label>用户名
          <input v-model="form.username" required minlength="3" maxlength="32" pattern="[A-Za-z0-9_\-]{3,32}"
            autocomplete="off" aria-describedby="new-admin-name-hint" />
          <small class="field-hint" id="new-admin-name-hint">3–32 位字母、数字、下划线或短横线。</small>
        </label>
        <label>初始密码
          <input v-model="form.password" required type="password" autocomplete="new-password" aria-describedby="new-admin-password-hint" />
          <small class="field-hint" id="new-admin-password-hint">8–72 字节。</small>
        </label>
        <label>角色
          <select v-model="form.role">
            <option value="operator">运营管理员</option><option value="super">超级管理员</option>
          </select>
          <small v-if="form.role === 'super'" class="field-hint">超级管理员还可管理系统配置和其他管理员。</small>
        </label>
      </fieldset>
      <p v-if="createError" class="notice err" role="alert">{{ createError }}</p>
      <div class="admin-create-submit">
        <button class="primary" type="submit" :disabled="busy">{{ creating ? '创建中…' : '创建管理员' }}</button>
      </div>
    </form>
  </section>

  <p v-if="error" class="notice err" role="alert">{{ error }} <button :disabled="loading || busy" @click="load">重试</button></p>
  <p v-if="notice" class="notice ok" role="status">{{ notice }}</p>

  <section class="panel" :aria-busy="loading">
    <div class="panel-heading"><h2 id="admin-list">管理员列表 <span v-if="!loading" class="count">{{ admins.length }}</span></h2></div>
    <p v-if="loading" class="admin-loading" role="status">正在加载管理员…</p>
    <div v-if="admins.length" ref="table" class="admin-table-wrap" role="region" tabindex="0" aria-labelledby="admin-list">
      <table class="data-table admin-table">
        <thead><tr><th scope="col">管理员</th><th scope="col">角色</th><th scope="col">创建</th><th scope="col">最近登录</th><th scope="col">操作</th></tr></thead>
        <tbody>
          <tr v-for="item in admins" :key="item.id">
            <td class="admin-table-identity"><strong>{{ item.username }}</strong><small>编号 {{ item.id }}</small></td>
            <td><span class="tag">{{ item.role === 'super' ? '超级管理员' : '运营管理员' }}</span></td>
            <td>{{ date(item.created_at) }}</td>
            <td>{{ date(item.last_login_at) }}</td>
            <td class="admin-actions">
              <button type="button" class="text-button" :aria-label="`管理管理员 ${item.username}`"
                :disabled="loading || busy" @click="open($event, item)">管理</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else-if="!loading && !error" class="empty"><h3>暂无管理员</h3></div>
  </section>

  <OverlayPanel :open="!!target" title="管理管理员账号" @close="close">
    <div v-if="target" class="admin-operation">
      <p class="muted">管理员「{{ target.username }}」<span v-if="target.username === auth.admin?.username"> · 当前账号</span></p>
      <p v-if="actionError" class="notice err" role="alert">{{ actionError }}</p>
      <form v-if="resetting" @submit.prevent="resetPassword">
        <h3>重置口令</h3>
        <p class="muted small">重置后，该管理员需重新登录。</p>
        <label>新密码
          <input ref="passwordInput" v-model="password" type="password" :disabled="updating" required autocomplete="new-password"
            aria-describedby="admin-password-reset-hint" />
          <small id="admin-password-reset-hint" class="field-hint">8–72 字节。</small>
        </label>
        <div class="dialog-actions">
          <button type="button" :disabled="updating" @click="close">取消</button>
          <button class="danger" type="submit" :disabled="updating">{{ updating ? '提交中…' : '确定重置口令' }}</button>
        </div>
      </form>
      <div v-else class="admin-operation-actions">
        <button :disabled="updating" @click="editPassword">重置口令</button>
        <button v-if="target.username !== auth.admin?.username" class="danger" :disabled="updating" @click="remove">删除管理员</button>
      </div>
    </div>
  </OverlayPanel>
</template>
