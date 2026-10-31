import { reactive } from 'vue'

/**
 * Tiny global toast queue. `ToastHost` (mounted once in App.vue) renders it.
 * Actions are router links so toasts can offer "查看下载" / "去登录".
 */

export type ToastType = 'info' | 'success' | 'error'

export interface ToastAction {
  label: string
  to: string
}

export interface ToastOptions {
  type?: ToastType
  /** ms; 0 keeps it until dismissed */
  timeout?: number
  action?: ToastAction
}

export interface ToastItem {
  id: number
  message: string
  type: ToastType
  action?: ToastAction
}

const items = reactive<ToastItem[]>([])
let seq = 0

export function dismissToast(id: number): void {
  const idx = items.findIndex((t) => t.id === id)
  if (idx >= 0) items.splice(idx, 1)
}

export function showToast(message: string, opts: ToastOptions = {}): number {
  const id = ++seq
  items.push({
    id,
    message,
    type: opts.type ?? 'info',
    ...(opts.action ? { action: opts.action } : {}),
  })
  const timeout = opts.timeout ?? 5000
  if (timeout > 0) {
    window.setTimeout(() => dismissToast(id), timeout)
  }
  return id
}

export function useToast() {
  return {
    items,
    dismiss: dismissToast,
    show: showToast,
    success: (message: string, opts: Omit<ToastOptions, 'type'> = {}) =>
      showToast(message, { ...opts, type: 'success' }),
    error: (message: string, opts: Omit<ToastOptions, 'type'> = {}) =>
      showToast(message, { ...opts, type: 'error' }),
  }
}
