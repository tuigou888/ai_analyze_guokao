<script setup>
import { computed } from 'vue'
const props=defineProps({id:{type:Number,default:0},name:{type:String,default:'研'},large:Boolean})
const styleID=computed(()=>Number.isInteger(props.id)&&props.id>=0&&props.id<=7?props.id:0)
</script>
<template><span :class="['pc-avatar',`pc-avatar-${styleID}`,{'pc-avatar-large':large}]" aria-hidden="true">{{ Array.from(name || '研')[0].toUpperCase() }}</span></template>
