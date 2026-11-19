<script setup>
import { useRoute, useRouter } from 'vue-router'
import { auth, clearUser } from './auth'
import { api, forUser } from './api'
import SidebarNav from './components/SidebarNav.vue'
import OverlayPanel from './components/OverlayPanel.vue'
import ToastHost from './components/ToastHost.vue'
import { ref, watch } from 'vue'
const route = useRoute(), router = useRouter(), error = ref(''), currentView = ref(null)
watch(() => route.fullPath, () => { error.value = ''; navigationOpen.value=false })
const collapsed=ref(false),navigationOpen=ref(false)
async function logout() {
  const admin = !!route.meta.admin, owner = admin ? auth.admin : auth.user?.id
  const client = forUser(owner)
  const ownsAction = () => admin ? auth.admin === owner : auth.user?.id === owner && !auth.identityChanged
  error.value = ''
  try {
    await currentView.value?.savePending?.()
    if (!ownsAction()) return
    if (admin) { await api.logout(); if (!ownsAction()) return; auth.admin = null; await router.push('/admin/login') }
    else { await client.userLogout(); if (!ownsAction()) return; clearUser({announce:true}); await router.push('/login') }
  } catch(e) { if (admin ? auth.admin === owner : auth.user?.id === owner) error.value = e.message }
}

</script>
<template>
  <a href="#main" class="skip">跳到正文</a>
  <ToastHost />
  <RouterView v-if="route.meta.public" />
  <div v-else :class="['app-shell',{'sidebar-collapsed':collapsed}]">
    <aside class="sidebar">
      <RouterLink class="brand" to="/questions" title="行测研习"><span class="brand-mark">行</span><span class="brand-copy"><strong>行测研习</strong><small>专注真题 · 系统提升</small></span></RouterLink>
      <SidebarNav />
      <div class="sidebar-note"><p>看懂一道题，<br>再走稳下一步。</p></div>
      <button class="sidebar-collapse" :aria-label="collapsed?'展开侧栏':'折叠侧栏'" :aria-expanded="!collapsed" @click="collapsed=!collapsed"><span aria-hidden="true">{{ collapsed?'»':'«' }}</span><span class="nav-text">收起导航</span></button>
    </aside>
    <OverlayPanel :open="navigationOpen" title="行测研习" drawer @close="navigationOpen=false"><div class="mobile-navigation"><SidebarNav @navigate="navigationOpen=false" /></div></OverlayPanel>
    <div class="workspace">
      <header class="topbar"><div class="topbar-context"><button class="mobile-nav-toggle" aria-label="展开导航" @click="navigationOpen=true">☰</button><span>{{ route.meta.admin?'管理后台':route.path==='/account'?'个人空间':['/wrongbook','/favorites','/records'].includes(route.path)?'复习与记录':'学习中心' }} <span aria-hidden="true">/</span> {{ route.meta.title }}</span></div><div class="topbar-user"><RouterLink v-if="!route.meta.admin" to="/account" class="pc-topbar-link" aria-label="打开个人中心"><span :class="['avatar','pc-avatar',`pc-avatar-${auth.profile?.avatar_id || 0}`]">{{ Array.from(auth.user?.nickname || auth.user?.username || '?')[0]?.toUpperCase() }}</span><span v-if="auth.user?.role === 'vip'" class="vip-badge">{{ auth.user.vip_expires_at ? `会员至 ${auth.user.vip_expires_at.slice(0,10)}` : '会员' }}</span><span>{{ auth.user?.nickname || auth.user?.username }}</span></RouterLink><span v-else>{{ auth.admin?.username }}</span><button class="text-button" @click="logout">退出</button></div></header>
      <main id="main" tabindex="-1"><p v-if="auth.identityChanged&&!route.meta.admin" class="notice warn" role="alert">登录账户已变化，请记录未保存输入后重新进入个人中心。本页输入已保留。 <a href="/account">重新进入个人中心</a></p><p v-if="error" class="notice err" role="alert">{{ error }}</p><RouterView v-slot="{ Component }"><component :is="Component" :key="route.meta.admin ? `${route.path}:${auth.admin?.username}:${auth.admin?.role}` : ['/account','/papers','/concepts'].includes(route.path) ? route.path : route.fullPath" ref="currentView" v-on="['/account','/admin'].includes(route.path)?{logout}:{}" /></RouterView></main>
      <footer class="site-footer">行测研习 <span>以真题为起点，以理解为进步。</span></footer>
    </div>
  </div>
</template>
<style scoped>
.vip-badge {
  display: inline-flex;
  align-items: center;
  flex: none;
  padding: var(--space-1) var(--space-2);
  background: var(--color-warning-surface);
  color: var(--color-warning);
  border-radius: var(--radius-small);
  font-size: var(--font-size-xs);
  font-weight: 600;
  white-space: nowrap;
}
</style>
