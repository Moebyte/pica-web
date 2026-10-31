import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'
import { listSources, isSourceKey, type SourceInfo } from '@/api/sources'

export type Theme = 'dark' | 'light'
export type ReaderMode = 'vertical' | 'paged'

const LS_SRC = 'v2.src'
const LS_THEME = 'v2.theme'
const LS_READER_MODE = 'v2.readerMode'

function readLS(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeLS(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    /* private mode */
  }
}

export const useAppStore = defineStore('app', () => {
  // ---- sources ----
  const sources = ref<SourceInfo[]>([])
  const sourcesLoaded = ref(false)
  const sourcesError = ref('')

  const currentSrc = ref<string>(readLS(LS_SRC) ?? 'jm')
  if (!isSourceKey(currentSrc.value)) currentSrc.value = 'jm'

  const currentSource = computed(
    () => sources.value.find((s) => s.key === currentSrc.value) ?? null,
  )

  async function loadSources(): Promise<void> {
    sourcesError.value = ''
    try {
      sources.value = await listSources()
      sourcesLoaded.value = true
      if (!sources.value.some((s) => s.key === currentSrc.value)) {
        const first = sources.value[0]
        if (first) currentSrc.value = first.key
      }
    } catch (e) {
      sourcesError.value = e instanceof Error ? e.message : String(e)
    }
  }

  function setSource(key: string): void {
    if (!isSourceKey(key)) return
    currentSrc.value = key
    writeLS(LS_SRC, key)
  }

  watch(currentSrc, (v) => writeLS(LS_SRC, v))

  // ---- theme ----
  const storedTheme = readLS(LS_THEME)
  const theme = ref<Theme>(storedTheme === 'light' ? 'light' : 'dark')

  function applyTheme(): void {
    document.documentElement.dataset.theme = theme.value
  }

  function setTheme(t: Theme): void {
    theme.value = t
    writeLS(LS_THEME, t)
    applyTheme()
  }

  function toggleTheme(): void {
    setTheme(theme.value === 'dark' ? 'light' : 'dark')
  }

  // ---- reader prefs ----
  const storedMode = readLS(LS_READER_MODE)
  const readerMode = ref<ReaderMode>(storedMode === 'paged' ? 'paged' : 'vertical')

  function setReaderMode(m: ReaderMode): void {
    readerMode.value = m
    writeLS(LS_READER_MODE, m)
  }

  return {
    sources,
    sourcesLoaded,
    sourcesError,
    currentSrc,
    currentSource,
    loadSources,
    setSource,
    theme,
    setTheme,
    toggleTheme,
    applyTheme,
    readerMode,
    setReaderMode,
  }
})
