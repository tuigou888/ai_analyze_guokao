<script setup>
import { computed, ref, useId, watch, onMounted, onBeforeUnmount } from 'vue'
const props=defineProps({modelValue:[String,Number],options:{type:Array,default:()=>[]},label:{type:String,default:'地区'},disabled:Boolean})
const emit=defineEmits(['update:modelValue'])
const id=useId(),root=ref(null),open=ref(false),text=ref(''),active=ref(0)
const choices=computed(()=>[{value:'',label:`全部${props.label}`},...props.options.map(v=>({value:v,label:String(v)}))].filter(v=>!text.value||v.label.toLowerCase().includes(text.value.trim().toLowerCase())))
watch(text,()=>{active.value=0})
function show(){open.value=true;text.value='';active.value=0}
function choose(v){emit('update:modelValue',v);open.value=false;text.value=''}
function key(e){if(e.isComposing)return;if(['ArrowDown','ArrowUp'].includes(e.key)){e.preventDefault();if(!open.value)show();else active.value=(active.value+(e.key==='ArrowDown'?1:-1)+choices.value.length)%Math.max(1,choices.value.length);root.value.querySelectorAll('[role="option"]')[active.value]?.scrollIntoView({block:'nearest'})}else if(e.key==='Enter'&&open.value){e.preventDefault();if(choices.value[active.value])choose(choices.value[active.value].value)}else if(e.key==='Escape'&&open.value){e.preventDefault();e.stopPropagation();open.value=false;text.value=''}else if(e.key==='Tab'){open.value=false;text.value=''}}
function outside(e){if(!root.value?.contains(e.target)){open.value=false;text.value=''}}
onMounted(()=>document.addEventListener('pointerdown',outside))
onBeforeUnmount(()=>document.removeEventListener('pointerdown',outside))
</script>
<template><div ref="root" class="searchable-select"><input :id="id" role="combobox" :aria-label="label" aria-autocomplete="list" :aria-expanded="open" :aria-controls="`${id}-list`" :aria-activedescendant="open&&choices[active]?`${id}-${active}`:undefined" :value="open?text:(modelValue||'')" :placeholder="`全部${label}`" :disabled="disabled" autocomplete="off" @focus="show" @click="!open&&show()" @input="text=$event.target.value;open=true" @keydown="key"/><span class="select-chevron" aria-hidden="true">⌄</span><ul v-if="open" :id="`${id}-list`" role="listbox" :aria-label="label" class="select-options"><li v-for="(v,i) in choices" :id="`${id}-${i}`" :key="v.value" role="option" :aria-selected="v.value===modelValue" :class="{highlighted:active===i}" @mousedown.prevent @click="choose(v.value)">{{ v.label }}<span v-if="v.value===modelValue" aria-hidden="true">✓</span></li><li v-if="!choices.length" class="muted" role="presentation">没有匹配选项</li></ul></div></template>
