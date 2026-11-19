<script setup>
import { computed,onMounted,onBeforeUnmount,ref } from 'vue'
import { useRouter } from 'vue-router'
import { forUser } from '../../api'
import { auth, clearUser } from '../../auth'
const pageUserID=auth.user?.id,api=forUser(pageUserID)
let alive=true
const ownsPage=()=>alive&&auth.user?.id===pageUserID&&!auth.identityChanged
onBeforeUnmount(()=>{alive=false;requestSequence++})
function ensureOwner(){if(ownsPage())return true;error.value='登录账户已变化，请记录未保存输入后重新进入个人中心';return false}
const emit=defineEmits(['logout','maintenance'])
const router=useRouter(),current=ref(''),password=ref(''),confirm=ref(''),error=ref(''),notice=ref(''),busy=ref(false),sessionsBusy=ref(false),sessionsError=ref(''),sessions=ref({items:[],page:1,total:0,size:20})
const dirty=computed(()=>!!(current.value||password.value||confirm.value))
const displayTime=v=>v?new Date(v).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'信息未知'
function reset(){current.value='';password.value='';confirm.value=''}
async function change(){if(busy.value||!ensureOwner())return;error.value='';if(password.value!==confirm.value){error.value='两次输入的新密码不一致';return};busy.value=true;try{await api.userPassword({current_password:current.value,password:password.value});if(!ownsPage())return;reset();clearUser({announce:true});await router.replace('/login')}catch(e){if(ownsPage())error.value=e.message}finally{busy.value=false}}
let requestSequence=0
async function load(page=1){if(!ensureOwner())return;const seq=++requestSequence;sessionsBusy.value=true;sessionsError.value='';try{const result=await api.loginSessions(page);if(ownsPage()&&seq===requestSequence)sessions.value=result}catch(e){if(ownsPage()&&seq===requestSequence)sessionsError.value=e.message}finally{if(ownsPage()&&seq===requestSequence)sessionsBusy.value=false}}
async function revoke(item){if(sessionsBusy.value||item.current||!ensureOwner())return;if(!window.confirm('撤销这份登录会话后，该设备需要重新登录。确定撤销吗？'))return;sessionsBusy.value=true;sessionsError.value='';try{await api.revokeSession(item.public_id);if(!ownsPage())return;await load(Math.max(1,Math.min(sessions.value.page,Math.ceil((sessions.value.total-1)/20))))}catch(e){if(ownsPage())sessionsError.value=e.message}finally{sessionsBusy.value=false}}
async function revokeOthers(){if(sessionsBusy.value||!ensureOwner())return;if(!window.confirm('确定退出其他所有登录会话吗？当前设备会保留登录。'))return;sessionsBusy.value=true;sessionsError.value='';try{const result=await api.revokeOtherSessions();if(!ownsPage())return;notice.value=`已退出 ${result.revoked} 个其他会话，当前会话已保留`;await load()}catch(e){if(ownsPage())sessionsError.value=e.message}finally{sessionsBusy.value=false}}
onMounted(()=>load())
defineExpose({dirty,busy,reset})
</script>
<template>
  <div class="pc-security">
    <form class="pc-card pc-password-form" @submit.prevent="change">
      <h2>修改密码</h2>
      <p class="muted">修改成功后会退出所有设备，请重新登录。</p>
      <fieldset :disabled="busy">
        <label>当前密码<input v-model="current" required type="password" autocomplete="current-password" /></label>
        <label>新密码<input v-model="password" required minlength="8" type="password" autocomplete="new-password" /></label>
        <label>确认新密码<input v-model="confirm" required minlength="8" type="password" autocomplete="new-password" /></label>
        <button type="submit" class="primary">{{ busy ? '正在处理…' : '修改密码' }}</button>
      </fieldset>
      <p v-if="error" class="notice err" role="alert">{{ error }}</p>
    </form>

    <section class="pc-card">
      <div class="pc-card-heading">
        <div>
          <h2>活跃登录会话</h2>
          <p class="muted small">来源地址已脱敏，登录与过期时间为北京时间。</p>
        </div>
        <button :disabled="sessionsBusy || !sessions.total" @click="revokeOthers">退出其他会话</button>
      </div>
      <p v-if="sessionsBusy" role="status">正在读取会话…</p>
      <p v-if="sessionsError" class="notice err" role="alert">
        {{ sessionsError }} <button :disabled="sessionsBusy" @click="load(sessions.page)">重试</button>
      </p>
      <p v-if="notice" class="notice ok" role="status">{{ notice }}</p>
      <ul class="pc-session-list">
        <li v-for="item in sessions.items" :key="item.public_id">
          <div>
            <h3>{{ item.device_label || '历史会话 / 信息未知' }} <span v-if="item.current" class="pc-tag">当前设备</span></h3>
            <p>登录 {{ displayTime(item.created_at) }} · 过期 {{ displayTime(item.expires_at) }}</p>
            <p class="muted small">来源 {{ item.ip_hint || '信息未知' }}</p>
          </div>
          <button v-if="!item.current" :disabled="sessionsBusy" @click="revoke(item)">撤销此会话</button>
        </li>
      </ul>
      <p v-if="!sessionsBusy && !sessionsError && !sessions.items.length" class="muted">暂无活跃会话记录。</p>
      <nav v-if="sessions.total > 20" class="pc-session-pagination" aria-label="登录会话分页">
        <button :disabled="sessionsBusy || sessions.page <= 1" @click="load(sessions.page - 1)">上一页</button>
        <span>第 {{ sessions.page }} 页 · {{ sessions.total }} 个会话</span>
        <button :disabled="sessionsBusy || sessions.page * 20 >= sessions.total" @click="load(sessions.page + 1)">下一页</button>
      </nav>
    </section>

    <section class="pc-card">
      <h2>退出登录</h2>
      <p class="muted">退出当前设备后，其他登录会话仍然有效。</p>
      <button type="button" @click="emit('logout')">退出当前设备</button>
    </section>
    <button type="button" class="text-button" @click="emit('maintenance')">前往学习数据维护</button>
  </div>
</template>
