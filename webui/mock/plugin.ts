/**
 * Vite dev/preview middleware that mocks the semantic /api/src/* endpoints
 * while the real gateway does not expose them yet.
 *
 * The plugin probes the upstream gateway (default http://127.0.0.1:18081,
 * override with MOCK_UPSTREAM) on start and periodically: as soon as the real
 * semantic API answers, the mock disables itself and everything is proxied.
 * Store, downloads and image-sign requests are never intercepted.
 */

import type { Plugin, PreviewServer, ViteDevServer } from 'vite'
import type { IncomingMessage, ServerResponse } from 'node:http'
import { hashString, mulberry32, TABS, findComic, orderedPool, type MockComic } from './data'

const UPSTREAM = process.env.MOCK_UPSTREAM ?? 'http://127.0.0.1:18081'
const PROBE_INTERVAL_MS = 60_000
const ARTIFICIAL_LATENCY_MS = 40
const PAGE_SIZE = 20

const loginState: Record<string, boolean> = { jm: false, picacg: false }

let upstreamHasSemanticApi = false
let lastProbe = 0
let probing = false

export function mockApiPlugin(): Plugin {
  return {
    name: 'pica-mock-semantic-api',
    configureServer(server: ViteDevServer) {
      void probeUpstream()
      install(server.middlewares.use.bind(server.middlewares))
    },
    configurePreviewServer(server: PreviewServer) {
      void probeUpstream()
      install(server.middlewares.use.bind(server.middlewares))
    },
  }
}

type Use = (fn: (req: IncomingMessage, res: ServerResponse, next: () => void) => void) => void

function install(use: Use): void {
  use((req, res, next) => {
    void handle(req, res, next)
  })
}

async function handle(req: IncomingMessage, res: ServerResponse, next: () => void): Promise<void> {
  const url = new URL(req.url ?? '/', 'http://mock.local')
  const path = url.pathname

  if (path.startsWith('/api/img/mock/')) {
    serveMockImage(path, res)
    return
  }

  if (!path.startsWith('/api/src/')) {
    next()
    return
  }

  if (Date.now() - lastProbe > PROBE_INTERVAL_MS) void probeUpstream()
  if (upstreamHasSemanticApi) {
    next()
    return
  }

  await delay(ARTIFICIAL_LATENCY_MS)
  route(path, url, req, res)
}

async function probeUpstream(): Promise<void> {
  if (probing) return
  probing = true
  try {
    const res = await fetch(`${UPSTREAM}/api/src/sources`, {
      signal: AbortSignal.timeout(2500),
    })
    if (res.ok) {
      const data = (await res.json()) as { sources?: unknown[] }
      if (Array.isArray(data.sources) && data.sources.length > 0 && !upstreamHasSemanticApi) {
        upstreamHasSemanticApi = true
        console.log(`[pica-mock] real semantic API detected at ${UPSTREAM}, mock disabled`)
      }
    }
  } catch {
    /* gateway down or semantic API absent — keep mocking */
  } finally {
    lastProbe = Date.now()
    probing = false
  }
}

function delay(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms))
}

// ---------------------------------------------------------------------------
// Routing
// ---------------------------------------------------------------------------

function route(path: string, url: URL, req: IncomingMessage, res: ServerResponse): void {
  const parts = path.split('/').filter(Boolean) // ['api','src',...]
  const seg = parts.slice(2) // after ['api','src']
  const src = seg[0] ?? ''
  const rest = seg.slice(1).join('/')

  if (src === 'sources' && rest === '') return sendJson(res, sourcesPayload())
  if (!src || !(src in TABS)) return sendJson(res, { ok: false, error: 'unknown source' }, 404)

  if (rest === 'tabs') return sendJson(res, { tabs: TABS[src] })
  if (rest === 'explore') return handleExplore(src, url, res)
  if (rest === 'search') return handleSearch(src, url, res)
  if (rest === 'login') return void handleLogin(src, req, res)
  if (rest === 'logout') {
    loginState[src] = false
    return sendJson(res, { ok: true })
  }
  const comicMatch = /^comic\/([^/]+)(?:\/pages)?$/.exec(rest)
  if (comicMatch) {
    const id = comicMatch[1] ?? ''
    if (rest.endsWith('/pages')) return handlePages(src, id, url, res)
    return handleComic(src, id, res)
  }
  sendJson(res, { ok: false, error: 'not_found' }, 404)
}

function sourcesPayload() {
  return {
    sources: [
      { key: 'jm', name: '禁漫天堂', needLogin: false, loggedIn: loginState.jm },
      { key: 'picacg', name: 'PicACG', needLogin: true, loggedIn: loginState.picacg },
    ],
  }
}

function handleExplore(src: string, url: URL, res: ServerResponse): void {
  const tab = url.searchParams.get('tab') ?? (TABS[src]?.[0]?.id ?? '')
  const page = toPage(url.searchParams.get('page'))
  const pool = orderedPool(src, tab)
  sendPage(res, pool, page)
}

function handleSearch(src: string, url: URL, res: ServerResponse): void {
  const q = (url.searchParams.get('q') ?? '').trim()
  const sort = url.searchParams.get('sort') ?? 'mr'
  const page = toPage(url.searchParams.get('page'))
  if (!q) return sendJson(res, { comics: [], page: 1, maxPage: 1 })

  const pool = orderedPool(src, `search:${q}:${sort}`)
  const lower = q.toLowerCase()
  const matched = pool.filter(
    (c) =>
      c.title.toLowerCase().includes(lower) ||
      c.subtitle.toLowerCase().includes(lower) ||
      c.author.toLowerCase().includes(lower) ||
      c.tags.some((t) => t.toLowerCase().includes(lower)),
  )
  // Guarantee some results for any query (mirrors "search always returns
  // something" behaviour of the real sites for v1 testing).
  if (matched.length < PAGE_SIZE) {
    const rand = mulberry32(hashString(`synth:${q}:${src}`))
    const extra: MockComic[] = []
    for (let i = 0; matched.length + extra.length < Math.min(60, PAGE_SIZE * 3); i++) {
      const id = `synth-${src}-${hashString(`${q}:${i}`).toString(36)}`
      extra.push({
        id,
        title: `${q} 搜索结果 ${i + 1}`,
        subtitle: `${q} 相关 (${src === 'jm' ? '作者' : '汉化组'})`,
        author: q,
        cover: `/api/img/mock/cover-${src}-${id}.svg`,
        pageCount: 10 + Math.floor(rand() * 40),
        chapters:
          src === 'jm'
            ? [{ id: `${id}-c1`, title: '全一話', order: 1 }]
            : Array.from({ length: 2 + (i % 3) }, (_, n) => ({
                id: `${id}-c${n + 1}`,
                title: `第 ${n + 1} 話`,
                order: n + 1,
              })),
        tags: [q],
        categories: [q],
        languages: ['中文'],
        favorites: Math.floor(rand() * 5000),
        uploadDate: Math.floor(Date.now() / 1000) - i * 3600,
      })
    }
    matched.push(...extra)
  }
  sendPage(res, matched, page)
}

function handleComic(src: string, id: string, res: ServerResponse): void {
  const c = findComic(src, id) ?? synthComicFromId(src, id)
  if (!c) return sendJson(res, { ok: false, error: '漫画不存在' }, 404)
  sendJson(res, {
    comic: {
      id: c.id,
      title: c.title,
      subtitle: c.subtitle,
      author: c.author,
      cover: c.cover,
      pageCount: c.pageCount,
      favorites: c.favorites,
      uploadDate: c.uploadDate,
      tags: {
        分类: c.categories,
        语言: c.languages,
        作者: [c.author],
        标签: c.tags,
      },
      chapters: c.chapters,
      thumbs: c.chapters
        .slice(0, 2)
        .flatMap((ch) => [
          `/api/img/mock/page-${src}-${c.id}-${ch.id}-1.svg`,
          `/api/img/mock/page-${src}-${c.id}-${ch.id}-2.svg`,
        ]),
    },
  })
}

function handlePages(src: string, id: string, url: URL, res: ServerResponse): void {
  const c = findComic(src, id) ?? synthComicFromId(src, id)
  if (!c) return sendJson(res, { ok: false, error: '漫画不存在' }, 404)
  const ep = url.searchParams.get('ep')
  const chapter =
    c.chapters.find((ch) => ch.id === ep) ?? c.chapters.find((ch) => ch.order === 1) ?? c.chapters[0]
  const total = Math.max(6, Math.min(c.pageCount, Math.round(c.pageCount / Math.max(1, c.chapters.length))))
  const pages = Array.from(
    { length: total },
    (_, i) => `/api/img/mock/page-${src}-${id}-${chapter?.id ?? 'c1'}-${i + 1}.svg`,
  )
  sendJson(res, { pages })
}

async function handleLogin(src: string, req: IncomingMessage, res: ServerResponse): Promise<void> {
  const body = await readBody(req)
  let username = ''
  let password = ''
  try {
    const parsed = JSON.parse(body) as { username?: string; password?: string }
    username = parsed.username ?? ''
    password = parsed.password ?? ''
  } catch {
    return sendJson(res, { ok: false, error: '请求格式错误' }, 400)
  }
  await delay(300)
  if (username === 'demo' && password === 'demo') {
    loginState[src] = true
    return sendJson(res, { ok: true })
  }
  sendJson(res, { ok: false, error: '用户名或密码错误' }, 401)
}

function synthComicFromId(src: string, id: string): MockComic | undefined {
  if (!id.startsWith('synth-')) return undefined
  const rand = mulberry32(hashString(id))
  return {
    id,
    title: `搜索结果漫画 ${id.slice(-6)}`,
    subtitle: 'synth',
    author: 'synth',
    cover: `/api/img/mock/cover-${src}-${id}.svg`,
    pageCount: 12 + Math.floor(rand() * 20),
    chapters:
      src === 'jm'
        ? [{ id: `${id}-c1`, title: '全一話', order: 1 }]
        : Array.from({ length: 3 }, (_, n) => ({ id: `${id}-c${n + 1}`, title: `第 ${n + 1} 話`, order: n + 1 })),
    tags: ['synth'],
    categories: ['synth'],
    languages: ['中文'],
    favorites: 100,
    uploadDate: Math.floor(Date.now() / 1000),
  }
}

function sendPage(res: ServerResponse, pool: MockComic[], page: number): void {
  const maxPage = Math.max(1, Math.ceil(pool.length / PAGE_SIZE))
  const p = Math.min(Math.max(1, page), maxPage)
  const slice = pool.slice((p - 1) * PAGE_SIZE, p * PAGE_SIZE)
  sendJson(res, {
    comics: slice.map((c) => ({
      id: c.id,
      title: c.title,
      subtitle: c.subtitle,
      author: c.author,
      cover: c.cover,
      pageCount: c.pageCount,
      chapters: c.chapters.length,
      tags: c.tags,
    })),
    page: p,
    maxPage,
  })
}

function toPage(raw: string | null): number {
  const n = Number.parseInt(raw ?? '1', 10)
  return Number.isFinite(n) && n > 0 ? n : 1
}

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve) => {
    const chunks: Buffer[] = []
    req.on('data', (c: Buffer) => chunks.push(c))
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')))
  })
}

function sendJson(res: ServerResponse, data: unknown, status = 200): void {
  const body = JSON.stringify(data)
  res.statusCode = status
  res.setHeader('Content-Type', 'application/json; charset=utf-8')
  res.setHeader('Cache-Control', 'no-store')
  res.end(body)
}

// ---------------------------------------------------------------------------
// Mock images (deterministic SVG so every screen is visually verifiable)
// ---------------------------------------------------------------------------

function serveMockImage(path: string, res: ServerResponse): void {
  const svg = renderMockSvg(path)
  res.statusCode = 200
  res.setHeader('Content-Type', 'image/svg+xml; charset=utf-8')
  res.setHeader('Cache-Control', 'public, max-age=86400')
  res.end(svg)
}

function esc(s: string): string {
  return s
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
}

function renderMockSvg(path: string): string {
  const seed = hashString(path)
  const rand = mulberry32(seed)
  const hue = Math.floor(rand() * 360)
  const hue2 = (hue + 40 + Math.floor(rand() * 80)) % 360
  const isCover = path.includes('cover-')
  const name = path.split('/').pop() ?? 'img'

  if (isCover) {
    const parts = name.replace('.svg', '').split('-') // cover,src,id...
    const id = parts.slice(2).join('-')
    const label = id.startsWith('synth-') ? 'SYNTH' : id.slice(-6)
    return `<svg xmlns="http://www.w3.org/2000/svg" width="300" height="400" viewBox="0 0 300 400">
<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">
<stop offset="0" stop-color="hsl(${hue},45%,38%)"/><stop offset="1" stop-color="hsl(${hue2},55%,22%)"/>
</linearGradient></defs>
<rect width="300" height="400" fill="url(#g)"/>
<text x="150" y="196" font-size="34" fill="rgba(255,255,255,.92)" text-anchor="middle" font-family="sans-serif" font-weight="700">${esc(label)}</text>
<text x="150" y="238" font-size="15" fill="rgba(255,255,255,.55)" text-anchor="middle" font-family="monospace">mock ${esc(parts[1] ?? '')}</text>
</svg>`
  }

  // page image: parse page-<src>-<id>-<ep>-<n>.svg
  const m = /page-([a-z]+)-(.+)-(.+)-(\d+)\.svg$/.exec(name)
  const n = m?.[4] ?? '?'
  return `<svg xmlns="http://www.w3.org/2000/svg" width="800" height="1131" viewBox="0 0 800 1131">
<defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1">
<stop offset="0" stop-color="hsl(${hue},30%,30%)"/><stop offset="1" stop-color="hsl(${hue2},35%,14%)"/>
</linearGradient></defs>
<rect width="800" height="1131" fill="url(#g)"/>
<rect x="24" y="24" width="752" height="1083" rx="12" fill="none" stroke="rgba(255,255,255,.18)" stroke-width="2"/>
<text x="400" y="500" font-size="140" fill="rgba(255,255,255,.85)" text-anchor="middle" font-family="sans-serif" font-weight="800">${esc(n)}</text>
<text x="400" y="600" font-size="34" fill="rgba(255,255,255,.45)" text-anchor="middle" font-family="monospace">page ${esc(n)}</text>
</svg>`
}
