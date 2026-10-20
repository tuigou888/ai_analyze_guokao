import { reactive } from 'vue'

// 全站共用的右上角提示队列，由 App.vue 根部的 ToastHost 渲染。
// ok/info 到时自动消失；err/warn 需要完整阅读，保留到手动关闭。
let seq = 0
export const toasts = reactive([])

export function dismiss(id) {
  const i = toasts.findIndex(t => t.id === id)
  if (i !== -1) toasts.splice(i, 1)
}

function push(kind, text, { lines = [], timeout } = {}) {
  const id = ++seq
  toasts.push({ id, kind, text, lines })
  const auto = timeout === undefined ? (kind === 'err' || kind === 'warn' ? 0 : 4000) : timeout
  if (auto > 0) setTimeout(() => dismiss(id), auto)
  return id
}

export const toast = {
  ok: (text, opts) => push('ok', text, opts),
  err: (text, opts) => push('err', text, opts),
  warn: (text, opts) => push('warn', text, opts),
  info: (text, opts) => push('info', text, opts),
}
