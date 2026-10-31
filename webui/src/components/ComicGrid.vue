<script setup lang="ts">
import type { ComicBrief } from '@/api/sources'
import ComicCard from './ComicCard.vue'

withDefaults(
  defineProps<{
    comics: ComicBrief[]
    src: string
    skeleton?: number
  }>(),
  { skeleton: 0 },
)
</script>

<template>
  <div class="comic-grid">
    <ComicCard v-for="c in comics" :key="c.id" :comic="c" :src="src" />
    <div v-for="i in skeleton" :key="`sk-${i}`" class="sk-card" aria-hidden="true">
      <div class="skeleton sk-cover" />
      <div class="skeleton sk-line" />
      <div class="skeleton sk-line short" />
    </div>
  </div>
</template>

<style scoped>
.comic-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: 16px 14px;
}

@media (max-width: 480px) {
  .comic-grid {
    grid-template-columns: repeat(2, 1fr);
    gap: 12px 10px;
  }
}

.sk-card {
  min-width: 0;
}

.sk-cover {
  width: 100%;
  aspect-ratio: 2 / 3;
  border-radius: 10px;
}

.sk-line {
  margin-top: 8px;
  height: 13px;
}

.sk-line.short {
  width: 60%;
}
</style>
