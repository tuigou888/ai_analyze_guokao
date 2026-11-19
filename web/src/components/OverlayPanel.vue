<script setup>
import { ref, watch, nextTick, onBeforeUnmount, useId } from 'vue'
const props = defineProps({ open:Boolean, title:String, drawer:Boolean })
const emit = defineEmits(['close'])
const dialog=ref(null), titleId=useId()
let previous
watch(()=>props.open, async value=>{
  await nextTick()
  if(!dialog.value)return
  if(value && !dialog.value.open){previous=document.activeElement;dialog.value.showModal()}
  else if(!value && dialog.value.open) dialog.value.close()
}, { immediate:true })
function close(){emit('close')}
function restore(){emit('close');requestAnimationFrame(()=>{const target=previous?.isConnected&&!previous.disabled?previous:document.querySelector('#main');target?.focus()})}
onBeforeUnmount(()=>{if(dialog.value?.open)dialog.value.close()})
</script>
<template><Teleport to="body"><dialog ref="dialog" :class="['overlay-panel',{drawer}]" :aria-labelledby="titleId" @cancel.prevent="close" @close="restore" @click="event=>{if(event.target===dialog)close()}"><div class="overlay-heading"><h2 :id="titleId">{{ title }}</h2><button type="button" aria-label="关闭弹层" @click="close">×</button></div><div class="overlay-body"><slot /></div></dialog></Teleport></template>
