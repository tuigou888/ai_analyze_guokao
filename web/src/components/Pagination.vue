<script setup>
import { computed } from 'vue'
const props=defineProps({page:Number,size:Number,total:Number,busy:Boolean})
const emit=defineEmits(['change'])
const pages=computed(()=>Math.max(1,Math.ceil(props.total/props.size)))
</script>
<template><nav class="pagination" aria-label="分页"><span class="muted">共 {{ total.toLocaleString() }} 条 · 第 {{ page }} / {{ pages }} 页</span><div><button :disabled="page<=1||busy" @click="emit('change',page-1)">上一页</button><button :disabled="page>=pages||busy" @click="emit('change',page+1)">下一页</button></div></nav></template>
