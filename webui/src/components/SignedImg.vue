<script setup lang="ts">
import { ref, watch } from 'vue'
import LazyImg from './LazyImg.vue'
import { cachedSignedUrl, signImageUrl } from '@/api/imgsign'

/**
 * Cover for URLs coming straight from the source site (favorites rows,
 * legacy history rows): wrapped through /api/img/sign before display.
 * Origin-relative /api/img/... URLs pass through untouched.
 */

const props = withDefaults(
  defineProps<{
    url: string
    alt?: string
    ratio?: string
  }>(),
  { alt: '', ratio: '2 / 3' },
)

const resolved = ref('')
const signing = ref(false)

async function resolve(): Promise<void> {
  if (!props.url) {
    resolved.value = ''
    return
  }
  if (!/^https?:\/\//i.test(props.url)) {
    resolved.value = props.url
    return
  }
  const hit = cachedSignedUrl(props.url)
  if (hit !== undefined && hit !== '') {
    resolved.value = hit
    return
  }
  signing.value = true
  resolved.value = ''
  const signed = await signImageUrl(props.url)
  signing.value = false
  resolved.value = signed
}

watch(() => props.url, () => void resolve(), { immediate: true })
</script>

<template>
  <div v-if="signing" class="skeleton sign-skeleton" :style="{ aspectRatio: ratio }" />
  <LazyImg v-else :src="resolved" :alt="alt" :ratio="ratio" />
</template>

<style scoped>
.sign-skeleton {
  width: 100%;
  border-radius: inherit;
}
</style>
