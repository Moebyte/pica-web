import { ref, shallowRef } from 'vue'
import { errMessage } from '@/api/http'

export interface PagedFetcher<T> {
  (page: number): Promise<{ items: T[]; maxPage: number }>
}

/**
 * Paged list state machine shared by explore/search.
 * `reload()` resets to page 1, `loadMore()` appends the next page.
 */
export function usePagedList<T>(fetcher: PagedFetcher<T>) {
  const items = shallowRef<T[]>([])
  const page = ref(0)
  const maxPage = ref(1)
  const loading = ref(false)
  const loadingMore = ref(false)
  const error = ref('')
  const initialized = ref(false)

  function done(): boolean {
    return initialized.value && page.value >= maxPage.value
  }

  async function run(pageNo: number): Promise<void> {
    error.value = ''
    try {
      const res = await fetcher(pageNo)
      if (pageNo === 1) items.value = res.items
      else items.value = [...items.value, ...res.items]
      page.value = res.items.length > 0 || pageNo === 1 ? pageNo : res.maxPage
      maxPage.value = Math.max(1, res.maxPage)
      initialized.value = true
    } catch (e) {
      error.value = errMessage(e)
      throw e
    }
  }

  async function reload(): Promise<void> {
    if (loading.value) return
    loading.value = true
    initialized.value = false
    items.value = []
    page.value = 0
    maxPage.value = 1
    try {
      await run(1)
    } finally {
      loading.value = false
    }
  }

  async function loadMore(): Promise<void> {
    if (loading.value || loadingMore.value || !initialized.value) return
    if (page.value >= maxPage.value) return
    loadingMore.value = true
    try {
      await run(page.value + 1)
    } catch {
      /* error surfaced in state; sentinel keeps the retry button */
    } finally {
      loadingMore.value = false
    }
  }

  return { items, page, maxPage, loading, loadingMore, error, initialized, done, reload, loadMore }
}
