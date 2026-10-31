<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
} from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { getComic, getPages, isSourceKey, type ChapterInfo } from '@/api/sources'
import { useAppStore } from '@/stores/app'
import { useHistoryStore } from '@/stores/history'
import ErrorRetry from '@/components/ErrorRetry.vue'

const route = useRoute()
const router = useRouter()
const app = useAppStore()
const { readerMode } = storeToRefs(app)
const historyStore = useHistoryStore()

const src = computed(() => {
  const s = route.params.src as string
  return isSourceKey(s) ? s : 'jm'
})
const id = computed(() => (route.params.id as string) ?? '')

// ---- data ----
const title = ref('')
const cover = ref('')
const subtitle = ref('')
const pageCountTotal = ref(0)
const chapters = shallowRef<ChapterInfo[]>([])
const sortedChapters = computed(() => [...chapters.value].sort((a, b) => a.order - b.order))

const currentEp = ref('')
const pages = shallowRef<string[]>([])
const pagesLoading = ref(false)
const pagesError = ref('')
const comicLoading = ref(true)
const comicError = ref('')

const chapterIndex = computed(() => {
  const i = sortedChapters.value.findIndex((c) => c.id === currentEp.value)
  return i >= 0 ? i : 0
})
const currentChapter = computed(
  () => sortedChapters.value[chapterIndex.value] ?? { id: '', title: '', order: 1 },
)

// ---- viewer state ----
const pageIndex = ref(1) // 1-based
const chromeVisible = ref(true)
const containerEl = ref<HTMLElement | null>(null)

const hasPrevChapter = computed(() => chapterIndex.value > 0)
const hasNextChapter = computed(
  () => chapterIndex.value < sortedChapters.value.length - 1,
)

// ---- history recording ----
function recordNow(page = pageIndex.value): void {
  if (!currentEp.value) return
  historyStore.record({
    src: src.value,
    comicId: id.value,
    title: title.value,
    subtitle: subtitle.value,
    cover: cover.value,
    maxPage: pageCountTotal.value,
    ep: currentChapter.value.order,
    page,
    readEpisode: currentEp.value,
  })
}

// ---- data loading ----
async function loadComic(): Promise<void> {
  comicLoading.value = true
  comicError.value = ''
  try {
    const detail = await getComic(src.value, id.value)
    title.value = detail.title
    cover.value = detail.cover
    subtitle.value = detail.subtitle ?? ''
    pageCountTotal.value = detail.pageCount ?? 0
    chapters.value = detail.chapters ?? []
    // resolve initial episode: query ?ep= > history > first chapter
    const queryEp = route.query.ep as string
    const list = [...detail.chapters].sort((a, b) => a.order - b.order)
    const first = list[0]
    let ep = queryEp
    if (!ep || !list.some((c) => c.id === ep)) {
      const row = historyStore.find(id.value)
      ep = row?.readEpisode || first?.id || ''
    }
    currentEp.value = ep
    if (first) await loadPages()
    else pagesError.value = '没有可用章节'
  } catch (e) {
    comicError.value = e instanceof Error ? e.message : String(e)
  } finally {
    comicLoading.value = false
  }
}

async function loadPages(scrollTop = true): Promise<void> {
  if (!currentEp.value) return
  pagesLoading.value = true
  pagesError.value = ''
  try {
    const res = await getPages(src.value, id.value, currentEp.value)
    pages.value = res
    pageIndex.value = 1
    preloadAhead()
    if (scrollTop) {
      await nextTick()
      window.scrollTo({ top: 0 })
    }
  } catch (e) {
    pagesError.value = e instanceof Error ? e.message : String(e)
  } finally {
    pagesLoading.value = false
  }
}

// ---- chapter switching ----
async function switchChapter(epId: string): Promise<void> {
  if (!epId || epId === currentEp.value) return
  void historyStore.flush() // persist previous chapter progress
  currentEp.value = epId
  void router.replace({ query: { ep: epId } })
  await loadPages()
  recordNow(1)
}

function prevChapter(): void {
  if (!hasPrevChapter.value) return
  const target = sortedChapters.value[chapterIndex.value - 1]?.id
  if (target) void switchChapter(target)
}

function nextChapter(): void {
  if (!hasNextChapter.value) return
  const target = sortedChapters.value[chapterIndex.value + 1]?.id
  if (target) void switchChapter(target)
}

// ---- paging ----
function goToPage(n: number): void {
  const total = pages.value.length
  if (total === 0) return
  const target = Math.min(Math.max(1, n), total)
  if (target === pageIndex.value) return
  pageIndex.value = target
  if (readerMode.value === 'vertical') {
    const el = containerEl.value?.querySelector<HTMLElement>(`[data-page="${target}"]`)
    el?.scrollIntoView({ block: 'start' })
  }
  preloadAhead()
  recordNow(target)
}

function nextPage(): void {
  if (pageIndex.value < pages.value.length) goToPage(pageIndex.value + 1)
  else nextChapter()
}

function prevPage(): void {
  if (pageIndex.value > 1) goToPage(pageIndex.value - 1)
  else prevChapter()
}

/** Preload the next 2 images. */
function preloadAhead(): void {
  const list = pages.value
  const n = pageIndex.value
  for (let i = 1; i <= 2; i++) {
    const url = list[n - 1 + i]
    if (!url) break
    const img = new Image()
    img.decoding = 'async'
    img.src = url
  }
}

// ---- vertical mode: track the topmost visible page ----
let observer: IntersectionObserver | null = null

function setupObserver(): void {
  observer = new IntersectionObserver(
    (entries) => {
      let best: { page: number; ratio: number } | null = null
      for (const entry of entries) {
        const page = Number((entry.target as HTMLElement).dataset.page ?? '0')
        if (entry.isIntersecting && (!best || entry.intersectionRatio > best.ratio)) {
          best = { page, ratio: entry.intersectionRatio }
        }
      }
      if (best && best.page > 0 && best.page !== pageIndex.value) {
        pageIndex.value = best.page
        preloadAhead()
        recordNow(best.page)
      }
    },
    { threshold: [0, 0.1, 0.5, 0.9] },
  )
  observePages()
}

function observePages(): void {
  if (!observer || readerMode.value !== 'vertical') return
  containerEl.value?.querySelectorAll<HTMLElement>('[data-page]').forEach((el) => {
    observer?.observe(el)
  })
}

// ---- chrome (top bar) ----
let lastScrollTop = 0

function onScroll(): void {
  const y = window.scrollY
  if (y > lastScrollTop + 12 && chromeVisible.value) {
    chromeVisible.value = false
  } else if (y < lastScrollTop - 12 && !chromeVisible.value) {
    chromeVisible.value = true
  }
  lastScrollTop = y
}

function toggleChrome(): void {
  chromeVisible.value = !chromeVisible.value
}

let swipedAt = 0

/** Invisible tap zones: center toggles chrome, sides page in paged mode. */
function onViewportTap(e: MouseEvent): void {
  if (Date.now() - swipedAt < 350) return // ignore the click following a swipe
  const target = e.currentTarget as HTMLElement
  const rect = target.getBoundingClientRect()
  const x = e.clientX - rect.left
  if (readerMode.value === 'paged') {
    const zone = rect.width * 0.32
    if (x < zone) {
      prevPage()
      return
    }
    if (x > rect.width - zone) {
      nextPage()
      return
    }
  }
  toggleChrome()
}

// ---- keyboard ----
function onKeydown(e: KeyboardEvent): void {
  if (readerMode.value !== 'paged') return
  if (e.key === 'ArrowRight' || e.key === ' ' || e.key === 'PageDown') {
    e.preventDefault()
    nextPage()
  } else if (e.key === 'ArrowLeft' || e.key === 'PageUp') {
    e.preventDefault()
    prevPage()
  }
}

// ---- touch swipe (paged mode) ----
let touchStartX = 0
let touchStartY = 0

function onTouchStart(e: TouchEvent): void {
  const t = e.touches[0]
  if (!t) return
  touchStartX = t.clientX
  touchStartY = t.clientY
}

function onTouchEnd(e: TouchEvent): void {
  const t = e.changedTouches[0]
  if (!t) return
  const dx = t.clientX - touchStartX
  const dy = t.clientY - touchStartY
  if (Math.abs(dx) > 48 && Math.abs(dx) > Math.abs(dy) * 1.5) {
    swipedAt = Date.now()
    if (dx < 0) nextPage()
    else prevPage()
  }
}

// ---- lifecycle ----
function onVisibilityChange(): void {
  if (document.visibilityState === 'hidden') {
    void historyStore.flush()
  }
}

onMounted(async () => {
  void historyStore.load()
  await loadComic()
  recordNow(1)
  setupObserver()
  window.addEventListener('scroll', onScroll, { passive: true })
  window.addEventListener('keydown', onKeydown)
  document.addEventListener('visibilitychange', onVisibilityChange)
  window.addEventListener('pagehide', historyStore.flushKeepalive)
})

onBeforeUnmount(() => {
  recordNow()
  void historyStore.flush()
  observer?.disconnect()
  observer = null
  window.removeEventListener('scroll', onScroll)
  window.removeEventListener('keydown', onKeydown)
  document.removeEventListener('visibilitychange', onVisibilityChange)
  window.removeEventListener('pagehide', historyStore.flushKeepalive)
})

watch(pages, () => nextTick(observePages))
watch(readerMode, (mode) => {
  if (mode === 'vertical') {
    nextTick(observePages)
  } else {
    observer?.disconnect()
  }
})

function goBack(): void {
  if (window.history.length > 1) router.back()
  else void router.push({ name: 'comic', params: { src: src.value, id: id.value } })
}

const progressLabel = computed(() => {
  const total = pages.value.length
  const epLabel =
    sortedChapters.value.length > 1
      ? `章节 ${chapterIndex.value + 1}/${sortedChapters.value.length} · `
      : ''
  return `${epLabel}P ${pageIndex.value}${total ? `/${total}` : ''}`
})

const modeLabel = computed(() => (readerMode.value === 'paged' ? '单页' : '连续'))
</script>

<template>
  <div class="reader" data-testid="reader">
    <!-- top chrome -->
    <div class="reader-top" :class="{ visible: chromeVisible }">
      <button class="chrome-btn" aria-label="返回" @click="goBack">
        <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <path d="M14.5 5.5 8 12l6.5 6.5" />
        </svg>
      </button>
      <div class="chrome-title">
        <div class="t">{{ title || '加载中…' }}</div>
        <div class="p" data-testid="reader-progress">{{ progressLabel }}</div>
      </div>
      <button class="chrome-btn mode" :title="`切换为${modeLabel === '单页' ? '连续' : '单页'}模式`" @click="app.setReaderMode(readerMode === 'paged' ? 'vertical' : 'paged')">
        {{ modeLabel }}
      </button>
    </div>

    <!-- chapter switcher -->
    <div v-if="chapters.length > 1" class="chapter-switch" :class="{ visible: chromeVisible }">
      <button class="btn small" :disabled="!hasPrevChapter" @click="prevChapter()">上一話</button>
      <span class="ch-name">{{ currentChapter.title }}</span>
      <button class="btn small" :disabled="!hasNextChapter" @click="nextChapter()">下一話</button>
    </div>

    <ErrorRetry
      v-if="comicError"
      :message="`加载漫画失败：${comicError}`"
      :retrying="comicLoading"
      @retry="loadComic"
    />

    <div v-else class="viewport" :class="{ paged: readerMode === 'paged' }" @click="onViewportTap" @touchstart.passive="onTouchStart" @touchend.passive="onTouchEnd">
      <div v-if="pagesLoading" class="pages-loading">
        <div class="spinner" />
      </div>

      <ErrorRetry
        v-else-if="pagesError"
        :message="`加载图片失败：${pagesError}`"
        @retry="loadPages(false)"
      />

      <template v-else-if="readerMode === 'vertical'">
        <div ref="containerEl" class="strip" data-testid="reader-strip">
          <div v-for="(p, i) in pages" :key="p + i" class="strip-page" :data-page="i + 1">
            <img :src="p" :alt="`第 ${i + 1} 页`" :loading="i < 2 ? 'eager' : 'lazy'" decoding="async" />
            <span class="page-num">{{ i + 1 }} / {{ pages.length }}</span>
          </div>
        </div>
        <div class="strip-end" @click.stop>
          <template v-if="hasNextChapter">
            <button class="btn primary" @click="nextChapter()">进入下一話</button>
          </template>
          <template v-else>
            <span>已经是最后一話</span>
            <RouterLink class="btn" :to="{ name: 'comic', params: { src, id } }">返回详情</RouterLink>
          </template>
        </div>
      </template>

      <div v-else class="single" data-testid="reader-single">
        <img
          v-if="pages[pageIndex - 1]"
          :key="pageIndex"
          :src="pages[pageIndex - 1]"
          :alt="`第 ${pageIndex} 页`"
          class="single-img"
          draggable="false"
        />
        <div class="single-hint">← → / 空格 / 轻扫翻页 · 点中间显示工具栏</div>
        <div class="page-nav prev" aria-label="上一页" @click.stop="prevPage()">‹</div>
        <div class="page-nav next" aria-label="下一页" @click.stop="nextPage()">›</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.reader {
  position: relative;
  min-height: 100vh;
  background: #0a0a0e;
}

:root[data-theme='light'] .reader {
  background: #e9e9f0;
}

.reader-top {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  background: rgba(10, 10, 14, 0.82);
  backdrop-filter: blur(10px);
  color: #eee;
  transform: translateY(-100%);
  transition: transform 0.2s ease;
}

:root[data-theme='light'] .reader-top {
  background: rgba(250, 250, 252, 0.86);
  color: #222;
}

.reader-top.visible {
  transform: translateY(0);
}

.chrome-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 34px;
  height: 34px;
  border-radius: 9px;
  border: 1px solid rgba(255, 255, 255, 0.18);
  background: transparent;
  color: inherit;
  cursor: pointer;
  font-size: 12.5px;
  padding: 0 8px;
}

.chrome-btn:hover {
  background: rgba(255, 255, 255, 0.1);
}

:root[data-theme='light'] .chrome-btn {
  border-color: rgba(0, 0, 0, 0.15);
}

.chrome-title {
  flex: 1;
  min-width: 0;
}

.t {
  font-size: 14.5px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.p {
  font-size: 12px;
  opacity: 0.65;
}

.chapter-switch {
  position: fixed;
  top: 52px;
  left: 0;
  right: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 6px 12px;
  background: rgba(10, 10, 14, 0.78);
  backdrop-filter: blur(10px);
  color: #eee;
  transform: translateY(-140%);
  transition: transform 0.2s ease;
}

:root[data-theme='light'] .chapter-switch {
  background: rgba(250, 250, 252, 0.84);
  color: #222;
}

.chapter-switch.visible {
  transform: translateY(0);
}

.ch-name {
  font-size: 13px;
  max-width: 40vw;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.viewport {
  padding-top: 0;
}

.pages-loading {
  display: flex;
  justify-content: center;
  padding: 45vh 0;
}

.strip-page {
  position: relative;
  max-width: 900px;
  margin: 0 auto;
}

.strip-page img {
  width: 100%;
  height: auto;
  display: block;
  min-height: 40vh;
  background: var(--skeleton-a);
}

.page-num {
  position: absolute;
  right: 8px;
  bottom: 8px;
  font-size: 11px;
  color: rgba(255, 255, 255, 0.85);
  background: rgba(0, 0, 0, 0.45);
  border-radius: 6px;
  padding: 1px 7px;
  font-family: monospace;
}

.strip-end {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 32px 0 64px;
  color: var(--text-faint);
}

.single {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  padding: 0 4vw;
}

.single-img {
  max-width: min(900px, 100%);
  max-height: 96vh;
  object-fit: contain;
  width: auto;
  user-select: none;
}

.single-hint {
  position: absolute;
  bottom: 14px;
  left: 50%;
  transform: translateX(-50%);
  font-size: 11.5px;
  color: rgba(255, 255, 255, 0.5);
  white-space: nowrap;
  pointer-events: none;
}

:root[data-theme='light'] .single-hint {
  color: rgba(0, 0, 0, 0.45);
}

.page-nav {
  position: absolute;
  top: 0;
  bottom: 0;
  width: 12%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 44px;
  color: rgba(255, 255, 255, 0.35);
  cursor: pointer;
  user-select: none;
}

:root[data-theme='light'] .page-nav {
  color: rgba(0, 0, 0, 0.3);
}

.page-nav.prev {
  left: 0;
}

.page-nav.next {
  right: 0;
}
</style>
