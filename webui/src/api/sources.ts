import { request } from './http'

/** Source keys supported by v1. */
export type SourceKey = 'jm' | 'picacg'

export function isSourceKey(v: string | undefined | null): v is SourceKey {
  return v === 'jm' || v === 'picacg'
}

export interface SourceInfo {
  key: SourceKey
  name: string
  needLogin: boolean
  loggedIn: boolean
}

export interface TabInfo {
  id: string
  title: string
}

export interface ComicBrief {
  id: string
  title: string
  subtitle?: string
  author?: string
  /** Origin-relative URL usable directly in <img src>. */
  cover: string
  pageCount: number
  chapters: number
  tags: string[]
}

export interface ChapterInfo {
  id: string
  title: string
  order: number
}

export interface ComicTagGroups {
  [group: string]: string[]
}

export interface ComicDetail extends Omit<ComicBrief, 'tags' | 'chapters'> {
  favorites?: number
  /** unix seconds */
  uploadDate?: number
  tags: ComicTagGroups
  chapters: ChapterInfo[]
  thumbs: string[]
}

export interface PageResult {
  comics: ComicBrief[]
  page: number
  maxPage: number
}

export interface ExploreQuery {
  tab: string
  page: number
}

export interface SearchQuery {
  q: string
  page: number
  sort?: string
}

export const JM_SORTS: { value: string; label: string }[] = [
  { value: 'mr', label: '最新' },
  { value: 'mv', label: '最多观看' },
  { value: 'mp', label: '最多收藏' },
  { value: 'mfm', label: '最多喜欢' },
]

export async function listSources(): Promise<SourceInfo[]> {
  const data = await request<{ sources: SourceInfo[] }>('/api/src/sources')
  return data.sources ?? []
}

export async function getTabs(src: string): Promise<TabInfo[]> {
  const data = await request<{ tabs: TabInfo[] }>(`/api/src/${src}/tabs`)
  return data.tabs ?? []
}

export async function explore(src: string, query: ExploreQuery): Promise<PageResult> {
  const params = new URLSearchParams({ tab: query.tab, page: String(query.page) })
  return request<PageResult>(`/api/src/${src}/explore?${params}`)
}

export async function search(src: string, query: SearchQuery): Promise<PageResult> {
  const params = new URLSearchParams({ q: query.q, page: String(query.page) })
  if (query.sort) params.set('sort', query.sort)
  return request<PageResult>(`/api/src/${src}/search?${params}`)
}

export async function getComic(src: string, id: string): Promise<ComicDetail> {
  const data = await request<{ comic: ComicDetail }>(`/api/src/${src}/comic/${id}`)
  return data.comic
}

export async function getPages(
  src: string,
  id: string,
  ep?: string,
): Promise<string[]> {
  const params = new URLSearchParams()
  if (ep) params.set('ep', ep)
  const qs = params.size > 0 ? `?${params}` : ''
  const data = await request<{ pages: string[] }>(`/api/src/${src}/comic/${id}/pages${qs}`)
  return data.pages ?? []
}

export async function login(src: string, username: string, password: string): Promise<void> {
  await request(`/api/src/${src}/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
}

export async function logout(src: string): Promise<void> {
  await request(`/api/src/${src}/logout`, { method: 'POST' })
}
