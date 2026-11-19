<script setup>
import { computed,onBeforeUnmount,onMounted,ref,nextTick } from 'vue'
import { onBeforeRouteLeave,onBeforeRouteUpdate,useRoute,useRouter,isNavigationFailure } from 'vue-router'
import { forUser } from '../api'
import { auth,applyProfile } from '../auth'
import Avatar from '../components/account/Avatar.vue'
import Overview from '../components/account/Overview.vue'
import Review from '../components/account/Review.vue'
import ProfileForm from '../components/account/ProfileForm.vue'
import Security from '../components/account/Security.vue'
const api=forUser(auth.user?.id)
const emit=defineEmits(['logout'])
const tabs = [
  { key: 'overview', label: '学习总览' },
  { key: 'review', label: '复习助手' },
  { key: 'profile', label: '资料与偏好' },
  { key: 'security', label: '账户安全' },
]
const route = useRoute(), router = useRouter()
const tabFromRoute = location => Math.max(0, tabs.findIndex(item => item.key === location.query.tab))
const tab = computed(() => tabFromRoute(route))
const tablist=ref(null),days=ref(30),profile=ref(auth.profile),dashboard=ref(null),busy=ref(false),error=ref(''),profileError=ref(''),form=ref(null),security=ref(null)
const maintenance = ref(null), rebuilding = ref(false), maintenanceError = ref(''), maintenanceNotice = ref('')
let loadGeneration=0,alive=true
const pageUserID=auth.user?.id,pageUsername=auth.user?.username
const ownsPage=()=>alive&&auth.user?.id===pageUserID&&!auth.identityChanged
const nickname=computed(()=>profile.value?.nickname || auth.user?.username || '研习者')
const countdown=computed(()=>{if(!profile.value?.exam_date)return '';const today=new Intl.DateTimeFormat('en-CA',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date());const remaining=Math.round((Date.parse(profile.value.exam_date+'T00:00:00+08:00')-Date.parse(today+'T00:00:00+08:00'))/86400000);return remaining>0?`距离考试还有 ${remaining} 天`:remaining===0?'考试日期已到':'考试日期已过'})
async function load(){const generation=++loadGeneration;busy.value=true;error.value='';try{const data=await api.dashboard(days.value);if(ownsPage()&&generation===loadGeneration){if(data.profile.username!==pageUsername){error.value='登录账户已变化，请重新进入个人中心';return};dashboard.value=data;saved(data.profile)}}catch(e){if(ownsPage()&&generation===loadGeneration)error.value=e.message}finally{if(ownsPage()&&generation===loadGeneration)busy.value=false}}
async function reloadProfile(){profileError.value='';try{const p=await api.profile();if(ownsPage()&&!saved(p))profileError.value='登录账户已变化，请重新进入个人中心'}catch(e){if(ownsPage())profileError.value=e.message}}
function saved(p){if(!ownsPage()||p.username!==pageUsername||!applyProfile(p,pageUserID))return false;profile.value=auth.profile;if(dashboard.value)dashboard.value.profile=auth.profile;return true}
function guard(reset=true){if(!auth.user)return true;if(form.value?.busy||security.value?.busy)return false;if(form.value?.dirty||security.value?.dirty){if(!window.confirm('有尚未保存的资料或密码输入。取消可返回保存；确定将放弃修改并离开。'))return false;if(reset){form.value?.reset();security.value?.reset()}}return true}
async function switchTab(i) {
  if (i === tab.value) return true
  const failure = await router.push({ path: route.path, query: { ...route.query, tab: tabs[i].key } })
  return !isNavigationFailure(failure)
}
async function tabKey(e, i) {
  let target
  if (e.key === 'ArrowRight') target = (i + 1) % tabs.length
  else if (e.key === 'ArrowLeft') target = (i + tabs.length - 1) % tabs.length
  else if (e.key === 'Home') target = 0
  else if (e.key === 'End') target = tabs.length - 1
  else return
  e.preventDefault()
  if (await switchTab(target)) {
    await nextTick()
    tablist.value?.querySelectorAll('button')[target]?.focus()
  }
}
async function openMaintenance() {
  if (!await switchTab(1)) return
  await nextTick()
  requestAnimationFrame(() => maintenance.value?.focus())
}
async function rebuild() {
  if (rebuilding.value) return
  maintenanceError.value = ''
  maintenanceNotice.value = ''
  if (!ownsPage()) {
    maintenanceError.value = '登录账户已变化，请重新进入个人中心'
    return
  }
  rebuilding.value = true
  try {
    await api.rebuildStats()
    if (ownsPage()) maintenanceNotice.value = '考点练习统计已重新计算'
  } catch (e) {
    if (ownsPage()) maintenanceError.value = e.message
  } finally {
    rebuilding.value = false
  }
}
function beforeUnload(e){if(form.value?.dirty||security.value?.dirty||form.value?.busy||security.value?.busy){e.preventDefault();e.returnValue=''}}
onBeforeRouteLeave(()=>guard())
onBeforeRouteUpdate(to => tabFromRoute(to) === tab.value || guard())
defineExpose({savePending:async()=>{if(auth.identityChanged)throw new Error('登录账户已变化，请记录未保存输入后重新进入个人中心');if(!guard(false))throw new Error('尚未保存的资料已保留，请保存后再退出')}})
onMounted(()=>{load();if(!profile.value)reloadProfile();window.addEventListener('beforeunload',beforeUnload)})
onBeforeUnmount(()=>{alive=false;loadGeneration++;window.removeEventListener('beforeunload',beforeUnload)})
</script>
<template>
<div class="pc-account">
  <header class="pc-heading"><div class="pc-identity"><Avatar :id="profile?.avatar_id || 0" :name="nickname" large/><div><h1>{{ nickname }}，今天继续研习</h1><p class="muted">{{ profile?.username || auth.user?.username }} · {{ profile?.created_at ? profile.created_at.slice(0,10)+' 加入' : '你的个人学习空间' }}</p><p v-if="profile?.bio" class="pc-bio">{{ profile.bio }}</p></div></div><div class="pc-exam" v-if="countdown"><span>{{ profile.exam_name || '备考计划' }}</span><strong>{{ countdown }}</strong><button class="text-button" @click="switchTab(2)">编辑考试信息</button></div><div class="pc-exam" v-else><button @click="switchTab(2)">设置考试与目标</button></div></header>
  <div class="pc-tabs" role="tablist" aria-label="个人中心页面" ref="tablist"><button v-for="(item,i) in tabs" :key="item.key" role="tab" :id="`pc-tab-${i}`" :aria-controls="`pc-panel-${i}`" :aria-selected="tab===i" :tabindex="tab===i?0:-1" @click="switchTab(i)" @keydown="tabKey($event,i)">{{ item.label }}</button></div>
  <div class="pc-range" v-show="tab<=1"><span>统计范围</span><div role="group" aria-label="统计范围"><button v-for="value in [7,30,90]" :key="value" :aria-pressed="days===value" @click="days=value;load()">{{ value }} 天</button></div><span class="muted small">{{ busy?'正在更新…':'按北京时间统计' }}</span></div>
  <p v-if="profileError" class="notice err" role="alert">资料加载失败：{{ profileError }} <button @click="reloadProfile">重试资料</button></p><div v-if="error&&tab<=1" class="notice err" role="alert">学习数据加载失败：{{ error }} <button @click="load">重试学习数据</button></div><p v-if="!dashboard&&busy&&tab<=1" class="pc-empty" role="status">正在读取你的学习记录…</p>
  <section v-show="tab===0" role="tabpanel" id="pc-panel-0" aria-labelledby="pc-tab-0" tabindex="0"><Overview v-if="dashboard&&profile" :dashboard="dashboard" :profile="profile" @goals="switchTab(2)"/></section>
  <section v-show="tab===1" role="tabpanel" id="pc-panel-1" aria-labelledby="pc-tab-1" tabindex="0">
    <Review v-if="dashboard&&profile" :review="dashboard.review" :profile="profile" :days="dashboard.days"/>
    <section ref="maintenance" tabindex="-1" class="pc-card pc-maintenance">
      <div class="pc-card-heading">
        <div>
          <h2>学习数据维护</h2>
          <p class="muted">考点标注更新后，可根据历史作答重新计算考点掌握度。</p>
        </div>
        <button :disabled="rebuilding" @click="rebuild">{{ rebuilding ? '正在计算…' : '重新计算考点统计' }}</button>
      </div>
      <p v-if="maintenanceError" class="notice err" role="alert">{{ maintenanceError }}</p>
      <p v-if="maintenanceNotice" class="notice ok" role="status">{{ maintenanceNotice }}</p>
    </section>
  </section>
  <section v-show="tab===2" role="tabpanel" id="pc-panel-2" aria-labelledby="pc-tab-2" tabindex="0"><ProfileForm v-if="profile" ref="form" :profile="profile" @saved="saved"/></section>
  <section v-show="tab===3" role="tabpanel" id="pc-panel-3" aria-labelledby="pc-tab-3" tabindex="0"><Security ref="security" @logout="emit('logout')" @maintenance="openMaintenance"/></section>
</div>
</template>
