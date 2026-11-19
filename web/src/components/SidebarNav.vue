<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { auth } from '../auth'
defineEmits(['navigate'])
const route=useRoute()
const groups=computed(()=>route.meta.admin?[
 {name:'网站管理',items:[['/admin?tab=users','用户管理','户'],...(auth.admin?.role==='super'?[['/admin?tab=admins','管理员账号','管'],['/admin?tab=settings','系统配置','设']]:[]),['/admin?tab=maintenance','数据维护','维'],['/admin?tab=account','我的账户','我']]},
 {name:'学习空间',items:[['/questions','返回学习中心','题']]}
]:[
 {name:'学习中心',items:[['/questions','题库练习','题'],['/papers','真题试卷','卷'],['/concepts','考点地图','知']]},
 {name:'复习与记录',items:[['/wrongbook','错题本','错'],['/favorites','我的收藏','藏'],['/records','学习记录','记']]}
])
function selected(to){if(to.startsWith('/admin?'))return route.meta.admin&&(route.query.tab|| (auth.admin?.role==='super'?'settings':'users'))===new URLSearchParams(to.split('?')[1]).get('tab');return route.path===to||to==='/concepts'&&route.path.startsWith('/concepts/')}
</script>
<template><nav aria-label="主导航"><section v-for="group in groups" :key="group.name" class="nav-group"><p class="nav-label">{{ group.name }}</p><RouterLink v-for="[to,label,symbol] in group.items" :key="to" :to="to" :class="{'nav-active':selected(to)}" :aria-current="selected(to)?'page':undefined" :title="label" @click="$emit('navigate')"><span class="nav-symbol" aria-hidden="true">{{ symbol }}</span><span class="nav-text">{{ label }}</span></RouterLink></section></nav><nav v-if="!route.meta.admin" class="sidebar-bottom" aria-label="账户导航"><RouterLink to="/account" :class="{'nav-active':route.path==='/account'}" :aria-current="route.path==='/account'?'page':undefined" title="个人中心" @click="$emit('navigate')"><span class="nav-symbol" aria-hidden="true">我</span><span class="nav-text">个人中心</span></RouterLink><RouterLink v-if="auth.admin" to="/admin" title="管理后台" @click="$emit('navigate')"><span class="nav-symbol" aria-hidden="true">管</span><span class="nav-text">管理后台</span></RouterLink></nav></template>
