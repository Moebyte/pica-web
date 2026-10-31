/** Normalized API error. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(message: string, status = 0, code = '') {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError
}

/** Extract a human readable message from unknown errors. */
export function errMessage(e: unknown): string {
  if (isApiError(e)) return e.message
  if (e instanceof Error) return e.message
  return String(e)
}

interface RequestOptions {
  method?: string
  body?: string
  headers?: Record<string, string>
  /** Used by history flushes on pagehide so the request survives navigation. */
  keepalive?: boolean
}

/**
 * Same-origin JSON request. All gateway endpoints answer with
 * `{ok:true,...}` on success and `{ok:false,error}` (or `{error,message}`) on
 * failure; non-2xx is normalized into an ApiError.
 */
export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const init: RequestInit = { method: opts.method ?? 'GET', credentials: 'include' }
  if (opts.body !== undefined) init.body = opts.body
  if (opts.headers !== undefined) init.headers = opts.headers
  if (opts.keepalive !== undefined) init.keepalive = opts.keepalive
  let res: Response
  try {
    res = await fetch(path, init)
  } catch {
    throw new ApiError('网络连接失败，请检查网关是否在线', 0, 'network')
  }
  if (!res.ok) {
    let message = `请求失败 (${res.status})`
    let code = ''
    try {
      const data = (await res.json()) as { error?: string; message?: string }
      message = data.error || data.message || message
      code = data.error ?? ''
    } catch {
      /* non-json error body, keep default message */
    }
    throw new ApiError(message, res.status, code)
  }
  return (await res.json()) as T
}
