<script setup lang="ts">
withDefaults(
  defineProps<{
    message?: string
    retryLabel?: string
    retrying?: boolean
  }>(),
  { message: '加载失败', retryLabel: '重试', retrying: false },
)

defineEmits<{ retry: [] }>()
</script>

<template>
  <div class="error-retry">
    <svg viewBox="0 0 24 24" width="30" height="30" fill="none" stroke="currentColor" stroke-width="1.6">
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7.5v5.5" stroke-linecap="round" />
      <circle cx="12" cy="16.4" r="0.9" fill="currentColor" stroke="none" />
    </svg>
    <p>{{ message }}</p>
    <button class="btn small" :disabled="retrying" @click="$emit('retry')">
      <span v-if="retrying" class="spinner tiny" />
      {{ retryLabel }}
    </button>
  </div>
</template>

<style scoped>
.error-retry {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 40px 16px;
  color: var(--danger);
}

p {
  margin: 0;
  color: var(--text-dim);
  font-size: 14px;
  word-break: break-all;
}

.spinner.tiny {
  width: 13px;
  height: 13px;
  border-width: 2px;
}
</style>
