import { onBeforeUnmount, onMounted, type Ref } from 'vue'

/**
 * IntersectionObserver-driven infinite scroll sentinel.
 * Calls `onHit` whenever the sentinel enters the viewport while enabled.
 */
export function useInfiniteSentinel(
  sentinel: Ref<HTMLElement | null>,
  onHit: () => void,
  enabled: () => boolean,
): void {
  let observer: IntersectionObserver | null = null
  let firing = false

  onMounted(() => {
    observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting && enabled() && !firing) {
            firing = true
            try {
              onHit()
            } finally {
              // allow re-fire on next intersection change
              requestAnimationFrame(() => {
                firing = false
              })
            }
          }
        }
      },
      { rootMargin: '600px 0px' },
    )
    if (sentinel.value) observer.observe(sentinel.value)
  })

  onBeforeUnmount(() => {
    observer?.disconnect()
    observer = null
  })
}
