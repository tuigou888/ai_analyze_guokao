<script setup>
import { onMounted, ref } from 'vue'
import { api } from '../api'
const props = defineProps({ modelValue: { type:Object,required:true }, search: Boolean })
const emit = defineEmits(['update:modelValue','apply'])
const data = ref({}),error=ref('')
onMounted(async()=>{try{data.value=await api.filters()}catch(e){error.value=e.message}})
function update(key,value) { emit('update:modelValue',{...props.modelValue,[key]:value}) }
</script>
<template><form class="filters" @submit.prevent="emit('apply')">
  <label v-if="search" class="search-field">关键词<input :value="modelValue.q" placeholder="搜索题干或考点名称" @input="update('q',$event.target.value)" maxlength="120" /></label>
  <label v-for="[key,name,list] in [['module','模块','modules'],['year','年份','years'],['region','地区','regions'],['exam_type','考试类型','exam_types'],['variant','卷别','variants']]" :key="key">{{ name }}<select :value="modelValue[key] || ''" @change="update(key,$event.target.value)"><option value="">全部{{ name }}</option><option v-for="v in data[list]" :key="v" :value="v">{{ v }}</option></select></label>
  <button class="primary" type="submit">筛选{{ search ? ' / 搜索' : '' }}</button><p v-if="error" class="err" role="alert">{{ error }}</p>
</form></template>
