<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { srcFromType, type HistoryRow } from '@/api/store'
import { useHistoryStore } from '@/stores/history'
import SignedImg from '@/components/SignedImg.vue'
import ErrorRetry from '@/components/ErrorRetry.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useScrollRestore } from '@/composables/useScrollRestore'
import { relativeTime } from '@/utils/format'

defineOptions({ name: 'HistoryPage' })

const router = useRouter()
const store = useHistoryStore()

const rows = computed(() => [...store.rows].sort((a, b) => b.time - a.time))

function label(row: HistoryRow): string {
  const src = srcFromType(row.type, row.target)
  if (src === 'jm') return '禁漫'
  if (src === 'picacg') return 'PicACG'
  return `类型${row.type}`
}

function progress(row: HistoryRow): string {
  const parts: string[] = []
  if (row.ep > 0) parts.push(`第 ${row.ep} 話`)
  parts.push(`P${row.page}${row.max_page ? `/${row.max_page}` : ''}`)
  return parts.join(' · ')
}

function open(row: HistoryRow): void {
  const src = srcFromType(row.type, row.target)
  if (!src) return
  void router.push({
    name: 'read',
    params: { src, id: row.target },
    query: row.readEpisode ? { ep: row.readEpisode } : {},
  })
}

onMounted(() => void store.load(true))
useScrollRestore()
</script>

<template>
  <div class="page">
    <ErrorRetry
      v-if="store.error && !store.loading"
      :message="`历史读取失败：${store.error}`"
      :retrying="store.loading"
      @retry="store.load(true)"
    />

    <div v-else-if="store.loading && rows.length === 0" class="list">
      <div v-for="i in 6" :key="i" class="row sk-row">
        <div class="skeleton sk-cover" />
        <div class="sk-lines">
          <div class="skeleton sk-l1" />
          <div class="skeleton sk-l2" />
        </div>
      </div>
    </div>

    <EmptyState v-else-if="rows.length === 0" icon="🕘" text="还没有阅读记录" />

    <div v-else class="list" data-testid="history-list">
      <button v-for="row in rows" :key="row.target" class="row" @click="open(row)">
        <div class="cover">
          <SignedImg :url="row.cover || ''" :alt="row.title" />
        </div>
        <div class="info">
          <div class="t" :title="row.title">{{ row.title }}</div>
          <div v-if="row.subtitle" class="s">{{ row.subtitle }}</div>
          <div class="p">
            <span class="src-chip" :data-type="row.type">{{ label(row) }}</span>
            <span>{{ progress(row) }}</span>
            <span class="time">{{ relativeTime(row.time) }}</span>
          </div>
        </div>
        <span class="go">›</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.row {
  display: flex;
  gap: 14px;
  align-items: center;
  padding: 10px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: var(--card);
  cursor: pointer;
  text-align: left;
  color: var(--text);
  width: 100%;
}

.row:hover {
  background: var(--card-hover);
}

.cover {
  width: 64px;
  flex: none;
  border-radius: 8px;
  overflow: hidden;
}

.info {
  flex: 1;
  min-width: 0;
}

.t {
  font-size: 14px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.s {
  font-size: 12.5px;
  color: var(--text-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.p {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 4px;
  font-size: 12.5px;
  color: var(--text-faint);
}

.src-chip {
  padding: 0 8px;
  border-radius: 6px;
  font-size: 11px;
  background: var(--accent-soft);
  color: var(--accent-text);
}

.src-chip[data-type='2'] {
  background: var(--danger-soft);
  color: var(--danger);
}

.time {
  margin-left: auto;
}

.go {
  color: var(--text-faint);
  font-size: 22px;
  flex: none;
}

.sk-row {
  cursor: default;
}

.sk-cover {
  width: 64px;
  aspect-ratio: 2 / 3;
}

.sk-lines {
  flex: 1;
}

.sk-l1 {
  height: 15px;
  max-width: 300px;
}

.sk-l2 {
  height: 12px;
  margin-top: 8px;
  max-width: 180px;
}
</style>
