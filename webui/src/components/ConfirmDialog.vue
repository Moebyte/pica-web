<script setup lang="ts">
import { ref, watch } from 'vue'

const props = defineProps<{
  open: boolean
  title: string
  message?: string
  confirmText?: string
  cancelText?: string
  danger?: boolean
  checkboxLabel?: string
  checkboxDefault?: boolean
  busy?: boolean
}>()

const emit = defineEmits<{
  cancel: []
  confirm: [checkbox: boolean]
}>()

const checked = ref(false)

watch(
  () => props.open,
  (open) => {
    if (open) checked.value = props.checkboxDefault ?? false
  },
)
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="dialog-overlay" @click.self="emit('cancel')">
      <div class="dialog" role="alertdialog" :aria-label="title">
        <h3>{{ title }}</h3>
        <p v-if="message">{{ message }}</p>
        <label v-if="checkboxLabel" class="check-row">
          <input v-model="checked" type="checkbox" class="box" />
          <span>{{ checkboxLabel }}</span>
        </label>
        <div class="actions">
          <button class="btn" :disabled="busy" @click="emit('cancel')">
            {{ cancelText ?? '取消' }}
          </button>
          <button
            class="btn"
            :class="danger ? 'danger' : 'primary'"
            :disabled="busy"
            @click="emit('confirm', checked)"
          >
            <span v-if="busy" class="spinner tiny" />
            {{ confirmText ?? '确定' }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.dialog-overlay {
  position: fixed;
  inset: 0;
  z-index: 200;
  background: var(--overlay);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}

.dialog {
  width: min(400px, 100%);
  background: var(--bg-soft);
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 20px;
  box-shadow: var(--shadow);
}

h3 {
  margin: 0 0 8px;
  font-size: 16px;
}

p {
  margin: 0;
  color: var(--text-dim);
  font-size: 14px;
  word-break: break-word;
}

.check-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 14px;
  font-size: 14px;
  color: var(--text);
  cursor: pointer;
  user-select: none;
}

.box {
  width: 16px;
  height: 16px;
  accent-color: var(--accent);
}

.actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 18px;
}

.btn.danger {
  background: var(--danger);
  border-color: transparent;
  color: #fff;
}

.spinner.tiny {
  width: 13px;
  height: 13px;
  border-width: 2px;
}
</style>
