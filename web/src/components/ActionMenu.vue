<script setup>
import { ref, nextTick, onMounted, onBeforeUnmount, useId } from 'vue'
defineProps({ label:{type:String,default:'更多操作'}, disabled:Boolean })
const root=ref(null),trigger=ref(null),open=ref(false),menuId=useId()
function close(restore=false){open.value=false;if(restore)trigger.value?.focus()}
async function toggle(){open.value=!open.value;if(open.value){await nextTick();root.value?.querySelector('[role="menu"] button:not(:disabled)')?.focus()}}
function key(e){if(e.key==='Escape'){e.preventDefault();close(true)}else if(['ArrowDown','ArrowUp','Home','End'].includes(e.key)){e.preventDefault();const items=[...root.value.querySelectorAll('[role="menu"] button:not(:disabled)')];const i=items.indexOf(document.activeElement);items[e.key==='Home'?0:e.key==='End'?items.length-1:(i+(e.key==='ArrowDown'?1:-1)+items.length)%items.length]?.focus()}else if(e.key==='Tab')close()}
function outside(e){if(!root.value?.contains(e.target))close()}
onMounted(()=>document.addEventListener('pointerdown',outside))
onBeforeUnmount(()=>document.removeEventListener('pointerdown',outside))
</script>
<template><div ref="root" class="action-menu" @keydown="key"><button ref="trigger" type="button" :disabled="disabled" aria-haspopup="menu" :aria-controls="menuId" :aria-expanded="open" :aria-label="label" @click="toggle">···</button><div v-if="open" :id="menuId" class="action-menu-popover" role="menu" :aria-label="label" @click="close(true)"><slot /></div></div></template>
