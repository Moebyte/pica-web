import { request, isApiError } from './http'

/**
 * History blob shared with the Flutter app.
 * NOTE: the gateway store wraps the blob in `{ok,key,value}` where `value` is
 * the JSON-encoded blob string, and PUT expects the raw blob JSON as body.
 */

/** type field in history rows: picacg = 0, jm = 2. */
export const HISTORY_TYPE = {
  picacg: 0,
  jm: 2,
} as const

export interface HistoryRow {
  target: string
  title: string
  subtitle?: string
  cover?: string
  /** milliseconds */
  time: number
  type: number
  /** 1-based chapter order */
  ep: number
  /** 1-based page number */
  page: number
  /** chapter id */
  readEpisode: string
  max_page: number
}

export interface HistoryBlob {
  updated: number
  data: {
    history: HistoryRow[]
    imageFavorites: unknown[]
  }
}

const EMPTY_HISTORY: HistoryBlob = { updated: 0, data: { history: [], imageFavorites: [] } }

export async function getHistory(): Promise<HistoryBlob> {
  try {
    const res = await request<{ ok: boolean; value: string }>('/api/store/history')
    const blob = JSON.parse(res.value) as HistoryBlob
    if (!blob || typeof blob !== 'object' || !blob.data) return EMPTY_HISTORY
    if (!Array.isArray(blob.data.history)) blob.data.history = []
    if (!Array.isArray(blob.data.imageFavorites)) blob.data.imageFavorites = []
    return blob
  } catch (e) {
    if (isApiError(e) && e.status === 404) return EMPTY_HISTORY
    throw e
  }
}

/** PUT the full blob; `updated` is refreshed to now (unix seconds). */
export async function putHistory(blob: HistoryBlob, keepalive = false): Promise<void> {
  const payload: HistoryBlob = { ...blob, updated: Math.floor(Date.now() / 1000) }
  await request('/api/store/history', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
    keepalive,
  })
}

/** GET -> upsert row by target -> PUT. Returns rows actually stored. */
export async function upsertHistoryRow(row: HistoryRow, keepalive = false): Promise<void> {
  const blob = await getHistory()
  const rows = blob.data.history
  const idx = rows.findIndex((r) => r.target === row.target)
  if (idx >= 0) rows[idx] = { ...rows[idx], ...row }
  else rows.unshift(row)
  await putHistory(blob, keepalive)
}

/**
 * Pagehide-safe upsert: a GET cannot be relied upon while the page unloads,
 * so the row is merged into the last known blob and pushed with a single
 * keepalive PUT.
 */
export function putHistoryRowKeepalive(row: HistoryRow, base: HistoryBlob): void {
  const rows = [...base.data.history]
  const idx = rows.findIndex((r) => r.target === row.target)
  if (idx >= 0) rows[idx] = { ...rows[idx], ...row }
  else rows.unshift(row)
  const blob: HistoryBlob = {
    updated: Math.floor(Date.now() / 1000),
    data: { history: rows, imageFavorites: [...base.data.imageFavorites] },
  }
  void fetch('/api/store/history', {
    method: 'PUT',
    credentials: 'include',
    keepalive: true,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(blob),
  }).catch(() => {
    /* best effort */
  })
}

export interface FavoriteItem {
  target: string
  name: string
  author: string
  type: number
  tags: string[]
  /** origin URL, needs /api/img/sign wrapping before display */
  cover_path: string
  time: number
  display_order?: number
}

export interface FavoritesBlob {
  updated?: number
  data: {
    folderOrder: { folder_name: string; order_value: number }[]
    folders: Record<string, FavoriteItem[]>
  }
}

const EMPTY_FAVORITES: FavoritesBlob = {
  updated: 0,
  data: { folderOrder: [], folders: {} },
}

export async function getFavorites(): Promise<FavoritesBlob> {
  try {
    const res = await request<{ ok: boolean; value: string }>('/api/store/favorites')
    const blob = JSON.parse(res.value) as FavoritesBlob
    if (!blob || typeof blob !== 'object' || !blob.data) return EMPTY_FAVORITES
    if (!Array.isArray(blob.data.folderOrder)) blob.data.folderOrder = []
    if (!blob.data.folders || typeof blob.data.folders !== 'object') blob.data.folders = {}
    return blob
  } catch (e) {
    if (isApiError(e) && e.status === 404) return EMPTY_FAVORITES
    throw e
  }
}

/** Map a history/favorite row type back to its source key. */
export function srcFromType(type: number, target = ''): 'picacg' | 'jm' | '' {
  if (type === HISTORY_TYPE.picacg) return 'picacg'
  if (type === HISTORY_TYPE.jm) return 'jm'
  // legacy rows written by the Flutter app may use other enum values:
  // fall back to id shape (picacg ids are 24-char hex, jm ids are numeric).
  if (/^\d+$/.test(target)) return 'jm'
  if (/^[0-9a-f]{24}$/i.test(target)) return 'picacg'
  return ''
}
