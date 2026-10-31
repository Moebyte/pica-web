import { request, isApiError } from './http'

/**
 * Sign queue for origin cover URLs (favorites / legacy history rows).
 * Each unique URL is signed once through /api/img/sign and cached; requests
 * run through a small concurrency-limited queue so a huge favorites folder
 * cannot stampede the gateway.
 */

const CACHE_LIMIT = 800
const MAX_ACTIVE = 4
const MAX_PENDING = 400

const cache = new Map<string, string>()
let active = 0
const pending: { url: string; resolve: (v: string) => void }[] = []

export function cachedSignedUrl(url: string): string | undefined {
  return cache.get(url)
}

export function signImageUrl(url: string): Promise<string> {
  if (!url) return Promise.resolve('')
  // Already an origin-relative /api/img/... URL — nothing to sign.
  if (!/^https?:\/\//i.test(url)) return Promise.resolve(url)
  const hit = cache.get(url)
  if (hit) return Promise.resolve(hit)
  if (pending.length >= MAX_PENDING) return Promise.resolve('')
  return new Promise<string>((resolve) => {
    pending.push({ url, resolve })
    drain()
  })
}

function drain(): void {
  while (active < MAX_ACTIVE && pending.length > 0) {
    const job = pending.shift()
    if (!job) break
    active++
    signOne(job.url)
      .then((signed) => {
        if (cache.size >= CACHE_LIMIT) {
          const first = cache.keys().next()
          if (!first.done) cache.delete(first.value)
        }
        cache.set(job.url, signed)
        job.resolve(signed)
      })
      .catch(() => job.resolve(''))
      .finally(() => {
        active--
        drain()
      })
  }
}

async function signOne(url: string): Promise<string> {
  const api = `/api/img/sign?url=${encodeURIComponent(url)}&ttl=86400`
  try {
    const data = await request<{ url?: string }>(api)
    return data.url ?? ''
  } catch (e) {
    if (isApiError(e) && e.status === 404) return ''
    throw e
  }
}
