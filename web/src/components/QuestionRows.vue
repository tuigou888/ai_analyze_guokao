<script setup>
import { ref, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import RichText from './RichText.vue'
const props=defineProps({items:{type:Array,default:()=>[]},busy:Boolean,practiceLabel:{type:String,default:'开始练习'}})
defineEmits(['practice'])
const root=ref(null),expanded=ref(new Set()),overflowing=ref(new Set())
let observer
const figure=q=>q.has_figure||q.stem?.includes('⟦IMG:')||q.stem?.includes('![')
function measure(){const next=new Set(overflowing.value);for(const element of root.value?.querySelectorAll('.brief-text')||[]){const id=Number(element.dataset.question);if(!expanded.value.has(id)){if(element.scrollHeight>element.clientHeight+1)next.add(id);else next.delete(id)}}overflowing.value=next}
async function observe(){await nextTick();observer?.disconnect();for(const element of root.value?.querySelectorAll('.brief-text')||[])observer?.observe(element);measure()}
function toggle(id){const next=new Set(expanded.value);next.has(id)?next.delete(id):next.add(id);expanded.value=next}
onMounted(()=>{observer=new ResizeObserver(measure);observe()})
watch(()=>props.items,observe)
onBeforeUnmount(()=>observer?.disconnect())
</script>
<template>
<div ref="root" class="question-rows">
  <article v-for="q in items" :key="q.id" class="question-row">
    <div class="question-row-main">
      <div class="row-meta"><span class="tag">{{ q.module }}</span><span>{{ {single:'单选题',multi:'多选题',judge:'判断题',other:'暂无答案'}[q.answer_type] }}</span><span class="mono">#{{ q.id }}</span><span v-if="q.has_label" class="label-mark">已归纳考点</span><slot name="meta" :question="q" /></div>
      <RichText :id="`stem-${q.id}`" :data-question="q.id" :class="['brief-text',{expanded:expanded.has(q.id)}]" :text="q.stem" />
      <button v-if="overflowing.has(q.id)||expanded.has(q.id)||figure(q)" class="text-button stem-toggle" :aria-expanded="expanded.has(q.id)" :aria-controls="`stem-${q.id}`" @click="toggle(q.id)">{{ expanded.has(q.id)?'收起题干':'展开完整题干' }}</button>
    </div>
    <div class="row-actions"><button :disabled="q.answer_type==='other'||busy" @click="$emit('practice',q.id)">{{ q.answer_type==='other'?'暂不可练习':practiceLabel }} <span aria-hidden="true">↗</span></button><slot name="actions" :question="q" /></div>
  </article>
</div>
</template>
