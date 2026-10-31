<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import {
  listDownloadJobs,
  deleteDownloadJob,
  downloadFileUrl,
  type DownloadJob,
} from '@/api/downloads'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import ErrorRetry from '@/components/ErrorRetry.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useScrollRestore } from '@/composables/useScrollRestore'
import { errMessage } from '@/api/http'

defineOptions({ name: 'DownloadsPage' })

const POLL_MS = 5000

const jobs = ref<DownloadJob[]>([])
const loading = ref(false)
const error = ref('')

const expandedId = ref('')

// delete dialog state
const deleteTarget = ref<DownloadJob | null>(null)
const deleteFiles = ref(true)
const deleting = ref(false)
const deleteError = ref('')

const statusMeta: Record<string, { label: string; cls: string }> = {
  running: { label: '下载中', cls: 'running' },
  queued: { label: '排队中', cls: 'queued' },
  pending: { label: '排队中', cls: 'queued' },
  done: { label: '已完成', cls: 'done' },
  partial: { label: '部分完成', cls: 'partial' },
  failed: { label: '失败', cls: 'failed' },
  paused: { label: '已暂停', cls: 'paused' },
  stopped: { label: '已停止', cls: 'paused' },
}

function meta(job: DownloadJob): { label: string; cls: string } {
  return (
    statusMeta[job.status] ?? { label: job.status || '未知', cls: 'unknown' }
  )
}

function progressPct(job: DownloadJob): number {
  if (job.total <= 0) return 0
  return Math.min(100, Math.round((job.done / job.total) * 100))
}

function canBrowse(job: DownloadJob): boolean {
  const s = job.status.toLowerCase()
  return (s === 'done' || s === 'partial') && job.files.length > 0
}

function toggleExpand(job: DownloadJob): void {
  expandedId.value = expandedId.value === job.id ? '' : job.id
}

async function refresh(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    jobs.value = await listDownloadJobs()
  } catch (e) {
    error.value = errMessage(e)
  } finally {
    loading.value = false
  }
}

function askDelete(job: DownloadJob): void {
  deleteTarget.value = job
  deleteFiles.value = true
  deleteError.value = ''
}

async function confirmDelete(checked: boolean): Promise<void> {
  const target = deleteTarget.value
  if (!target) return
  deleting.value = true
  deleteError.value = ''
  try {
    await deleteDownloadJob(target.id, checked)
    deleteTarget.value = null
    await refresh()
  } catch (e) {
    deleteError.value = errMessage(e)
  } finally {
    deleting.value = false
  }
}

// poll only while the tab is active
let timer: ReturnType<typeof setInterval> | null = null

function startPolling(): void {
  stopPolling()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') void refresh()
  }, POLL_MS)
}

function stopPolling(): void {
  if (timer) clearInterval(timer)
  timer = null
}

function onVisibility(): void {
  if (document.visibilityState === 'visible') void refresh()
}

onMounted(() => {
  void refresh()
  startPolling()
  document.addEventListener('visibilitychange', onVisibility)
})

onBeforeUnmount(() => {
  stopPolling()
  document.removeEventListener('visibilitychange', onVisibility)
})

useScrollRestore()

function sourceLabel(src: string): string {
  if (src === 'jm') return '禁漫'
  if (src === 'picacg') return 'PicACG'
  return src
}
</script>

<template>
  <div class="page">
    <ErrorRetry
      v-if="error && jobs.length === 0 && !loading"
      :message="`下载任务读取失败：${error}`"
      :retrying="loading"
      @retry="refresh"
    />

    <div v-else-if="loading && jobs.length === 0" class="jobs-loading">
      <div class="spinner" />
    </div>

    <EmptyState v-else-if="jobs.length === 0" icon="📥" text="没有下载任务（任务由网关/App 端创建）" />

    <template v-else>
      <div class="job-list" data-testid="download-jobs">
        <div v-for="job in jobs" :key="job.id" class="job" :data-status="job.status">
          <div class="job-head" @click="canBrowse(job) && toggleExpand(job)">
            <div class="job-title">
              <div class="t" :title="job.title">{{ job.title }}</div>
              <div class="sub">
                <span class="status-chip" :class="meta(job).cls">{{ meta(job).label }}</span>
                <span>{{ sourceLabel(job.source) }}</span>
                <span v-if="job.epName || job.ep">#{{ job.ep }} {{ job.epName }}</span>
                <span>{{ job.done }}/{{ job.total }}{{ job.failed ? ` · 失败 ${job.failed}` : '' }}</span>
              </div>
            </div>
            <button
              class="btn small danger del-btn"
              aria-label="删除任务"
              @click.stop="askDelete(job)"
            >
              删除
            </button>
          </div>
          <div class="bar">
            <div class="bar-fill" :class="meta(job).cls" :style="{ width: `${progressPct(job)}%` }" />
          </div>

          <div v-if="expandedId === job.id" class="files" data-testid="download-files">
            <img
              v-for="f in job.files"
              :key="f"
              :src="downloadFileUrl(job.dir, f)"
              :alt="f"
              loading="lazy"
              decoding="async"
            />
          </div>
        </div>
      </div>
    </template>

    <ConfirmDialog
      :open="!!deleteTarget"
      title="删除下载任务？"
      :message="deleteTarget ? `将移除任务「${deleteTarget.title}」` : ''"
      confirm-text="删除"
      danger
      checkbox-label="同时删除已下载的文件"
      :checkbox-default="true"
      :busy="deleting"
      @cancel="deleteTarget = null"
      @confirm="confirmDelete"
    />
    <p v-if="deleteError" class="del-error">{{ deleteError }}</p>
  </div>
</template>

<style scoped>
.job-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.job {
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--card);
  padding: 12px 14px;
}

.job-head {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  cursor: default;
}

.job-title {
  flex: 1;
  min-width: 0;
}

.t {
  font-size: 14.5px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.sub {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 10px;
  margin-top: 4px;
  font-size: 12.5px;
  color: var(--text-faint);
}

.status-chip {
  padding: 0 8px;
  border-radius: 6px;
  font-size: 11.5px;
  font-weight: 600;
}

.status-chip.running {
  background: rgba(90, 167, 232, 0.16);
  color: var(--info);
}

.status-chip.queued {
  background: var(--accent-soft);
  color: var(--accent-text);
}

.status-chip.done {
  background: rgba(76, 195, 138, 0.16);
  color: var(--ok);
}

.status-chip.partial {
  background: rgba(224, 168, 74, 0.16);
  color: var(--warn);
}

.status-chip.failed {
  background: var(--danger-soft);
  color: var(--danger);
}

.status-chip.paused,
.status-chip.unknown {
  background: var(--bg);
  color: var(--text-dim);
}

.bar {
  margin-top: 10px;
  height: 5px;
  border-radius: 4px;
  background: var(--bg);
  overflow: hidden;
}

.bar-fill {
  height: 100%;
  border-radius: 4px;
  background: var(--accent);
  transition: width 0.4s ease;
}

.bar-fill.done {
  background: var(--ok);
}

.bar-fill.partial {
  background: var(--warn);
}

.bar-fill.failed {
  background: var(--danger);
}

.files {
  margin-top: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 65vh;
  overflow-y: auto;
  border-top: 1px solid var(--border);
  padding-top: 12px;
}

.files img {
  width: 100%;
  border-radius: 8px;
  background: var(--skeleton-a);
}

.jobs-loading {
  display: flex;
  justify-content: center;
  padding: 60px 0;
}

.del-error {
  color: var(--danger);
  font-size: 13px;
  margin-top: 12px;
}
</style>
