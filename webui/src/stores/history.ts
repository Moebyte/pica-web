import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  getHistory,
  upsertHistoryRow,
  putHistoryRowKeepalive,
  HISTORY_TYPE,
  type HistoryBlob,
  type HistoryRow,
} from '@/api/store'

export interface RecordInput {
  src: 'jm' | 'picacg'
  comicId: string
  title: string
  subtitle?: string
  cover?: string
  maxPage: number
  /** 1-based chapter order */
  ep: number
  /** 1-based page */
  page: number
  /** chapter id */
  readEpisode: string
}

/** Flush after every N page turns (throttle for store PUTs). */
const FLUSH_EVERY_PAGES = 5

export const useHistoryStore = defineStore('history', () => {
  const rows = ref<HistoryRow[]>([])
  const loaded = ref(false)
  const loading = ref(false)
  const error = ref('')

  let pending: HistoryRow | null = null
  let dirtyPages = 0
  let flushing = false
  /** Last known server blob, kept for pagehide-only keepalive PUTs. */
  let lastBlob: HistoryBlob | null = null

  async function load(force = false): Promise<void> {
    if (loading.value || (loaded.value && !force)) return
    loading.value = true
    error.value = ''
    try {
      const blob = await getHistory()
      lastBlob = blob
      rows.value = blob.data.history
      loaded.value = true
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    } finally {
      loading.value = false
    }
  }

  function find(target: string): HistoryRow | undefined {
    return rows.value.find((r) => r.target === target)
  }

  /** Merge a progress point into the pending row; flush by throttle rules. */
  function record(input: RecordInput): void {
    const row: HistoryRow = {
      target: input.comicId,
      title: input.title,
      time: Date.now(),
      type: HISTORY_TYPE[input.src],
      ep: input.ep,
      page: input.page,
      readEpisode: input.readEpisode,
      max_page: input.maxPage,
    }
    if (input.subtitle !== undefined) row.subtitle = input.subtitle
    if (input.cover !== undefined) row.cover = input.cover
    if (pending && pending.target === row.target) {
      pending = { ...pending, ...row }
    } else {
      void flush()
      pending = row
      dirtyPages = 0
    }
    dirtyPages++
    if (dirtyPages >= FLUSH_EVERY_PAGES) void flush()
  }

  /** Persist the pending row (chapter switch / leave reader). */
  async function flush(): Promise<void> {
    const row = pending
    if (!row || flushing) return
    pending = null
    dirtyPages = 0
    flushing = true
    try {
      await upsertHistoryRow(row)
      // keep the offline fallback blob in sync
      if (lastBlob) {
        const rr = [...lastBlob.data.history]
        const i = rr.findIndex((r) => r.target === row.target)
        if (i >= 0) rr[i] = { ...rr[i], ...row }
        else rr.unshift(row)
        lastBlob = {
          ...lastBlob,
          data: { ...lastBlob.data, history: rr },
        }
      }
      // refresh local cache
      const idx = rows.value.findIndex((r) => r.target === row.target)
      if (idx >= 0) rows.value.splice(idx, 1, { ...rows.value[idx], ...row } as HistoryRow)
      else rows.value.unshift(row)
      rows.value.sort((a, b) => b.time - a.time)
    } catch {
      // keep the row so the next flush retries it
      pending = pending ?? row
    } finally {
      flushing = false
    }
  }

  /**
   * Synchronous best-effort flush for pagehide/unload: a GET would die with
   * the page, so merge into the cached blob and fire a single keepalive PUT.
   */
  function flushKeepalive(): void {
    const row = pending
    if (!row) return
    pending = null
    dirtyPages = 0
    if (lastBlob) {
      putHistoryRowKeepalive(row, lastBlob)
    }
  }

  return { rows, loaded, loading, error, load, find, record, flush, flushKeepalive }
})
