<script setup>
import { computed,onBeforeUnmount,onMounted,ref,nextTick } from 'vue'
import { onBeforeRouteLeave,onBeforeRouteUpdate } from 'vue-router'
import { forUser } from '../api'
import { auth,applyProfile } from '../auth'
import Avatar from '../components/account/Avatar.vue'
import Overview from '../components/account/Overview.vue'
import Review from '../components/account/Review.vue'
import ProfileForm from '../components/account/ProfileForm.vue'
import Security from '../components/account/Security.vue'
const api=forUser(auth.user?.id)
const emit=defineEmits(['logout'])
const tabs=['学习总览','复习助手','资料与偏好','账户安全'],tab=ref(0),tablist=ref(null),days=ref(30),profile=ref(auth.profile),dashboard=ref(null),busy=ref(false),error=ref(''),profileError=ref(''),form=ref(null),security=ref(null)
let loadGeneration=0,alive=true
const pageUserID=auth.user?.id,pageUsername=auth.user?.username
const ownsPage=()=>alive&&auth.user?.id===pageUserID
const nickname=computed(()=>profile.value?.nickname || auth.user?.username || '研习者')
const countdown=computed(()=>{if(!profile.value?.exam_date)return '';const today=new Intl.DateTimeFormat('en-CA',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date());const remaining=Math.round((Date.parse(profile.value.exam_date+'T00:00:00+08:00')-Date.parse(today+'T00:00:00+08:00'))/86400000);return remaining>0?`距离考试还有 ${remaining} 天`:remaining===0?'考试日期已到':'考试日期已过'})
async function load(){const generation=++loadGeneration;busy.value=true;error.value='';try{const data=await api.dashboard(days.value);if(ownsPage()&&generation===loadGeneration){if(data.profile.username!==pageUsername){error.value='登录账户已变化，请重新进入个人中心';return};dashboard.value=data;saved(data.profile)}}catch(e){if(ownsPage()&&generation===loadGeneration)error.value=e.message}finally{if(ownsPage()&&generation===loadGeneration)busy.value=false}}
async function reloadProfile(){profileError.value='';try{const p=await api.profile();if(ownsPage()&&!saved(p))profileError.value='登录账户已变化，请重新进入个人中心'}catch(e){if(ownsPage())profileError.value=e.message}}
function saved(p){if(!ownsPage()||p.username!==pageUsername||!applyProfile(p,pageUserID))return false;profile.value=auth.profile;if(dashboard.value)dashboard.value.profile=auth.profile;return true}
function guard(reset=true){if(!auth.user)return true;if(form.value?.busy||security.value?.busy)return false;if(form.value?.dirty||security.value?.dirty){if(!window.confirm('有尚未保存的资料或密码输入。取消可返回保存；确定将放弃修改并离开。'))return false;if(reset){form.value?.reset();security.value?.reset()}}return true}
function switchTab(i){if(i===tab.value)return true;if(!guard())return false;tab.value=i;return true}
async function tabKey(e,i){let target;if(e.key==='ArrowRight')target=(i+1)%4;else if(e.key==='ArrowLeft')target=(i+3)%4;else if(e.key==='Home')target=0;else if(e.key==='End')target=3;else return;e.preventDefault();if(switchTab(target)){await nextTick();tablist.value?.querySelectorAll('button')[target]?.focus()}}
function beforeUnload(e){if(form.value?.dirty||security.value?.dirty||form.value?.busy||security.value?.busy){e.preventDefault();e.returnValue=''}}
onBeforeRouteLeave(()=>guard())
onBeforeRouteUpdate(()=>guard())
defineExpose({savePending:async()=>{if(auth.identityChanged)throw new Error('登录账户已变化，请记录未保存输入后重新进入个人中心');if(!guard(false))throw new Error('尚未保存的资料已保留，请保存后再退出')}})
onMounted(()=>{load();if(!profile.value)reloadProfile();window.addEventListener('beforeunload',beforeUnload)})
onBeforeUnmount(()=>{alive=false;loadGeneration++;window.removeEventListener('beforeunload',beforeUnload)})
</script>
<template>
<div class="pc-account">
  <header class="pc-heading"><div class="pc-identity"><Avatar :id="profile?.avatar_id || 0" :name="nickname" large/><div><p class="eyebrow">个人中心 / 学习工作台</p><h1>{{ nickname }}，今天继续研习</h1><p class="muted">{{ profile?.username || auth.user?.username }} · {{ profile?.created_at ? profile.created_at.slice(0,10)+' 加入' : '你的个人学习空间' }}</p><p v-if="profile?.bio" class="pc-bio">{{ profile.bio }}</p></div></div><div class="pc-exam" v-if="countdown"><span>{{ profile.exam_name || '备考计划' }}</span><strong>{{ countdown }}</strong><button class="text-button" @click="switchTab(2)">编辑考试信息</button></div><div class="pc-exam" v-else><span>把下一步写进计划</span><button @click="switchTab(2)">设置考试与目标</button></div></header>
  <div class="pc-tabs" role="tablist" aria-label="个人中心页面" ref="tablist"><button v-for="(name,i) in tabs" :key="name" role="tab" :id="`pc-tab-${i}`" :aria-controls="`pc-panel-${i}`" :aria-selected="tab===i" :tabindex="tab===i?0:-1" @click="switchTab(i)" @keydown="tabKey($event,i)">{{ name }}</button></div>
  <div class="pc-range" v-show="tab<=1"><span>复盘范围</span><div role="group" aria-label="统计范围"><button v-for="value in [7,30,90]" :key="value" :aria-pressed="days===value" @click="days=value;load()">{{ value }} 天</button></div><span class="muted small">{{ busy?'正在更新…':'北京时间自然日' }}</span></div>
  <p v-if="profileError" class="notice err" role="alert">资料加载失败：{{ profileError }} <button @click="reloadProfile">重试资料</button></p><div v-if="error&&tab<=1" class="notice err" role="alert">学习数据加载失败：{{ error }} <button @click="load">重试学习数据</button></div><p v-if="!dashboard&&busy&&tab<=1" class="pc-empty" role="status">正在读取你的学习记录…</p>
  <section v-show="tab===0" role="tabpanel" id="pc-panel-0" aria-labelledby="pc-tab-0" tabindex="0"><Overview v-if="dashboard&&profile" :dashboard="dashboard" :profile="profile" @goals="switchTab(2)"/></section>
  <section v-show="tab===1" role="tabpanel" id="pc-panel-1" aria-labelledby="pc-tab-1" tabindex="0"><Review v-if="dashboard&&profile" :review="dashboard.review" :profile="profile" :days="dashboard.days"/></section>
  <section v-show="tab===2" role="tabpanel" id="pc-panel-2" aria-labelledby="pc-tab-2" tabindex="0"><ProfileForm v-if="profile" ref="form" :profile="profile" @saved="saved"/></section>
  <section v-show="tab===3" role="tabpanel" id="pc-panel-3" aria-labelledby="pc-tab-3" tabindex="0"><Security ref="security" @logout="emit('logout')"/></section>
</div>
</template>
