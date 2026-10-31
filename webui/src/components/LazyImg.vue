<script setup lang="ts">
import { ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    src: string
    alt?: string
    ratio?: string
    eager?: boolean
  }>(),
  { alt: '', ratio: '2 / 3', eager: false },
)

const failed = ref(false)

watch(
  () => props.src,
  () => {
    failed.value = false
  },
)
</script>

<template>
  <div class="lazy-img" :style="{ aspectRatio: ratio }">
    <img
      v-if="src && !failed"
      :src="src"
      :alt="alt"
      :loading="eager ? 'eager' : 'lazy'"
      decoding="async"
      @error="failed = true"
    />
    <div v-else class="fallback">
      <svg viewBox="0 0 24 24" width="26" height="26" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="3" y="4" width="18" height="16" rx="2" />
        <path d="M3 15l5-4 4 3 3-2 6 5" />
        <circle cx="9" cy="9" r="1.4" />
      </svg>
      <span>{{ failed ? '图片加载失败' : '无封面' }}</span>
    </div>
  </div>
</template>

<style scoped>
.lazy-img {
  position: relative;
  width: 100%;
  overflow: hidden;
  background: var(--skeleton-a);
  border-radius: inherit;
}

.lazy-img img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.fallback {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  color: var(--text-faint);
  font-size: 11px;
  background: var(--card);
}
</style>
