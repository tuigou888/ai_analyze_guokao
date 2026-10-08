<script setup>
import { useRoute, useRouter } from 'vue-router'
import { auth, clearUser } from './auth'
import { api, forUser } from './api'
import { ref, watch } from 'vue'
const route = useRoute(), router = useRouter(), error = ref(''), currentView = ref(null)
watch(() => route.fullPath, () => { error.value = '' })
const links = [['/questions','题库练习','题'],['/papers','真题试卷','卷'],['/concepts','考点地图','知'],['/wrongbook','错题本','错'],['/favorites','我的收藏','藏'],['/records','学习记录','记']]
async function logout() {
  const admin = !!route.meta.admin, owner = admin ? auth.admin : auth.user?.id
  const client = forUser(owner)
  const ownsAction = () => admin ? auth.admin === owner : auth.user?.id === owner && !auth.identityChanged
  error.value = ''
  try {
    if (!admin) await currentView.value?.savePending?.()
    if (!ownsAction()) return
    if (admin) { await api.logout(); if (!ownsAction()) return; auth.admin = null; await router.push('/admin/login') }
    else { await client.userLogout(); if (!ownsAction()) return; clearUser({announce:true}); await router.push('/login') }
  } catch(e) { if (admin ? auth.admin === owner : auth.user?.id === owner) error.value = e.message }
}

</script>
<template>
  <a href="#main" class="skip">跳到正文</a>
  <RouterView v-if="route.meta.public" />
  <div v-else class="app-shell">
    <aside class="sidebar">
      <RouterLink class="brand" to="/questions"><span class="brand-mark">行</span><span><strong>行测研习</strong><small>真题里的每一步</small></span></RouterLink>
      <p class="nav-label">学习空间</p>
      <nav aria-label="主导航">
        <RouterLink v-for="[to,label,symbol] in links" :key="to" :to="to"><span class="nav-symbol" aria-hidden="true">{{ symbol }}</span>{{ label }}</RouterLink>
      </nav>
      <div class="sidebar-note"><span class="small-label">研习笔记</span><p>看懂一道题，<br>再走稳下一步。</p><span class="muted">练习 · 复盘 · 归纳</span></div>
      <div class="sidebar-bottom"><RouterLink to="/account">个人中心</RouterLink><RouterLink to="/admin">管理入口</RouterLink></div>
    </aside>
    <div class="workspace">
      <header class="topbar"><span>{{ route.meta.title }}</span><div class="topbar-user"><RouterLink v-if="!route.meta.admin" to="/account" class="pc-topbar-link" aria-label="打开个人中心"><span :class="['avatar','pc-avatar',`pc-avatar-${auth.profile?.avatar_id || 0}`]">{{ Array.from(auth.user?.nickname || auth.user?.username || '?')[0]?.toUpperCase() }}</span><span>{{ auth.user?.nickname || auth.user?.username }}</span></RouterLink><span v-else>{{ auth.admin?.username }}</span><button class="text-button" @click="logout">退出</button></div></header>
      <main id="main" tabindex="-1"><p v-if="auth.identityChanged&&!route.meta.admin" class="notice warn" role="alert">登录账户已变化，请记录未保存输入后重新进入个人中心。本页输入已保留。 <a href="/account">重新进入个人中心</a></p><p v-if="error" class="notice err" role="alert">{{ error }}</p><RouterView v-slot="{ Component }"><component :is="Component" :key="route.fullPath" ref="currentView" @logout="logout" /></RouterView></main>
      <footer class="site-footer">行测研习 <span>以真题为起点，以理解为进步。</span></footer>
    </div>
  </div>
</template>
