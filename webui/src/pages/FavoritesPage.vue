<script setup lang="ts">
import { computed, onActivated, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  createFolder,
  deleteFolder,
  favoriteTimeMs,
  getFavorites,
  removeFavorite,
  sortedFolderNames,
  srcFromType,
  type FavoriteItem,
  type FavoritesBlob,
} from '@/api/store'
import { errMessage } from '@/api/http'
import { useToast } from '@/composables/useToast'
import SignedImg from '@/components/SignedImg.vue'
import ErrorRetry from '@/components/ErrorRetry.vue'
import EmptyState from '@/components/EmptyState.vue'
import ModalDialog from '@/components/ModalDialog.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import { useScrollRestore } from '@/composables/useScrollRestore'
import { relativeTime } from '@/utils/format'

defineOptions({ name: 'FavoritesPage' })

const router = useRouter()
const toast = useToast()

interface Folder {
  name: string
  items: FavoriteItem[]
}

const loading = ref(false)
const error = ref('')
const folders = ref<Folder[]>([])

const totalCount = computed(() => folders.value.reduce((n, f) => n + f.items.length, 0))

// new-folder dialog
const createOpen = ref(false)
const newFolderName = ref('')
const createError = ref('')
const creating = ref(false)

// folder delete confirmation
const folderTarget = ref<Folder | null>(null)
const deletingFolder = ref(false)

// per-row delete in flight
const itemBusy = ref('')

function applyBlob(blob: FavoritesBlob): void {
  folders.value = sortedFolderNames(blob).map((name) => ({
    name,
    items: blob.data.folders[name] ?? [],
  }))
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    applyBlob(await getFavorites())
  } catch (e) {
    error.value = errMessage(e)
  } finally {
    loading.value = false
  }
}

function open(item: FavoriteItem): void {
  const src = srcFromType(item.type, item.target)
  if (!src) return
  void router.push({ name: 'comic', params: { src, id: item.target } })
}

function timeLabel(item: FavoriteItem): string {
  const ms = favoriteTimeMs(item.time)
  return ms > 0 ? relativeTime(ms) : '—'
}

function openCreate(): void {
  newFolderName.value = ''
  createError.value = ''
  createOpen.value = true
}

async function confirmCreate(): Promise<void> {
  const name = newFolderName.value.trim()
  if (!name) {
    createError.value = '请输入文件夹名'
    return
  }
  if (folders.value.some((f) => f.name === name)) {
    createError.value = '该文件夹已存在'
    return
  }
  creating.value = true
  createError.value = ''
  try {
    applyBlob(await createFolder(name))
    createOpen.value = false
    toast.success(`已新建文件夹「${name}」`)
  } catch (e) {
    createError.value = `新建失败：${errMessage(e)}`
  } finally {
    creating.value = false
  }
}

async function deleteItem(item: FavoriteItem): Promise<void> {
  if (itemBusy.value) return
  itemBusy.value = item.target
  try {
    applyBlob(await removeFavorite(item.target))
    toast.success('已取消收藏')
  } catch (e) {
    toast.error(`删除失败：${errMessage(e)}`)
  } finally {
    itemBusy.value = ''
  }
}

function askDeleteFolder(folder: Folder): void {
  folderTarget.value = folder
}

async function confirmDeleteFolder(): Promise<void> {
  const target = folderTarget.value
  if (!target || deletingFolder.value) return
  deletingFolder.value = true
  try {
    applyBlob(await deleteFolder(target.name))
    folderTarget.value = null
    toast.success(`已删除文件夹「${target.name}」`)
  } catch (e) {
    toast.error(`删除文件夹失败：${errMessage(e)}`)
  } finally {
    deletingFolder.value = false
  }
}

// KeepAlive: refresh every time the page is (re)entered so rows added from
// the detail page or the Flutter app show up immediately.
onActivated(() => void load())
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

    <template v-else>
      <div class="fav-toolbar">
        <div class="fav-summary" data-testid="fav-summary">
          共 {{ folders.length }} 个文件夹 · {{ totalCount }} 个收藏
        </div>
        <button class="btn small" data-testid="new-folder" @click="openCreate">新建文件夹</button>
      </div>

      <EmptyState
        v-if="folders.length === 0"
        icon="💛"
        text="还没有收藏，去漫画详情页点「收藏」吧"
      >
        <template #action>
          <button class="btn small" @click="openCreate">新建文件夹</button>
        </template>
      </EmptyState>

      <template v-else>
        <section
          v-for="folder in folders"
          :key="folder.name"
          class="folder"
          :data-folder="folder.name"
        >
        <div class="folder-head">
          <h2>
            {{ folder.name }}
            <span class="count">({{ folder.items.length }})</span>
          </h2>
          <button
            class="btn small danger folder-del"
            data-testid="delete-folder"
            @click="askDeleteFolder(folder)"
          >
            删除文件夹
          </button>
        </div>

        <p v-if="folder.items.length === 0" class="folder-empty">空文件夹</p>

        <div v-else class="fav-grid">
          <div v-for="item in folder.items" :key="item.target" class="fav-card-wrap">
            <button class="fav-card" @click="open(item)">
              <div class="cover">
                <SignedImg :url="item.cover_path" :alt="item.name" />
              </div>
              <div class="name" :title="item.name">{{ item.name }}</div>
              <div class="meta">
                <span class="author">{{ item.author || '—' }}</span>
                <span class="time">{{ timeLabel(item) }}</span>
              </div>
            </button>
            <button
              class="del-item"
              :disabled="itemBusy === item.target"
              aria-label="删除收藏"
              data-testid="fav-delete"
              @click.stop="deleteItem(item)"
            >
              <span v-if="itemBusy === item.target" class="spinner tiny" />
              <template v-else>×</template>
            </button>
          </div>
        </div>
        </section>
      </template>
    </template>

    <ModalDialog
      :open="createOpen"
      title="新建文件夹"
      :busy="creating"
      width="380px"
      @close="createOpen = false"
    >
      <label class="new-folder">
        <span>文件夹名</span>
        <input
          v-model="newFolderName"
          type="text"
          maxlength="64"
          placeholder="例如：我的收藏"
          data-testid="new-folder-input"
          @input="createError = ''"
          @keyup.enter="confirmCreate"
        />
      </label>
      <p v-if="createError" class="dialog-error" data-testid="new-folder-error">{{ createError }}</p>
      <template #footer>
        <button class="btn" :disabled="creating" @click="createOpen = false">取消</button>
        <button class="btn primary" :disabled="creating" data-testid="new-folder-confirm" @click="confirmCreate">
          <span v-if="creating" class="spinner tiny" />
          新建
        </button>
      </template>
    </ModalDialog>

    <ConfirmDialog
      :open="!!folderTarget"
      title="删除文件夹？"
      :message="
        folderTarget
          ? `将删除文件夹「${folderTarget.name}」及其中 ${folderTarget.items.length} 个收藏`
          : ''
      "
      confirm-text="删除"
      danger
      :busy="deletingFolder"
      @cancel="folderTarget = null"
      @confirm="confirmDeleteFolder"
    />
  </div>
</template>

<style scoped>
.fav-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
}

.fav-summary {
  color: var(--text-faint);
  font-size: 13px;
}

.folder {
  margin-bottom: 30px;
}

.folder-head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}

h2 {
  flex: 1;
  min-width: 0;
  font-size: 15.5px;
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.count {
  color: var(--text-faint);
  font-weight: 400;
  font-size: 13px;
}

.folder-del {
  flex: none;
}

.folder-empty {
  margin: 0;
  padding: 14px 16px;
  border: 1px dashed var(--border);
  border-radius: 10px;
  color: var(--text-faint);
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

.fav-card-wrap {
  position: relative;
  min-width: 0;
}

.fav-card {
  display: block;
  width: 100%;
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

.del-item {
  position: absolute;
  top: 6px;
  right: 6px;
  width: 26px;
  height: 26px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  border: 1px solid rgba(255, 255, 255, 0.18);
  background: rgba(12, 12, 16, 0.66);
  color: #fff;
  font-size: 17px;
  line-height: 1;
  cursor: pointer;
  backdrop-filter: blur(4px);
  opacity: 0.82;
  transition: opacity 0.15s, background 0.15s;
}

.del-item:hover:not(:disabled) {
  opacity: 1;
  background: var(--danger);
}

.del-item:disabled {
  cursor: default;
  opacity: 0.5;
}

.spinner.tiny {
  width: 12px;
  height: 12px;
  border-width: 2px;
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

.dialog-error {
  color: var(--danger);
  font-size: 13px;
  margin: 10px 0 0;
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
