<script setup>
import { computed } from 'vue'
import katex from 'katex'
const props = defineProps({ text: { type: String, default: '' } })
function escape(s) { return s.replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])) }
const html = computed(() => {
  const pattern = /⟦IMG:([^⟧]+)⟧|\$\$([\s\S]*?)\$\$|\$([^$\n]+)\$/g
  let out='', end=0, m
  while ((m=pattern.exec(props.text))) {
    out += escape(props.text.slice(end,m.index))
    if (m[1]) {
      const parts = m[1].split('/')
      if (parts.length===2 && ['题目图','公式图'].includes(parts[0]) && /^[^/\\]+\.(png|jpe?g|gif|webp)$/i.test(parts[1]) && !parts[1].startsWith('.')) {
        out += `<img src="/media/${parts.map(encodeURIComponent).join('/')}" alt="题目配图" loading="lazy" />`
      } else out += escape(m[0])
    } else {
      const tex = m[2] ?? m[3]
      try { out += katex.renderToString(tex,{ throwOnError:true, displayMode: m[2]!=null, trust:false, maxExpand:1000, maxSize:10, output:'htmlAndMathml' }) }
      catch { out += escape(m[0]) }
    }
    end=pattern.lastIndex
  }
  return out+escape(props.text.slice(end))
})
</script>
<template><div class="rich-text" v-html="html" /></template>
