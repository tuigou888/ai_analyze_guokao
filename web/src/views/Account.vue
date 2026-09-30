<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { auth } from '../auth'
import { api } from '../api'
const current=ref(''),password=ref(''),confirm=ref(''),error=ref(''),notice=ref(''),busy=ref(false),router=useRouter()
async function change(){error.value='';if(password.value!==confirm.value){error.value='两次输入的新密码不一致';return};busy.value=true;try{await api.userPassword({current_password:current.value,password:password.value});auth.user=null;await router.replace('/login')}catch(e){error.value=e.message}finally{busy.value=false}}
async function rebuild(){busy.value=true;try{await api.rebuildStats();notice.value='考点练习统计已重新计算'}catch(e){error.value=e.message}finally{busy.value=false}}
</script>
<template><div class="page-heading"><div><p class="eyebrow">账户设置 / ACCOUNT</p><h1>你的学习账户</h1><p class="muted">{{ auth.user?.nickname || auth.user?.username }} · {{ auth.user?.username }}</p></div></div><div class="account-grid"><form class="panel account-card" @submit.prevent="change"><h2>修改密码</h2><p class="muted">修改成功后会退出所有设备，请重新登录。</p><label>当前密码<input v-model="current" required type="password" autocomplete="current-password" /></label><label>新密码<input v-model="password" required minlength="8" type="password" autocomplete="new-password" /></label><label>确认新密码<input v-model="confirm" required minlength="8" type="password" autocomplete="new-password" /></label><button class="primary" :disabled="busy">修改密码</button></form><section class="panel account-card"><h2>学习统计</h2><p class="muted">考点标注更新后，可以根据历史作答重新计算考点掌握度。练习记录是统计依据。</p><button :disabled="busy" @click="rebuild">重新计算考点统计</button><p v-if="notice" class="notice ok" role="status">{{ notice }}</p></section></div><p v-if="error" class="notice err" role="alert">{{ error }}</p></template>
