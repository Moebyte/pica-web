import { request } from './http'

export interface DownloadJob {
  id: string
  title: string
  source: string
  comicId: string
  ep: number
  epName: string
  total: number
  done: number
  failed: number
  status: string
  dir: string
  files: string[]
}

export async function listDownloadJobs(): Promise<DownloadJob[]> {
  const data = await request<{ jobs: DownloadJob[] }>('/api/downloads/jobs')
  return data.jobs ?? []
}

export async function deleteDownloadJob(id: string, files: boolean): Promise<void> {
  const params = new URLSearchParams({ id, files: files ? 'true' : 'false' })
  await request(`/api/downloads/jobs?${params}`, { method: 'DELETE' })
}

export function downloadFileUrl(dir: string, file: string): string {
  const path = dir.endsWith('/') ? dir + file : `${dir}/${file}`
  return `/api/downloads/file?path=${encodeURIComponent(path)}`
}
