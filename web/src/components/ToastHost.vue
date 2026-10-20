<script setup>
import { toasts, dismiss } from '../toast'
</script>
<template>
  <div class="toast-host" aria-live="polite">
    <TransitionGroup name="toast">
      <div
        v-for="t in toasts"
        :key="t.id"
        :class="['toast-item', t.kind]"
        :role="t.kind === 'ok' || t.kind === 'info' ? 'status' : 'alert'"
      >
        <div class="toast-body">
          <p class="toast-text">{{ t.text }}</p>
          <div v-if="t.lines.length" class="toast-lines">
            <p v-for="(line, i) in t.lines" :key="i">{{ line }}</p>
          </div>
        </div>
        <button class="toast-close" type="button" aria-label="关闭提示" @click="dismiss(t.id)">×</button>
      </div>
    </TransitionGroup>
  </div>
</template>

<style scoped>
.toast-host {
  position: fixed;
  top: var(--space-4);
  right: var(--space-4);
  z-index: 60;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  width: min(92vw, 400px);
  pointer-events: none;
}

.toast-item {
  pointer-events: auto;
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  background: var(--color-surface);
  border: var(--border-width) solid var(--color-border);
  border-left-width: 4px;
  border-radius: var(--radius-control);
  padding: var(--space-3) var(--space-3) var(--space-3) var(--space-4);
  box-shadow: 0 12px 28px rgba(32, 50, 75, 0.16);
}

.toast-item.ok { border-left-color: var(--color-success); }
.toast-item.err { border-left-color: var(--color-danger); }
.toast-item.warn { border-left-color: var(--color-warning); }
.toast-item.info { border-left-color: var(--color-primary); }

.toast-body { flex: 1; min-width: 0; }
.toast-text { margin: 0; font-size: var(--font-size-label); font-weight: 600; color: var(--color-text); overflow-wrap: anywhere; }
.toast-lines { margin-top: var(--space-2); }
.toast-lines p { margin: 0 0 var(--space-1); font-size: var(--font-size-xs); color: var(--color-text-muted); overflow-wrap: anywhere; }
.toast-lines p:last-child { margin-bottom: 0; }

.toast-close {
  flex: none;
  width: 26px;
  height: 26px;
  display: grid;
  place-items: center;
  padding: 0;
  border: none;
  border-radius: var(--radius-small);
  background: transparent;
  color: var(--color-text-muted);
  font-size: 18px;
  line-height: 1;
  cursor: pointer;
}
.toast-close:hover { color: var(--color-text); background: var(--color-surface-subtle); }

.toast-enter-active,
.toast-leave-active,
.toast-move {
  transition: opacity 0.25s ease, transform 0.25s ease;
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateX(24px);
}
</style>
