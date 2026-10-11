<script setup>
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api'
import { auth, setUser } from '../auth'
const route = useRoute(), router = useRouter()
const admin = computed(() => !!route.meta.admin), register = ref(false)
const username = ref(''), password = ref(''), nickname = ref(''), busy = ref(false), error = ref('')
const email = ref(''), code = ref(''), codeSent = ref(false), codeCountdown = ref(0)
let countdownTimer = null
function startCountdown() {
  codeCountdown.value = 60
  clearInterval(countdownTimer)
  countdownTimer = setInterval(() => {
    codeCountdown.value--
    if (codeCountdown.value <= 0) clearInterval(countdownTimer)
  }, 1000)
}
async function sendCode() {
  if (!email.value || codeCountdown.value > 0) return
  error.value = ''
  try {
    await api.emailCode(email.value)
    codeSent.value = true
    startCountdown()
  } catch (e) {
    error.value = e.message
  }
}
async function submit() {
  busy.value = true; error.value = ''
  try {
    if (register.value && !admin.value) {
      if (!email.value || !code.value) { error.value = '请填写邮箱与验证码'; busy.value = false; return }
      await api.register({ username: username.value, password: password.value, nickname: nickname.value, email: email.value, code: code.value })
    }
    if (admin.value) auth.admin = await api.login(username.value, password.value)
    else setUser(await api.userLogin(username.value, password.value), { announce: true })
    const to = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/') && !route.query.redirect.startsWith('//') ? route.query.redirect : admin.value ? '/admin' : '/questions'
    await router.replace(to)
  } catch(e) { error.value = e.message } finally { busy.value = false }
}
</script>
<template>
  <main id="main" class="login-layout">
    <section class="login-intro"><RouterLink to="/" class="brand"><span class="brand-mark">行</span><strong>行测研习</strong></RouterLink><div><p class="eyebrow">从真题开始，把知识读懂</p><h1>每一次练习，<br>都有迹可循。</h1><p>按模块研习真题，记录作答与错题，<br>把零散的知识，慢慢连成地图。</p><div class="study-note"><span>研习方法</span><ol><li>独立作答</li><li>对照解析</li><li>归纳考点</li></ol></div></div><small>行测研习 · 你的日常学习桌</small></section>
    <section class="login-form-wrap"><form class="login-card" @submit.prevent="submit"><p class="eyebrow">{{ admin ? '管理入口' : '欢迎回到学习空间' }}</p><h2>{{ admin ? '管理员登录' : register ? '创建学习账户' : '开始今天的研习' }}</h2><p class="muted">{{ admin ? '使用独立的管理员账户登录。' : register ? '练习记录、错题与收藏会保存在你的账户中。' : '登录后继续练习，查看你的学习记录。' }}</p>
      <label>用户名<input v-model="username" required minlength="3" maxlength="32" autocomplete="username" placeholder="字母、数字、下划线或短横线" /></label>
      <label v-if="register && !admin">昵称（可选）<input v-model="nickname" maxlength="40" autocomplete="nickname" placeholder="怎么称呼你" /></label>
      <label>密码<input v-model="password" required :minlength="register ? 8 : undefined" type="password" :autocomplete="register ? 'new-password' : 'current-password'" placeholder="输入密码" /></label>
      <template v-if="register && !admin">
        <label>邮箱<input v-model="email" required type="email" autocomplete="email" placeholder="用于接收验证码" /></label>
        <div class="code-row">
          <label class="code-input"><input v-model="code" required minlength="6" maxlength="6" inputmode="numeric" autocomplete="one-time-code" placeholder="6 位验证码" /></label>
          <button type="button" class="ghost" :disabled="!email || codeCountdown > 0" @click="sendCode">{{ codeCountdown > 0 ? codeCountdown + 's' : (codeSent ? '重新发送' : '获取验证码') }}</button>
        </div>
      </template>
      <p v-if="error" class="notice err" role="alert">{{ error }}</p><button class="primary wide" :disabled="busy">{{ busy ? '正在登录…' : register && !admin ? '注册并登录' : '登录' }}</button>
      <p v-if="!admin" class="login-switch">{{ register ? '已有账户？' : '第一次来？' }} <button type="button" class="text-button" @click="register=!register;error='';codeSent=false">{{ register ? '返回登录' : '创建账户' }}</button></p>
      <RouterLink v-if="admin" class="subtle-link" to="/login">返回用户登录</RouterLink><RouterLink v-else class="subtle-link" to="/admin/login">管理员登录</RouterLink>
    </form></section>
  </main>
</template>
<style scoped>
.code-row { display: flex; gap: 8px; align-items: center; margin-top: var(--space-3); }
.code-input { flex: 1; }
.code-input input { width: 100%; }
</style>
