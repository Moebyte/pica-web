<script setup lang="ts">
import { onActivated, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { search, JM_SORTS } from '@/api/sources'
import { useAppStore } from '@/stores/app'
import { usePagedList } from '@/composables/usePagedList'
import { useInfiniteSentinel } from '@/composables/useInfiniteSentinel'
import { useScrollRestore } from '@/composables/useScrollRestore'
import ComicGrid from '@/components/ComicGrid.vue'
import ErrorRetry from '@/components/ErrorRetry.vue'
import EmptyState from '@/components/EmptyState.vue'

defineOptions({ name: 'SearchPage' })

const app = useAppStore()
const { currentSrc } = storeToRefs(app)
const route = useRoute()
const router = useRouter()

const input = ref((route.query.q as string) ?? '')
const sort = ref((route.query.sort as string) || 'mr')
const submitted = ref('')

const sentinel = ref<HTMLElement | null>(null)

const list = usePagedList(async (page) => {
  const res = await search(currentSrc.value, { q: submitted.value, page, sort: sort.value })
  return { items: res.comics, maxPage: res.maxPage }
})

useInfiniteSentinel(
  sentinel,
  () => void list.loadMore(),
  () => !list.loading.value && !list.loadingMore.value && !list.error.value && !list.done(),
)

useScrollRestore()

const isJm = ref(currentSrc.value === 'jm')
watch(currentSrc, (s) => {
  isJm.value = s === 'jm'
  submitted.value = ''
  list.items.value = []
  list.initialized.value = false
})

function submit(): void {
  const q = input.value.trim()
  submitted.value = q
  router.replace({ query: { ...route.query, q: q || undefined } })
  void list.reload()
}

function onSortChange(v: string): void {
  sort.value = v
  if (submitted.value) void list.reload()
}

/** Enter search from another page (tag chips / detail) via query param. */
function syncFromQuery(): void {
  const q = (route.query.q as string) ?? ''
  if (q !== submitted.value) {
    input.value = q
    submitted.value = q
    if (q) void list.reload()
  }
}

onMounted(() => {
  void app.loadSources()
  syncFromQuery()
})

onActivated(syncFromQuery)

watch(
  () => route.query.q,
  () => syncFromQuery(),
)
</script>

<template>
  <div class="page">
    <form class="search-bar" @submit.prevent="submit">
      <div class="input-wrap">
        <svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
          <circle cx="10.5" cy="10.5" r="6.5" />
          <path d="M20 20l-4.9-4.9" />
        </svg>
        <input
          v-model="input"
          type="search"
          :placeholder="`在${app.currentSource?.name ?? currentSrc}中搜索`"
          aria-label="搜索关键词"
          data-testid="search-input"
        />
        <button v-if="input" type="button" class="clear" aria-label="清空" @click="input = ''">×</button>
      </div>
      <select
        v-if="isJm"
        v-model="sort"
        class="sort-select"
        aria-label="排序"
        @change="onSortChange(sort)"
      >
        <option v-for="s in JM_SORTS" :key="s.value" :value="s.value">{{ s.label }}</option>
      </select>
      <button type="submit" class="btn primary" data-testid="search-submit">搜索</button>
    </form>

    <template v-if="submitted">
      <ComicGrid
        v-if="list.items.value.length > 0 || list.loading.value"
        :comics="list.items.value"
        :src="currentSrc"
        :skeleton="list.loading.value ? 12 : 0"
      />

      <EmptyState
        v-else-if="list.initialized.value && !list.error.value"
        :text="`没有找到与「${submitted}」相关的内容`"
        icon="🔍"
      />

      <ErrorRetry
        v-if="list.error.value && list.items.value.length === 0"
        :message="`搜索失败：${list.error.value}`"
        :retrying="list.loading.value"
        @retry="list.reload()"
      />

      <div v-if="list.loadingMore.value" class="loading-more">
        <div class="spinner" />
      </div>
      <div v-if="list.done() && list.items.value.length > 0" class="list-end">— 没有更多了 —</div>
      <div
        v-show="!list.done()"
        ref="sentinel"
        data-testid="infinite-sentinel"
        style="height: 1px"
        aria-hidden="true"
      />
    </template>

    <EmptyState v-else text="输入关键词开始搜索，支持标题、作者、标签" icon="✏️" />
  </div>
</template>

<style scoped>
.search-bar {
  display: flex;
  gap: 8px;
  margin-bottom: 16px;
}

.input-wrap {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 12px;
  border-radius: 10px;
  border: 1px solid var(--border);
  background: var(--card);
  color: var(--text-faint);
}

.input-wrap:focus-within {
  border-color: var(--accent);
}

input[type='search'] {
  flex: 1;
  border: none;
  outline: none;
  background: transparent;
  color: var(--text);
  font-size: 14.5px;
  padding: 9px 0;
}

input[type='search']::-webkit-search-cancel-button {
  display: none;
}

.clear {
  border: none;
  background: transparent;
  color: var(--text-faint);
  font-size: 18px;
  cursor: pointer;
  padding: 0 2px;
}

.sort-select {
  border-radius: 10px;
  border: 1px solid var(--border);
  background: var(--card);
  color: var(--text);
  padding: 0 8px;
  font-size: 13.5px;
}

.loading-more {
  display: flex;
  justify-content: center;
  padding: 22px 0;
}

.list-end {
  text-align: center;
  color: var(--text-faint);
  font-size: 12.5px;
  padding: 26px 0 40px;
}
</style>
