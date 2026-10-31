<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { getTabs, explore, type TabInfo } from '@/api/sources'
import { useAppStore } from '@/stores/app'
import { usePagedList } from '@/composables/usePagedList'
import { useInfiniteSentinel } from '@/composables/useInfiniteSentinel'
import { useScrollRestore } from '@/composables/useScrollRestore'
import ComicGrid from '@/components/ComicGrid.vue'
import ErrorRetry from '@/components/ErrorRetry.vue'
import EmptyState from '@/components/EmptyState.vue'

defineOptions({ name: 'ExplorePage' })

const app = useAppStore()
const { currentSrc } = storeToRefs(app)

const tabs = ref<TabInfo[]>([])
const tabsLoading = ref(false)
const tabsError = ref('')
const activeTab = ref('')

const sentinel = ref<HTMLElement | null>(null)

const list = usePagedList(async (page) => {
  const res = await explore(currentSrc.value, { tab: activeTab.value, page })
  return { items: res.comics, maxPage: res.maxPage }
})

useInfiniteSentinel(
  sentinel,
  () => void list.loadMore(),
  () => !list.loading.value && !list.loadingMore.value && !list.error.value && !list.done(),
)

useScrollRestore()

async function loadTabs(): Promise<void> {
  tabsLoading.value = true
  tabsError.value = ''
  try {
    tabs.value = await getTabs(currentSrc.value)
    const first = tabs.value[0]
    if (!first) {
      tabs.value = []
      return
    }
    if (!tabs.value.some((t) => t.id === activeTab.value)) {
      activeTab.value = first.id
    }
    await list.reload()
  } catch (e) {
    tabsError.value = e instanceof Error ? e.message : String(e)
  } finally {
    tabsLoading.value = false
  }
}

function selectTab(id: string): void {
  if (id === activeTab.value) return
  activeTab.value = id
  void list.reload()
}

onMounted(() => {
  void app.loadSources()
  void loadTabs()
})

watch(currentSrc, () => {
  activeTab.value = ''
  void loadTabs()
  window.scrollTo({ top: 0 })
})
</script>

<template>
  <div class="page">
    <div class="tabs-row">
      <div v-if="tabsLoading && tabs.length === 0" class="tab-skeletons">
        <div v-for="i in 5" :key="i" class="skeleton tab-sk" />
      </div>
      <template v-else>
        <button
          v-for="t in tabs"
          :key="t.id"
          class="tab-btn"
          :class="{ active: t.id === activeTab }"
          @click="selectTab(t.id)"
        >
          {{ t.title }}
        </button>
      </template>
    </div>

    <div v-if="tabs.length === 0 && tabsError && !tabsLoading">
      <ErrorRetry :message="`分类加载失败：${tabsError}`" @retry="loadTabs" />
    </div>

    <template v-else>
      <ComicGrid
        v-if="list.items.value.length > 0 || list.loading.value"
        :comics="list.items.value"
        :src="currentSrc"
        :skeleton="list.loading.value ? 12 : 0"
      />

      <EmptyState
        v-else-if="list.initialized.value && !list.error.value"
        text="这个分类暂时没有内容"
        icon="🔍"
      />

      <ErrorRetry
        v-if="list.error.value && list.items.value.length === 0"
        :message="`加载失败：${list.error.value}`"
        :retrying="list.loading.value"
        @retry="list.reload()"
      />

      <div v-if="list.error.value && list.items.value.length > 0" class="load-more-error">
        加载更多失败：{{ list.error.value }}
        <button class="btn small" @click="list.loadMore()">重试</button>
      </div>

      <div v-if="list.loadingMore.value" class="loading-more">
        <div class="spinner" />
      </div>

      <div v-if="list.done()" class="list-end">— 没有更多了 —</div>

      <div
        v-show="!list.done()"
        ref="sentinel"
        data-testid="infinite-sentinel"
        style="height: 1px"
        aria-hidden="true"
      />
    </template>
  </div>
</template>

<style scoped>
.tabs-row {
  display: flex;
  gap: 8px;
  overflow-x: auto;
  padding-bottom: 12px;
  margin-bottom: 6px;
  scrollbar-width: none;
}

.tabs-row::-webkit-scrollbar {
  display: none;
}

.tab-skeletons {
  display: flex;
  gap: 8px;
}

.tab-sk {
  width: 72px;
  height: 32px;
  border-radius: 999px;
  flex: none;
}

.tab-btn {
  flex: none;
  padding: 6px 16px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--card);
  color: var(--text-dim);
  font-size: 13.5px;
  cursor: pointer;
  transition: all 0.15s;
}

.tab-btn.active {
  background: var(--accent-soft);
  border-color: var(--accent);
  color: var(--accent-text);
  font-weight: 600;
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

.load-more-error {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--danger);
  font-size: 13.5px;
  padding: 14px 0;
}
</style>
