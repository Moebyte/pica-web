<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  getComic,
  isSourceKey,
  type ChapterInfo,
  type ComicDetail,
} from '@/api/sources'
import { useHistoryStore } from '@/stores/history'
import {
  addFavoriteToFolder,
  findFavoriteFolder,
  getFavorites,
  removeFavorite,
  sortedFolderNames,
  type FavoritesBlob,
} from '@/api/store'
import { submitComicDownload } from '@/api/downloads'
import { errMessage, isApiError } from '@/api/http'
import { useToast } from '@/composables/useToast'
import LazyImg from '@/components/LazyImg.vue'
import ErrorRetry from '@/components/ErrorRetry.vue'
import ModalDialog from '@/components/ModalDialog.vue'
import { formatUploadDate, formatCount } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const historyStore = useHistoryStore()
const toast = useToast()

const src = computed(() => {
  const s = route.params.src as string
  return isSourceKey(s) ? s : 'jm'
})
const id = computed(() => (route.params.id as string) ?? '')

const comic = ref<ComicDetail | null>(null)
const loading = ref(false)
const error = ref('')

const sourceName = computed(() => (src.value === 'jm' ? '禁漫天堂' : 'PicACG'))

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

// ---------------------------------------------------------------------------
// Favorites (shared blob with Flutter; see api/store.ts)
// ---------------------------------------------------------------------------

const favBlob = ref<FavoritesBlob | null>(null)
const favLoading = ref(false)
const favBusy = ref(false)
const favDialogOpen = ref(false)
const favSelected = ref('')
const favNewFolder = ref('')
const favError = ref('')

const folderNames = computed(() => sortedFolderNames(favBlob.value))
const favFolder = computed(() =>
  comic.value ? findFavoriteFolder(favBlob.value, comic.value.id) : '',
)
const favorited = computed(() => favFolder.value !== '')

async function loadFavorites(): Promise<void> {
  if (favLoading.value) return
  favLoading.value = true
  try {
    favBlob.value = await getFavorites()
  } catch {
    /* non-fatal: the button stays 收藏 and the dialog retries on open */
  } finally {
    favLoading.value = false
  }
}

async function openFavoriteDialog(): Promise<void> {
  if (!comic.value) return
  favError.value = ''
  favNewFolder.value = ''
  favDialogOpen.value = true
  await loadFavorites()
  if (!favSelected.value || !folderNames.value.includes(favSelected.value)) {
    favSelected.value = folderNames.value[0] ?? ''
  }
}

function comicTagList(c: ComicDetail): string[] {
  const groups = c.tags
  if (!groups) return []
  const flat = Array.isArray(groups) ? groups : Object.values(groups).flat()
  return [...new Set(flat.map((t) => String(t).trim()).filter(Boolean))]
}

async function confirmFavorite(): Promise<void> {
  const c = comic.value
  if (!c || favBusy.value) return
  const folder = (favNewFolder.value.trim() || favSelected.value).trim()
  if (!folder) {
    favError.value = '请选择或输入文件夹名'
    return
  }
  favBusy.value = true
  favError.value = ''
  try {
    favBlob.value = await addFavoriteToFolder(
      {
        target: c.id,
        name: c.title,
        author: c.author ?? '',
        type: src.value === 'jm' ? 2 : 0,
        tags: comicTagList(c).join(','),
      },
      folder,
    )
    favDialogOpen.value = false
    toast.success(`已收藏到「${folder}」`)
  } catch (e) {
    favError.value = `收藏失败：${errMessage(e)}`
  } finally {
    favBusy.value = false
  }
}

async function toggleFavorite(): Promise<void> {
  const c = comic.value
  if (!c || favBusy.value) return
  if (!favorited.value) {
    await openFavoriteDialog()
    return
  }
  favBusy.value = true
  try {
    favBlob.value = await removeFavorite(c.id)
    toast.success('已取消收藏')
  } catch (e) {
    toast.error(`取消收藏失败：${errMessage(e)}`)
  } finally {
    favBusy.value = false
  }
}

// ---------------------------------------------------------------------------
// Server-side download submit (POST /api/src/{src}/comic/{id}/download)
// ---------------------------------------------------------------------------

/** '__all__' or a chapter id. */
const downloadChoice = ref('')
const downloadDialogOpen = ref(false)
const downloadBusy = ref(false)
const downloadDone = ref(0)
const downloadTotal = ref(0)

const downloadPct = computed(() =>
  downloadTotal.value > 0
    ? Math.round((downloadDone.value / downloadTotal.value) * 100)
    : 0,
)

function openDownload(): void {
  const c = comic.value
  if (!c || downloadBusy.value) return
  if (sortedChapters.value.length > 1) {
    downloadChoice.value = sortedChapters.value[0]?.id ?? '__all__'
    downloadDialogOpen.value = true
    return
  }
  const ch = sortedChapters.value[0]
  if (!ch) {
    toast.error('没有可下载的章节')
    return
  }
  void runDownload([ch])
}

function confirmDownload(): void {
  const list =
    downloadChoice.value === '__all__'
      ? sortedChapters.value
      : sortedChapters.value.filter((ch) => ch.id === downloadChoice.value)
  if (list.length === 0) return
  downloadDialogOpen.value = false
  void runDownload(list)
}

/** Serial submit: one POST per chapter; failures are collected, not fatal. */
async function runDownload(chapters: ChapterInfo[]): Promise<void> {
  const c = comic.value
  if (!c || chapters.length === 0) return
  downloadBusy.value = true
  downloadDone.value = 0
  downloadTotal.value = chapters.length
  let okCount = 0
  let failCount = 0
  let firstError = ''
  let unauthorized = false

  for (const ch of chapters) {
    try {
      await submitComicDownload(src.value, c.id, ch.id, ch.title || `第 ${ch.order} 話`)
      okCount++
    } catch (e) {
      if (isApiError(e) && e.status === 401) {
        unauthorized = true
        break
      }
      failCount++
      if (!firstError) firstError = errMessage(e)
    }
    downloadDone.value = okCount + failCount
  }

  downloadBusy.value = false
  downloadDone.value = downloadTotal.value

  if (unauthorized) {
    toast.show(`请先登录 ${sourceName.value} 后再下载`, {
      type: 'error',
      timeout: 8000,
      action: { label: '去登录', to: `/login/${src.value}` },
    })
    return
  }

  if (okCount > 0) {
    const extra = failCount > 0 ? `，${failCount} 章失败` : ''
    toast.show(`已加入下载队列（${okCount} 章${extra}）`, {
      type: failCount > 0 ? 'info' : 'success',
      action: { label: '查看下载', to: '/downloads' },
    })
    if (failCount > 0) {
      toast.error(`部分章节提交失败：${firstError || '未知错误'}`)
    }
  } else {
    toast.error(`下载提交失败：${firstError || '未知错误'}`)
  }
}

onMounted(() => {
  void load()
  void loadFavorites()
  void historyStore.load()
})

watch([src, id], () => {
  void load()
  void loadFavorites()
})
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
          <div class="actions">
            <button class="btn primary read-btn" data-testid="start-reading" @click="startReading()">
              {{ continueLabel || '开始阅读' }}
            </button>
            <button v-if="continueLabel" class="btn read-btn from-start" @click="startFromBeginning()">
              从头开始
            </button>
          </div>
          <div class="actions secondary-actions">
            <button
              class="btn action-btn"
              data-testid="download-btn"
              :disabled="downloadBusy"
              @click="openDownload"
            >
              <span v-if="downloadBusy" class="spinner tiny" />
              <svg v-else viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M12 4v10m0 0-4-4m4 4 4-4M5 19.5h14" />
              </svg>
              {{ downloadBusy ? `提交中 ${downloadDone}/${downloadTotal}` : '下载到服务器' }}
            </button>
            <button
              class="btn action-btn fav-btn"
              :class="{ active: favorited }"
              data-testid="fav-btn"
              :disabled="favBusy || favLoading"
              @click="toggleFavorite"
            >
              <span v-if="favBusy" class="spinner tiny" />
              <svg v-else viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M12 20s-7.5-4.6-9.3-8.9A5 5 0 0 1 12 6.3 5 5 0 0 1 21.3 11C19.5 15.4 12 20 12 20z" />
              </svg>
              {{ favorited ? '已收藏' : '收藏' }}
            </button>
          </div>

          <div v-if="downloadBusy" class="dl-progress" data-testid="download-progress">
            <div class="dl-progress-head">
              <span>正在提交下载 {{ downloadDone }}/{{ downloadTotal }}</span>
              <span>{{ downloadPct }}%</span>
            </div>
            <div class="dl-bar">
              <div class="dl-bar-fill" :style="{ width: `${downloadPct}%` }" />
            </div>
          </div>
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

    <!-- favorites: pick an existing folder or type a new one -->
    <ModalDialog
      :open="favDialogOpen"
      title="收藏到文件夹"
      :busy="favBusy"
      @close="favDialogOpen = false"
    >
      <div class="folder-pick" data-testid="fav-folder-list">
        <label
          v-for="name in folderNames"
          :key="name"
          class="folder-option"
          :class="{ active: favSelected === name && !favNewFolder.trim() }"
        >
          <input
            v-model="favSelected"
            type="radio"
            name="fav-folder"
            :value="name"
            @change="favNewFolder = ''"
          />
          <span class="folder-name">{{ name }}</span>
        </label>
        <p v-if="folderNames.length === 0" class="muted">还没有文件夹，输入名称新建一个</p>
      </div>
      <label class="new-folder">
        <span>新建文件夹</span>
        <input
          v-model="favNewFolder"
          type="text"
          maxlength="64"
          placeholder="例如：我的收藏"
          data-testid="fav-new-folder"
          @input="favError = ''"
          @keyup.enter="confirmFavorite"
        />
      </label>
      <p v-if="favError" class="dialog-error" data-testid="fav-error">{{ favError }}</p>
      <template #footer>
        <button class="btn" :disabled="favBusy" @click="favDialogOpen = false">取消</button>
        <button class="btn primary" :disabled="favBusy" data-testid="fav-confirm" @click="confirmFavorite">
          <span v-if="favBusy" class="spinner tiny" />
          收藏
        </button>
      </template>
    </ModalDialog>

    <!-- download: chapter picker for multi-chapter comics -->
    <ModalDialog
      :open="downloadDialogOpen"
      title="选择下载章节"
      :busy="downloadBusy"
      @close="downloadDialogOpen = false"
    >
      <div class="chapter-pick" data-testid="download-chapter-list">
        <label class="chapter-option all">
          <input
            v-model="downloadChoice"
            type="radio"
            name="dl-choice"
            value="__all__"
            data-testid="download-all"
          />
          <span>全部章节（{{ sortedChapters.length }}）</span>
        </label>
        <label v-for="ch in sortedChapters" :key="ch.id" class="chapter-option">
          <input
            v-model="downloadChoice"
            type="radio"
            name="dl-choice"
            :value="ch.id"
            :data-testid="`download-chapter-${ch.order}`"
          />
          <span class="ch-name">{{ ch.title || `第 ${ch.order} 話` }}</span>
        </label>
      </div>
      <template #footer>
        <button class="btn" :disabled="downloadBusy" @click="downloadDialogOpen = false">取消</button>
        <button
          class="btn primary"
          :disabled="downloadBusy"
          data-testid="download-confirm"
          @click="confirmDownload"
        >
          下载
        </button>
      </template>
    </ModalDialog>
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

.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.secondary-actions {
  margin-top: 10px;
}

.read-btn {
  min-width: 180px;
}

.from-start {
  margin-left: 0;
}

.action-btn {
  min-width: 140px;
}

.fav-btn.active {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent-text);
  font-weight: 600;
}

.dl-progress {
  margin-top: 12px;
  max-width: 420px;
}

.dl-progress-head {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  color: var(--text-faint);
  font-size: 12.5px;
  margin-bottom: 5px;
}

.dl-bar {
  height: 6px;
  border-radius: 4px;
  background: var(--bg);
  overflow: hidden;
}

.dl-bar-fill {
  height: 100%;
  border-radius: 4px;
  background: var(--accent);
  transition: width 0.25s ease;
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

/* ---- dialog contents ---- */

.folder-pick {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 240px;
  overflow-y: auto;
}

.folder-option,
.chapter-option {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 8px 10px;
  border-radius: 9px;
  border: 1px solid var(--border);
  background: var(--card);
  font-size: 14px;
  cursor: pointer;
}

.folder-option.active,
.chapter-option:hover {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.folder-option input,
.chapter-option input {
  accent-color: var(--accent);
  width: 15px;
  height: 15px;
  flex: none;
}

.folder-name,
.ch-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chapter-pick {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 300px;
  overflow-y: auto;
}

.chapter-option.all {
  font-weight: 600;
}

.new-folder {
  display: block;
  margin-top: 14px;
}

.new-folder span {
  display: block;
  font-size: 12.5px;
  color: var(--text-dim);
  margin-bottom: 6px;
}

.new-folder input {
  width: 100%;
  padding: 9px 11px;
  border-radius: 9px;
  border: 1px solid var(--border);
  background: var(--bg);
  color: var(--text);
  font-size: 14px;
  outline: none;
}

.new-folder input:focus {
  border-color: var(--accent);
}

.muted {
  color: var(--text-faint);
  font-size: 13px;
  margin: 4px 0;
}

.dialog-error {
  color: var(--danger);
  font-size: 13px;
  margin: 10px 0 0;
}

.spinner.tiny {
  width: 13px;
  height: 13px;
  border-width: 2px;
}

@media (max-width: 640px) {
  .head {
    flex-direction: column;
  }

  .cover {
    width: 150px;
  }

  .read-btn {
    min-width: 0;
    flex: 1;
  }

  .action-btn {
    flex: 1;
  }

  .chapter-list {
    grid-template-columns: 1fr;
  }
}
</style>
