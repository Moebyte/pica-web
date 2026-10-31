<script setup lang="ts">
withDefaults(
  defineProps<{
    text?: string
    icon?: string
  }>(),
  { text: '这里空空如也', icon: '🗂' },
)

defineSlots<{ action?: () => unknown }>()
</script>

<template>
  <div class="empty-state">
    <div class="icon">{{ icon }}</div>
    <div>{{ text }}</div>
    <slot name="action" />
  </div>
</template>
