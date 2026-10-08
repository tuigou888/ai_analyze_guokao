import { createRouter, createWebHistory } from 'vue-router'
import { api } from '../api'
import { auth, clearUser, setUser, loadProfile } from '../auth'
const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: () => ({ top: 0 }),
  routes: [
    { path: '/', redirect: '/questions' },
    { path: '/login', component: () => import('../views/Login.vue'), meta: { public: true } },
    { path: '/admin/login', component: () => import('../views/Login.vue'), meta: { public: true, admin: true } },
    { path: '/admin', component: () => import('../views/Admin.vue'), meta: { admin: true, title: '管理设置' } },
    { path: '/questions', component: () => import('../views/QuestionList.vue'), meta: { title: '题库练习' } },
    { path: '/papers', component: () => import('../views/Papers.vue'), meta: { title: '真题试卷' } },
    { path: '/practice', component: () => import('../views/Practice.vue'), meta: { title: '练习室' } },
    { path: '/wrongbook', component: () => import('../views/WrongBook.vue'), meta: { title: '错题本' } },
    { path: '/favorites', component: () => import('../views/Favorites.vue'), meta: { title: '我的收藏' } },
    { path: '/concepts', component: () => import('../views/ConceptList.vue'), meta: { title: '考点地图' } },
    { path: '/concepts/:id', component: () => import('../views/ConceptDetail.vue'), meta: { title: '考点卡片' } },
    { path: '/records', component: () => import('../views/Records.vue'), meta: { title: '学习记录' } },
    { path: '/account', component: () => import('../views/Account.vue'), meta: { title: '个人中心' } },
    { path: '/:pathMatch(.*)*', component: () => import('../views/NotFound.vue'), meta: { public: true } },
  ],
})
router.beforeEach(async to => {
  document.title = `${to.meta.title || '登录'} · 行测研习`
  if (to.meta.public) return true
  try {
    if (to.meta.admin) auth.admin = await api.me()
    else { setUser(await api.userMe()); await loadProfile().catch(e => { if(e.status === 401) throw e }) }
  } catch (e) {
    if (e.status !== 401) throw e
    if (!to.meta.admin) clearUser()
    return { path: to.meta.admin ? '/admin/login' : '/login', query: { redirect: to.fullPath } }
  }
})
window.addEventListener('session-expired', e => {
  const admin = e.detail === 'admin'
  if (!admin && auth.identityChanged) return
  if (!admin && typeof e.detail === 'object' && e.detail.expectedUser !== auth.user?.id) return
  if (admin) auth.admin = null
  else clearUser()
  router.replace({ path: admin ? '/admin/login' : '/login', query: { redirect: router.currentRoute.value.fullPath } })
})
export default router
