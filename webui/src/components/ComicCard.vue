<script setup lang="ts">
import { computed } from 'vue'
import type { ComicBrief } from '@/api/sources'
import LazyImg from './LazyImg.vue'

const props = defineProps<{
  comic: ComicBrief
  src: string
}>()

const to = computed(() => `/comic/${props.src}/${props.comic.id}`)

const pageBadge = computed(() => (props.comic.pageCount > 0 ? `${props.comic.pageCount}P` : ''))

const chapterBadge = computed(() =>
  props.comic.chapters > 1 ? `${props.comic.chapters}話` : '',
)
</script>

<template>
  <RouterLink :to="to" class="comic-card">
    <div class="cover-wrap">
      <LazyImg :src="comic.cover" :alt="comic.title" eager />
      <div v-if="pageBadge || chapterBadge" class="badges">
        <span v-if="pageBadge" class="badge">{{ pageBadge }}</span>
        <span v-if="chapterBadge" class="badge">{{ chapterBadge }}</span>
      </div>
    </div>
    <div class="title" :title="comic.title">{{ comic.title }}</div>
    <div v-if="comic.author || comic.subtitle" class="sub">
      {{ comic.author || comic.subtitle }}
    </div>
  </RouterLink>
</template>

<style scoped>
.comic-card {
  display: block;
  min-width: 0;
}

.cover-wrap {
  position: relative;
  border-radius: 10px;
  overflow: hidden;
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.25);
  transition: transform 0.15s ease;
}

.comic-card:hover .cover-wrap {
  transform: translateY(-2px);
}

.badges {
  position: absolute;
  right: 6px;
  bottom: 6px;
  display: flex;
  gap: 4px;
}

.badge {
  padding: 1px 7px;
  border-radius: 6px;
  background: rgba(0, 0, 0, 0.62);
  color: #fff;
  font-size: 11px;
  font-weight: 600;
  backdrop-filter: blur(4px);
}

.title {
  margin-top: 8px;
  font-size: 13.5px;
  line-height: 1.35;
  color: var(--text);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.sub {
  margin-top: 2px;
  font-size: 12px;
  color: var(--text-faint);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
