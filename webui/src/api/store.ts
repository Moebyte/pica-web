import { request, isApiError } from './http'

/**
 * History / favorites blobs shared with the Flutter app.
 * NOTE: the gateway store wraps the blob in `{ok,key,value}` where `value` is
 * the JSON-encoded blob string, and PUT expects the raw blob JSON as body.
 */

/** type field in history/favorite rows: picacg = 0, jm = 2. */
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

// ---------------------------------------------------------------------------
// Favorites (shared blob with the Flutter app)
// ---------------------------------------------------------------------------

/**
 * One favorites row, in the exact shape Flutter writes:
 * - `tags` is a comma-joined string
 * - `time` is local "YYYY-MM-DD HH:mm:ss" (legacy rows may carry a number)
 * - `display_order` is the folder-local ordering hint
 * - `cover_path` is the *origin* cover URL. v1 limitation: the semantic API
 *   only exposes signed relative /api/img URLs (which Flutter's Image.network
 *   cannot load), so rows created here store an empty string and both UIs
 *   fall back to a placeholder.
 */
export interface FavoriteItem {
  target: string
  name: string
  author: string
  type: number
  tags: string | string[]
  cover_path: string
  time: string | number
  display_order?: number
}

export interface FolderOrderEntry {
  folder_name: string
  order_value: number
}

export interface FavoritesBlob {
  updated?: number
  data: {
    folderOrder: FolderOrderEntry[]
    folders: Record<string, FavoriteItem[]>
    /** Unknown keys written by Flutter must survive a GET -> PUT round trip. */
    [key: string]: unknown
  }
}

export interface FavoriteInput {
  target: string
  name: string
  author: string
  type: number
  /** comma-joined tags */
  tags: string
}

const EMPTY_FAVORITES: FavoritesBlob = {
  updated: 0,
  data: { folderOrder: [], folders: {} },
}

function normalizeFavorites(raw: unknown): FavoritesBlob {
  if (!raw || typeof raw !== 'object') return { ...EMPTY_FAVORITES, data: { folderOrder: [], folders: {} } }
  const blob = raw as FavoritesBlob
  if (!blob.data || typeof blob.data !== 'object') blob.data = { folderOrder: [], folders: {} }
  if (!Array.isArray(blob.data.folderOrder)) blob.data.folderOrder = []
  if (!blob.data.folders || typeof blob.data.folders !== 'object') blob.data.folders = {}
  for (const name of Object.keys(blob.data.folders)) {
    if (!Array.isArray(blob.data.folders[name])) blob.data.folders[name] = []
  }
  return blob
}

export async function getFavorites(): Promise<FavoritesBlob> {
  try {
    const res = await request<{ ok?: boolean; value?: string } & Partial<FavoritesBlob>>(
      '/api/store/favorites',
    )
    // The gateway wraps blobs as {ok,key,value}; tolerate a direct blob too.
    const raw = typeof res.value === 'string' ? (JSON.parse(res.value) as unknown) : res
    return normalizeFavorites(raw)
  } catch (e) {
    if (isApiError(e) && e.status === 404) return normalizeFavorites(EMPTY_FAVORITES)
    throw e
  }
}

/** PUT the full blob; `updated` is refreshed to now (unix seconds). */
export async function putFavorites(blob: FavoritesBlob): Promise<void> {
  const payload: FavoritesBlob = { ...blob, updated: Math.floor(Date.now() / 1000) }
  await request('/api/store/favorites', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

/**
 * Serial write lock: every mutation is GET -> modify -> PUT, chained so two
 * UI actions can never interleave and clobber each other's blob.
 */
let writeChain: Promise<unknown> = Promise.resolve()

function enqueueWrite<T>(task: () => Promise<T>): Promise<T> {
  const run = writeChain.then(task, task)
  writeChain = run.then(
    () => undefined,
    () => undefined,
  )
  return run
}

/** Queued GET -> mutate -> PUT. Returns the blob that was written. */
export function mutateFavorites(
  mutate: (blob: FavoritesBlob) => void,
): Promise<FavoritesBlob> {
  return enqueueWrite(async () => {
    const blob = await getFavorites()
    mutate(blob)
    blob.updated = Math.floor(Date.now() / 1000)
    await putFavorites(blob)
    return blob
  })
}

/** Sorted folder names following folderOrder (unknown folders go last). */
export function sortedFolderNames(blob: FavoritesBlob | null | undefined): string[] {
  if (!blob) return []
  const order = new Map(blob.data.folderOrder.map((f) => [f.folder_name, f.order_value] as const))
  return Object.keys(blob.data.folders).sort((a, b) => {
    const oa = order.get(a)
    const ob = order.get(b)
    if (oa === undefined && ob === undefined) return a.localeCompare(b)
    if (oa === undefined) return 1
    if (ob === undefined) return -1
    return oa - ob
  })
}

/** Folder containing `target`, or '' when not favorited. */
export function findFavoriteFolder(
  blob: FavoritesBlob | null | undefined,
  target: string,
): string {
  if (!blob || !target) return ''
  for (const [name, items] of Object.entries(blob.data.folders)) {
    if (items.some((r) => r.target === target)) return name
  }
  return ''
}

export function maxDisplayOrder(items: FavoriteItem[]): number {
  let max = 0
  for (const row of items) {
    const n = Number(row.display_order)
    if (Number.isFinite(n) && n > max) max = n
  }
  return max
}

/** Local "YYYY-MM-DD HH:mm:ss" (the format Flutter writes). */
export function formatFavoriteTime(d: Date = new Date()): string {
  const p = (n: number): string => String(n).padStart(2, '0')
  return (
    `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ` +
    `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
  )
}

/** Parse a Flutter timestamp ("YYYY-MM-DD HH:mm:ss", local) to ms. */
export function favoriteTimeMs(t: string | number | undefined): number {
  if (typeof t === 'number' && Number.isFinite(t)) return t
  if (typeof t === 'string' && t) {
    const m = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2}):(\d{2})/.exec(t)
    if (m) {
      return new Date(
        Number(m[1]),
        Number(m[2]) - 1,
        Number(m[3]),
        Number(m[4]),
        Number(m[5]),
        Number(m[6]),
      ).getTime()
    }
    const parsed = Date.parse(t)
    if (Number.isFinite(parsed)) return parsed
  }
  return 0
}

function ensureFolder(blob: FavoritesBlob, name: string): void {
  if (Object.prototype.hasOwnProperty.call(blob.data.folders, name)) return
  blob.data.folders[name] = []
  let max = 0
  for (const entry of blob.data.folderOrder) {
    const n = Number(entry.order_value)
    if (Number.isFinite(n) && n > max) max = n
  }
  blob.data.folderOrder.push({ folder_name: name, order_value: max + 1 })
}

/** Add a row to a folder (removing the same target from other folders). */
export function addFavoriteToFolder(input: FavoriteInput, folder: string): Promise<FavoritesBlob> {
  return mutateFavorites((blob) => {
    ensureFolder(blob, folder)
    for (const [name, items] of Object.entries(blob.data.folders)) {
      if (name !== folder) {
        blob.data.folders[name] = items.filter((r) => r.target !== input.target)
      }
    }
    const items = blob.data.folders[folder] ?? []
    blob.data.folders[folder] = items
    if (items.some((r) => r.target === input.target)) return
    items.push({
      target: input.target,
      name: input.name,
      author: input.author,
      type: input.type,
      tags: input.tags,
      // v1: keep empty; see FavoriteItem.cover_path above.
      cover_path: '',
      time: formatFavoriteTime(),
      display_order: maxDisplayOrder(items) + 1,
    })
  })
}

/** Remove a target from whichever folder holds it. */
export function removeFavorite(target: string): Promise<FavoritesBlob> {
  return mutateFavorites((blob) => {
    for (const [name, items] of Object.entries(blob.data.folders)) {
      blob.data.folders[name] = items.filter((r) => r.target !== target)
    }
  })
}

/** Create an empty folder (idempotent; preserves folderOrder). */
export function createFolder(name: string): Promise<FavoritesBlob> {
  return mutateFavorites((blob) => {
    ensureFolder(blob, name)
  })
}

/** Delete a folder and all its rows. Other blob fields are preserved. */
export function deleteFolder(name: string): Promise<FavoritesBlob> {
  return mutateFavorites((blob) => {
    delete blob.data.folders[name]
    blob.data.folderOrder = blob.data.folderOrder.filter((f) => f.folder_name !== name)
  })
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
