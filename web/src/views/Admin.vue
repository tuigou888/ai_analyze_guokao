<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import Settings from './Settings.vue'
import Users from './admin/Users.vue'
import Admins from './admin/Admins.vue'
import { api } from '../api'
import { auth } from '../auth'

const emit = defineEmits(['logout'])
const route = useRoute(), router = useRouter()
const isSuper = computed(() => auth.admin?.role === 'super')
const sections = {
  users: { title: '用户管理', description: '查找学习账户，管理状态与会员权限。' },
  admins: { title: '管理员账号', description: '创建管理员，管理后台访问权限。', super: true },
  settings: { title: '系统配置', description: '管理模型接口、邮件服务与使用额度。', super: true },
  maintenance: { title: '数据维护', description: '刷新已有考点标注生成的数据。' },
  account: { title: '我的账户', description: '管理当前管理员的密码与登录。' },
}
function tabFromRoute(location) {
  const key = location.query.tab
  if (typeof key === 'string' && Object.hasOwn(sections, key) && (!sections[key].super || isSuper.value)) return key
  return isSuper.value ? 'settings' : 'users'
}
const tab = computed(() => tabFromRoute(route))
const section = computed(() => sections[tab.value])
const panel = ref(null)
const current = ref(''), password = ref('')
const changing = ref(false), passwordError = ref('')
const refreshing = ref(false), refreshError = ref(''), refreshNotice = ref('')
const dirty = computed(() => Boolean(current.value || password.value || panel.value?.dirty))
const busy = computed(() => changing.value || refreshing.value || panel.value?.busy)

function guard() {
  if (!auth.admin) return true
  if (busy.value) return false
  return !dirty.value || window.confirm('仍有未保存的输入。确定放弃修改并离开吗？')
}

onBeforeRouteLeave(guard)
onBeforeRouteUpdate(to => tabFromRoute(to) === tab.value || guard())
watch(() => route.query.tab, key => {
  if (route.path === '/admin' && key !== undefined && key !== tab.value) {
    router.replace({ path: '/admin', query: { ...route.query, tab: tab.value } })
  }
}, { immediate: true })
watch(tab, () => {
  current.value = ''
  password.value = ''
  passwordError.value = ''
})

function beforeUnload(event) {
  if (dirty.value || busy.value) {
    event.preventDefault()
    event.returnValue = ''
  }
}
onMounted(() => window.addEventListener('beforeunload', beforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))

defineExpose({
  savePending() {
    if (!guard()) throw new Error('输入已保留，请完成或放弃修改后退出')
  },
})

async function changePassword() {
  if (changing.value) return
  changing.value = true
  passwordError.value = ''
  try {
    await api.changePassword(current.value, password.value)
    auth.admin = null
    await router.replace('/admin/login')
  } catch (e) {
    passwordError.value = e.message
  } finally {
    changing.value = false
  }
}

async function refreshConcepts() {
  if (refreshing.value) return
  refreshing.value = true
  refreshError.value = ''
  refreshNotice.value = ''
  try {
    await api.refreshConcepts()
    refreshNotice.value = '已更新现有标注的考点映射与题量'
  } catch (e) {
    refreshError.value = e.message
  } finally {
    refreshing.value = false
  }
}
</script>

<template>
  <div class="admin-workspace">
    <header class="page-heading">
      <div>
        <h1>{{ section.title }}</h1>
        <p class="muted">{{ section.description }}</p>
      </div>
    </header>

    <Settings v-if="tab === 'settings' && isSuper" ref="panel" />
    <Users v-else-if="tab === 'users'" ref="panel" />
    <Admins v-else-if="tab === 'admins' && isSuper" ref="panel" />

    <section v-else-if="tab === 'maintenance'" class="card admin-narrow">
      <h2>更新考点映射</h2>
      <p class="muted">根据已有标注刷新考点映射与关联题量，保持考点编号稳定。</p>
      <button type="button" class="primary" :disabled="refreshing" @click="refreshConcepts">
        {{ refreshing ? '正在更新…' : '更新考点映射' }}
      </button>
      <p v-if="refreshError" class="notice err" role="alert">{{ refreshError }}</p>
      <p v-if="refreshNotice" class="notice ok" role="status">{{ refreshNotice }}</p>
    </section>

    <div v-else-if="tab === 'account'" class="admin-narrow">
      <form class="card admin-password" @submit.prevent="changePassword">
        <h2>修改管理员密码</h2>
        <p class="muted">修改成功后将注销该管理员的所有旧会话。</p>
        <fieldset :disabled="changing">
          <label>当前密码<input v-model="current" type="password" required autocomplete="current-password" /></label>
          <label>新密码<input v-model="password" type="password" required minlength="8" autocomplete="new-password" /></label>
          <button type="submit" class="primary">{{ changing ? '正在修改…' : '修改并重新登录' }}</button>
        </fieldset>
        <p v-if="passwordError" class="notice err" role="alert">{{ passwordError }}</p>
      </form>
      <section class="card admin-signout">
        <div><h2>当前登录</h2><p class="muted">{{ auth.admin?.username }}</p></div>
        <button type="button" @click="emit('logout')">退出管理后台</button>
      </section>
      <RouterLink to="/admin?tab=maintenance">前往数据维护</RouterLink>
    </div>
  </div>
</template>
