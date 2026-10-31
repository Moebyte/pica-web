import { request } from './http'

export interface DownloadJob {
  id: string
  title: string
  source: string
  comicId: string
  /** chapter id (JobView.EP is a string; older mock data may use a number) */
  ep: string | number
  epName: string
  total: number
  done: number
  failed: number
  status: string
  error?: string
  dir: string
  files: string[]
}

export interface DownloadSubmitResult {
  ok: boolean
  job: DownloadJob
  existed: boolean
}

/**
 * Ask the gateway to download one chapter of a comic to the server.
 * Errors: 401 when the source login is missing, 502 on upstream failures.
 */
export async function submitComicDownload(
  src: string,
  comicId: string,
  ep: string,
  epName: string,
): Promise<DownloadSubmitResult> {
  return request<DownloadSubmitResult>(
    `/api/src/${encodeURIComponent(src)}/comic/${encodeURIComponent(comicId)}/download`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ep, epName }),
    },
  )
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
