<script setup>
import { onMounted, ref, computed, watch } from 'vue'
import { api } from '../api'
import SearchableSelect from './SearchableSelect.vue'
import OverlayPanel from './OverlayPanel.vue'
const props=defineProps({modelValue:{type:Object,required:true},applied:Object,search:Boolean,busy:Boolean})
const emit=defineEmits(['update:modelValue','apply'])
const dimensions=[['module','模块','modules'],['year','年份','years'],['region','地区','regions'],['exam_type','考试类型','exam_types'],['variant','卷别','variants']]
const data=ref({}),error=ref(''),expanded=ref(false),mobileOpen=ref(false),snapshot=ref({...props.modelValue})
const effective=computed(()=>props.applied||snapshot.value)
const chips=computed(()=>[...dimensions,['q','关键词']].filter(([key])=>effective.value[key]))
const pending=computed(()=>[...dimensions,['q']].some(([key])=>String(props.modelValue[key]||'')!==String(effective.value[key]||'')))
const advanced=computed(()=>dimensions.slice(1))
const extraCount=computed(()=>advanced.value.filter(([key])=>props.modelValue[key]).length)
function update(key,value){emit('update:modelValue',{...props.modelValue,[key]:value})}
function apply(){snapshot.value={...props.modelValue};emit('apply');mobileOpen.value=false}
function clear(key){if(pending.value&&!window.confirm('还有尚未应用的筛选条件。继续清除将放弃这些编辑，并保留其他已应用条件。确定继续吗？'))return;const value=key?{...effective.value,[key]:''}:{...effective.value,...Object.fromEntries([...dimensions,['q']].map(([k])=>[k,'']))};emit('update:modelValue',value);snapshot.value={...value};emit('apply')}
async function load(){error.value='';try{data.value=await api.filters()}catch(e){error.value=e.message}}
watch(()=>props.applied,value=>{if(value)snapshot.value={...value}})
onMounted(load)
</script>
<template>
<section class="filter-panel" aria-label="筛选题目与试卷">
  <form class="filters filter-primary" @submit.prevent="apply">
    <label v-if="search" class="search-field">关键词<input :value="modelValue.q" placeholder="搜索题干或考点名称" maxlength="120" @input="update('q',$event.target.value)" /></label>
    <label>模块<select :value="modelValue.module||''" @change="update('module',$event.target.value)"><option value="">全部模块</option><option v-for="v in data.modules" :key="v" :value="v">{{ v }}</option></select></label>
    <template v-if="!search"><label class="desktop-filter">年份<select :value="modelValue.year||''" @change="update('year',$event.target.value)"><option value="">全部年份</option><option v-for="v in data.years" :key="v" :value="v">{{ v }}</option></select></label><div class="filter-field desktop-filter"><span>地区</span><SearchableSelect label="地区" :model-value="modelValue.region" :options="data.regions||[]" @update:model-value="update('region',$event)" /></div></template>
    <button class="primary" type="submit" :disabled="busy">{{ busy?'查询中…':search?'搜索':'查询试卷' }}</button>
    <button class="desktop-filter" type="button" :aria-expanded="expanded" aria-controls="advanced-filters" @click="expanded=!expanded">{{ expanded?'收起筛选':'高级筛选' }}{{ extraCount?` · ${extraCount}`:'' }} <span aria-hidden="true">⌄</span></button>
    <button class="mobile-filter" type="button" @click="mobileOpen=true">筛选条件{{ extraCount?` · ${extraCount}`:'' }}</button>
    <div v-if="expanded" id="advanced-filters" class="filter-advanced desktop-filter"><template v-for="[key,name,list] in advanced" :key="key"><div v-if="key==='region'&&search" class="filter-field"><span>{{ name }}</span><SearchableSelect :label="name" :model-value="modelValue[key]" :options="data[list]||[]" @update:model-value="update(key,$event)" /></div><label v-else-if="search||!['year','region'].includes(key)">{{ name }}<select :value="modelValue[key]||''" @change="update(key,$event.target.value)"><option value="">全部{{ name }}</option><option v-for="v in data[list]" :key="v" :value="v">{{ v }}</option></select></label></template></div>
    <p v-if="pending" class="filter-pending" role="status">条件已修改，点击{{ search?'搜索':'查询试卷' }}后生效。</p>
    <p v-if="error" class="err filter-error" role="alert">筛选选项加载失败：{{ error }} <button type="button" @click="load">重试</button></p>
  </form>
  <div v-if="chips.length" class="filter-chips" aria-label="已应用筛选"><span class="muted small">已应用</span><button v-for="[key,name] in chips" :key="key" class="filter-chip" type="button" :disabled="busy" :aria-label="`清除${name}：${effective[key]}`" @click="clear(key)">{{ name }}：{{ effective[key] }} <span aria-hidden="true">×</span></button><button class="text-button" :disabled="busy" @click="clear()">清空条件</button></div>
  <OverlayPanel :open="mobileOpen" title="筛选条件" drawer @close="mobileOpen=false"><form class="filter-drawer-form" @submit.prevent="apply"><template v-for="[key,name,list] in advanced" :key="key"><div v-if="key==='region'" class="filter-field"><span>{{ name }}</span><SearchableSelect :label="name" :model-value="modelValue[key]" :options="data[list]||[]" @update:model-value="update(key,$event)" /></div><label v-else>{{ name }}<select :value="modelValue[key]||''" @change="update(key,$event.target.value)"><option value="">全部{{ name }}</option><option v-for="v in data[list]" :key="v" :value="v">{{ v }}</option></select></label></template><p class="muted small">模块与关键词沿用页面当前选择。地区和考试类型分别筛选。</p><button class="primary wide" :disabled="busy">应用筛选</button></form></OverlayPanel>
</section>
</template>
