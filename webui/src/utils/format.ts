/** Small formatting helpers. */

export function formatUploadDate(unixSeconds: number | undefined): string {
  if (!unixSeconds) return '未知'
  const d = new Date(unixSeconds * 1000)
  return d.toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
}

export function formatCount(n: number | undefined): string {
  if (n === undefined || n === null) return '-'
  if (n >= 10000) return `${(n / 10000).toFixed(1)}万`
  return String(n)
}

export function relativeTime(ms: number): string {
  const diff = Date.now() - ms
  if (diff < 0) return '未来'
  const min = 60_000
  if (diff < min) return '刚刚'
  if (diff < 60 * min) return `${Math.floor(diff / min)} 分钟前`
  if (diff < 24 * 60 * min) return `${Math.floor(diff / (60 * min))} 小时前`
  if (diff < 30 * 24 * 60 * min) return `${Math.floor(diff / (24 * 60 * min))} 天前`
  const d = new Date(ms)
  return d.toLocaleDateString('zh-CN')
}

export function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const parts: string[] = []
  if (d > 0) parts.push(`${d} 天`)
  if (h > 0) parts.push(`${h} 小时`)
  parts.push(`${m} 分钟`)
  return parts.join(' ')
}
