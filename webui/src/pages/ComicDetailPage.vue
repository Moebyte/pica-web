<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getComic, isSourceKey, type ComicDetail } from '@/api/sources'
import { useHistoryStore } from '@/stores/history'
import LazyImg from '@/components/LazyImg.vue'
import ErrorRetry from '@/components/ErrorRetry.vue'
import { formatUploadDate, formatCount } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const historyStore = useHistoryStore()

const src = computed(() => {
  const s = route.params.src as string
  return isSourceKey(s) ? s : 'jm'
})
const id = computed(() => (route.params.id as string) ?? '')

const comic = ref<ComicDetail | null>(null)
const loading = ref(false)
const error = ref('')

const sortedChapters = computed(() =>
  [...(comic.value?.chapters ?? [])].sort((a, b) => a.order - b.order),
)

const tagGroups = computed(() => {
  const groups = comic.value?.tags
  if (!groups || Array.isArray(groups)) return []
  return Object.entries(groups).filter(([, tags]) => tags.length > 0)
})

const historyRow = computed(() => historyStore.find(id.value))

const continueEp = computed(() => historyRow.value?.readEpisode || '')

const continueLabel = computed(() => {
  const row = historyRow.value
  if (!row || !row.readEpisode) return ''
  return `继续 · 第 ${row.ep} 話 P${row.page}`
})

async function load(): Promise<void> {
  if (!id.value) return
  loading.value = true
  error.value = ''
  comic.value = null
  try {
    comic.value = await getComic(src.value, id.value)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

function startReading(ep?: string): void {
  const target = ep ?? historyRow.value?.readEpisode ?? sortedChapters.value[0]?.id
  void router.push({
    name: 'read',
    params: { src: src.value, id: id.value },
    query: target ? { ep: target } : {},
  })
}

function startFromBeginning(): void {
  const first = sortedChapters.value[0]?.id
  void router.push({
    name: 'read',
    params: { src: src.value, id: id.value },
    query: first ? { ep: first } : {},
  })
}

function searchTag(tag: string): void {
  void router.push({ name: 'search', query: { q: tag } })
}

onMounted(() => {
  void load()
  void historyStore.load()
})

watch([src, id], () => void load())
</script>

<template>
  <div class="page detail">
    <ErrorRetry
      v-if="error && !loading"
      :message="`加载详情失败：${error}`"
      :retrying="loading"
      @retry="load"
    />

    <div v-else-if="loading" class="detail-loading">
      <div class="skeleton head-sk" />
      <div class="skeleton line-sk" />
      <div class="skeleton line-sk short" />
    </div>

    <template v-else-if="comic">
      <div class="head">
        <div class="cover">
          <LazyImg :src="comic.cover" :alt="comic.title" eager />
        </div>
        <div class="meta">
          <h1 class="title">{{ comic.title }}</h1>
          <div v-if="comic.subtitle" class="subtitle">{{ comic.subtitle }}</div>
          <div v-if="comic.author" class="author">{{ comic.author }}</div>
          <div class="stats">
            <span v-if="comic.pageCount" class="stat">{{ comic.pageCount }} 页</span>
            <span class="stat">{{ sortedChapters.length }} 話</span>
            <span v-if="comic.favorites !== undefined" class="stat">
              {{ formatCount(comic.favorites) }} 收藏
            </span>
            <span class="stat">{{ formatUploadDate(comic.uploadDate) }}</span>
          </div>
          <button class="btn primary read-btn" data-testid="start-reading" @click="startReading()">
            {{ continueLabel || '开始阅读' }}
          </button>
          <button v-if="continueLabel" class="btn read-btn from-start" @click="startFromBeginning()">
            从头开始
          </button>
        </div>
      </div>

      <section v-if="tagGroups.length" class="section">
        <h2>标签</h2>
        <div v-for="[group, tags] in tagGroups" :key="group" class="tag-group">
          <span class="group-name">{{ group }}</span>
          <button v-for="tag in tags" :key="tag" class="chip" @click="searchTag(tag)">
            {{ tag }}
          </button>
        </div>
      </section>

      <section class="section">
        <h2>章节 <span class="count">({{ sortedChapters.length }})</span></h2>
        <div class="chapter-list" data-testid="chapter-list">
          <button
            v-for="ch in sortedChapters"
            :key="ch.id"
            class="chapter-item"
            :class="{ current: ch.id === continueEp }"
            :data-testid="`chapter-${ch.order}`"
            @click="startReading(ch.id)"
          >
            <span class="ch-title">{{ ch.title || `第 ${ch.order} 話` }}</span>
            <span v-if="ch.id === continueEp" class="reading-mark">上次看到</span>
          </button>
        </div>
      </section>

      <section v-if="comic.thumbs?.length" class="section">
        <h2>预览</h2>
        <div class="thumbs">
          <LazyImg
            v-for="(t, i) in comic.thumbs"
            :key="i"
            :src="t"
            :alt="`预览 ${i + 1}`"
            class="thumb"
          />
        </div>
      </section>
    </template>

    <ErrorRetry v-else message="漫画不存在" retry-label="返回" @retry="router.back()" />
  </div>
</template>

<style scoped>
.detail {
  padding-top: 18px;
}

.detail-loading {
  padding-top: 10px;
}

.head-sk {
  height: 220px;
  border-radius: 12px;
}

.line-sk {
  height: 16px;
  margin-top: 14px;
  max-width: 420px;
}

.line-sk.short {
  max-width: 260px;
}

.head {
  display: flex;
  gap: 20px;
}

.cover {
  width: 190px;
  flex: none;
  border-radius: 12px;
  overflow: hidden;
  box-shadow: var(--shadow);
}

.meta {
  flex: 1;
  min-width: 0;
}

.title {
  margin: 2px 0 6px;
  font-size: 20px;
  line-height: 1.4;
  word-break: break-word;
}

.subtitle,
.author {
  color: var(--text-dim);
  font-size: 14px;
  margin-bottom: 2px;
  word-break: break-word;
}

.stats {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 14px;
  margin: 12px 0 18px;
}

.stat {
  color: var(--text-faint);
  font-size: 13px;
}

.read-btn {
  min-width: 180px;
}

.from-start {
  margin-left: 10px;
}

.section {
  margin-top: 30px;
}

h2 {
  font-size: 16px;
  margin: 0 0 12px;
}

h2 .count {
  color: var(--text-faint);
  font-weight: 400;
  font-size: 13px;
}

.tag-group {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 10px;
}

.group-name {
  color: var(--text-faint);
  font-size: 12.5px;
  min-width: 3.5em;
}

.chapter-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(170px, 1fr));
  gap: 8px;
}

.chapter-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 12px;
  border-radius: 10px;
  border: 1px solid var(--border);
  background: var(--card);
  color: var(--text);
  cursor: pointer;
  text-align: left;
}

.chapter-item:hover {
  background: var(--card-hover);
}

.chapter-item.current {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.ch-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 14px;
}

.reading-mark {
  flex: none;
  color: var(--accent-text);
  font-size: 11.5px;
}

.thumbs {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(110px, 1fr));
  gap: 10px;
}

.thumb {
  border-radius: 8px;
  overflow: hidden;
}

@media (max-width: 640px) {
  .head {
    flex-direction: column;
  }

  .cover {
    width: 150px;
  }

  .read-btn.from-start {
    margin-left: 0;
    margin-top: 10px;
  }

  .chapter-list {
    grid-template-columns: 1fr;
  }
}
</style>
