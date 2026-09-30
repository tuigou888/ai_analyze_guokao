<script setup>
import { useRoute, useRouter } from 'vue-router'
import { auth } from './auth'
import { api } from './api'
import { ref } from 'vue'
const route = useRoute(), router = useRouter(), error = ref(''), currentView = ref(null)
const links = [['/questions','题库练习','题'],['/papers','真题试卷','卷'],['/concepts','考点地图','知'],['/wrongbook','错题本','错'],['/favorites','我的收藏','藏'],['/records','学习记录','记']]
async function logout() {
  try {
    if (!route.meta.admin) await currentView.value?.savePending?.()
    if (route.meta.admin) { await api.logout(); auth.admin = null; await router.push('/admin/login') }
    else { await api.userLogout(); auth.user = null; await router.push('/login') }
  } catch(e) { error.value = e.message }
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
      <div class="sidebar-bottom"><RouterLink to="/account">账户设置</RouterLink><RouterLink to="/admin">管理入口</RouterLink></div>
    </aside>
    <div class="workspace">
      <header class="topbar"><span>{{ route.meta.title }}</span><div class="topbar-user"><span class="avatar">{{ (route.meta.admin ? auth.admin?.username : auth.user?.nickname || auth.user?.username)?.slice(0,1)?.toUpperCase() }}</span><span>{{ route.meta.admin ? auth.admin?.username : auth.user?.nickname || auth.user?.username }}</span><button class="text-button" @click="logout">退出</button></div></header>
      <main id="main" tabindex="-1"><p v-if="error" class="notice err" role="alert">{{ error }}</p><RouterView v-slot="{ Component }"><component :is="Component" :key="route.fullPath" ref="currentView" /></RouterView></main>
      <footer class="site-footer">行测研习 <span>以真题为起点，以理解为进步。</span></footer>
    </div>
  </div>
</template>
