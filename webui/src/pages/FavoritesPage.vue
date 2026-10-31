<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { getFavorites, srcFromType, type FavoriteItem } from '@/api/store'
import SignedImg from '@/components/SignedImg.vue'
import ErrorRetry from '@/components/ErrorRetry.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useScrollRestore } from '@/composables/useScrollRestore'
import { relativeTime } from '@/utils/format'

defineOptions({ name: 'FavoritesPage' })

const router = useRouter()

interface Folder {
  name: string
  items: FavoriteItem[]
}

const loading = ref(false)
const error = ref('')
const folders = ref<Folder[]>([])

const totalCount = computed(() => folders.value.reduce((n, f) => n + f.items.length, 0))

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const blob = await getFavorites()
    const order = new Map(
      blob.data.folderOrder.map((f) => [f.folder_name, f.order_value] as const),
    )
    const names = Object.keys(blob.data.folders)
    names.sort((a, b) => (order.get(a) ?? 0) - (order.get(b) ?? 0))
    folders.value = names
      .filter((n) => (blob.data.folders[n] ?? []).length > 0)
      .map((n) => ({ name: n, items: blob.data.folders[n] ?? [] }))
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

function open(item: FavoriteItem): void {
  const src = srcFromType(item.type, item.target)
  if (!src) return
  void router.push({ name: 'comic', params: { src, id: item.target } })
}

onMounted(() => void load())
useScrollRestore()
</script>

<template>
  <div class="page">
    <ErrorRetry
      v-if="error && !loading"
      :message="`收藏读取失败：${error}`"
      :retrying="loading"
      @retry="load"
    />

    <div v-else-if="loading && folders.length === 0" class="fav-grid">
      <div v-for="i in 8" :key="i" class="sk">
        <div class="skeleton sk-cover" />
        <div class="skeleton sk-line" />
      </div>
    </div>

    <EmptyState
      v-else-if="folders.length === 0"
      icon="💛"
      text="还没有收藏（v1 为只读展示，请在 App 端添加收藏）"
    />

    <template v-else>
      <div class="fav-summary">共 {{ folders.length }} 个文件夹 · {{ totalCount }} 个收藏</div>
      <section v-for="folder in folders" :key="folder.name" class="folder">
        <h2>
          {{ folder.name }}
          <span class="count">({{ folder.items.length }})</span>
        </h2>
        <div class="fav-grid">
          <button v-for="item in folder.items" :key="item.target" class="fav-card" @click="open(item)">
            <div class="cover">
              <SignedImg :url="item.cover_path" :alt="item.name" />
            </div>
            <div class="name" :title="item.name">{{ item.name }}</div>
            <div class="meta">
              <span class="author">{{ item.author || '—' }}</span>
              <span class="time">{{ relativeTime(item.time) }}</span>
            </div>
          </button>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.fav-summary {
  color: var(--text-faint);
  font-size: 13px;
  margin-bottom: 16px;
}

.folder {
  margin-bottom: 30px;
}

h2 {
  font-size: 15.5px;
  margin: 0 0 12px;
}

.count {
  color: var(--text-faint);
  font-weight: 400;
  font-size: 13px;
}

.fav-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(130px, 1fr));
  gap: 14px;
}

@media (max-width: 480px) {
  .fav-grid {
    grid-template-columns: repeat(2, 1fr);
    gap: 12px 10px;
  }
}

.fav-card {
  display: block;
  text-align: left;
  background: transparent;
  border: none;
  padding: 0;
  color: var(--text);
  cursor: pointer;
  min-width: 0;
}

.cover {
  border-radius: 10px;
  overflow: hidden;
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.25);
}

.name {
  margin-top: 7px;
  font-size: 13px;
  line-height: 1.35;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.meta {
  display: flex;
  justify-content: space-between;
  gap: 6px;
  margin-top: 3px;
  font-size: 11.5px;
  color: var(--text-faint);
}

.author {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
</style>
