<script setup lang="ts">
import { useToast } from '@/composables/useToast'

const toast = useToast()
</script>

<template>
  <Teleport to="body">
    <div class="toast-stack" aria-live="polite" data-testid="toast-stack">
      <div
        v-for="t in toast.items"
        :key="t.id"
        class="toast"
        :class="t.type"
        data-testid="toast"
      >
        <span class="msg">{{ t.message }}</span>
        <RouterLink
          v-if="t.action"
          :to="t.action.to"
          class="toast-action"
          data-testid="toast-action"
          @click="toast.dismiss(t.id)"
        >
          {{ t.action.label }}
        </RouterLink>
        <button class="toast-close" aria-label="关闭提示" @click="toast.dismiss(t.id)">×</button>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-stack {
  position: fixed;
  left: 50%;
  bottom: 24px;
  transform: translateX(-50%);
  z-index: 400;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  width: min(460px, calc(100vw - 32px));
  pointer-events: none;
}

@media (max-width: 899.9px) {
  .toast-stack {
    bottom: calc(66px + env(safe-area-inset-bottom));
  }
}

.toast {
  pointer-events: auto;
  display: flex;
  align-items: center;
  gap: 10px;
  max-width: 100%;
  padding: 10px 12px;
  border-radius: 11px;
  border: 1px solid var(--border);
  background: var(--bg-soft);
  color: var(--text);
  box-shadow: var(--shadow);
  font-size: 13.5px;
  animation: toast-in 0.18s ease;
}

.toast.success {
  border-color: rgba(76, 195, 138, 0.5);
}

.toast.error {
  border-color: rgba(229, 97, 107, 0.55);
}

.msg {
  flex: 1;
  min-width: 0;
  word-break: break-word;
}

.toast-action {
  flex: none;
  color: var(--accent-text);
  font-weight: 600;
  white-space: nowrap;
}

.toast-action:hover {
  text-decoration: underline;
}

.toast-close {
  flex: none;
  width: 22px;
  height: 22px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--text-faint);
  font-size: 17px;
  line-height: 1;
  cursor: pointer;
}

.toast-close:hover {
  background: var(--card-hover);
  color: var(--text);
}

@keyframes toast-in {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
</style>
