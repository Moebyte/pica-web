import { nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted } from 'vue'

/**
 * Keep-alive friendly window scroll restore.
 *
 * The router resets scroll to 0 BEFORE deactivated hooks run, so the position
 * is tracked continuously with a passive scroll listener while the page is
 * active, then replayed (after nextTick + rAF) when the page is re-activated.
 */
export function useScrollRestore(): void {
  let saved = 0
  let active = true

  const track = () => {
    if (active && window.scrollY > 0) saved = window.scrollY
  }

  onMounted(() => {
    window.addEventListener('scroll', track, { passive: true })
  })

  onBeforeUnmount(() => {
    window.removeEventListener('scroll', track)
  })

  onDeactivated(() => {
    active = false
  })

  onActivated(() => {
    active = true
    const top = saved
    if (top <= 0) return
    void nextTick(() => {
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          if (Math.abs(window.scrollY - top) > 4) {
            window.scrollTo({ top, behavior: 'instant' as ScrollBehavior })
          }
        })
      })
    })
  })
}
