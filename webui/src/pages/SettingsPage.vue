<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { useAppStore, type ReaderMode } from '@/stores/app'
import { getHealth, type GatewayHealth } from '@/api/health'
import { errMessage } from '@/api/http'
import { formatUptime } from '@/utils/format'

defineOptions({ name: 'SettingsPage' })

const appVersion = __APP_VERSION__

const app = useAppStore()
const { theme, readerMode, sources } = storeToRefs(app)

const health = ref<GatewayHealth | null>(null)
const healthLoading = ref(false)
const healthError = ref('')

async function refreshHealth(): Promise<void> {
  healthLoading.value = true
  healthError.value = ''
  try {
    health.value = await getHealth()
  } catch (e) {
    healthError.value = errMessage(e)
    health.value = null
  } finally {
    healthLoading.value = false
  }
}

function setMode(m: ReaderMode): void {
  app.setReaderMode(m)
}

onMounted(() => {
  void app.loadSources()
  void refreshHealth()
})
</script>

<template>
  <div class="page settings">
    <section class="card">
      <h2>外观</h2>
      <div class="row">
        <span>主题</span>
        <div class="seg">
          <button
            class="seg-btn"
            :class="{ active: theme === 'dark' }"
            data-testid="theme-dark"
            @click="app.setTheme('dark')"
          >
            暗色
          </button>
          <button
            class="seg-btn"
            :class="{ active: theme === 'light' }"
            data-testid="theme-light"
            @click="app.setTheme('light')"
          >
            亮色
          </button>
        </div>
      </div>
      <div class="row">
        <span>默认阅读模式</span>
        <div class="seg">
          <button
            class="seg-btn"
            :class="{ active: readerMode === 'vertical' }"
            @click="setMode('vertical')"
          >
            连续滚动
          </button>
          <button
            class="seg-btn"
            :class="{ active: readerMode === 'paged' }"
            @click="setMode('paged')"
          >
            单页
          </button>
        </div>
      </div>
    </section>

    <section class="card">
      <h2>数据源</h2>
      <div v-for="s in sources" :key="s.key" class="row">
        <span>{{ s.name }}</span>
        <span class="dim">
          {{ s.needLogin ? (s.loggedIn ? '已登录' : '未登录') : '无需登录' }}
          <RouterLink v-if="s.needLogin && !s.loggedIn" :to="`/login/${s.key}`" class="mini-link">
            去登录
          </RouterLink>
        </span>
      </div>
      <div v-if="sources.length === 0" class="dim">加载中…</div>
    </section>

    <section class="card">
      <h2>
        网关状态
        <button class="btn small" :disabled="healthLoading" @click="refreshHealth">
          {{ healthLoading ? '刷新中…' : '刷新' }}
        </button>
      </h2>
      <template v-if="health">
        <div class="row">
          <span>状态</span>
          <span class="ok-badge">在线</span>
        </div>
        <div class="row">
          <span>运行时间</span>
          <span class="dim">{{ formatUptime(health.uptime) }}</span>
        </div>
        <div class="row">
          <span>版本</span>
          <span class="dim">{{ health.version || '—' }}</span>
        </div>
        <div v-if="health.downloads" class="row">
          <span>下载队列</span>
          <span class="dim">
            {{ health.downloads.running }} 运行 / {{ health.downloads.queued }} 排队
          </span>
        </div>
      </template>
      <div v-else-if="healthError" class="row err">{{ healthError }}</div>
      <div v-else class="row dim">检测中…</div>
    </section>

    <section class="card">
      <h2>关于</h2>
      <div class="row">
        <span>WebUI 版本</span>
        <span class="dim">{{ appVersion }}</span>
      </div>
      <div class="row">
        <span>技术栈</span>
        <span class="dim">Vue 3 + Vite + Pinia + Vue Router</span>
      </div>
    </section>
  </div>
</template>

<style scoped>
.settings {
  max-width: 720px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.card {
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 18px 20px;
}

h2 {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 15px;
  margin: 0 0 14px;
}

.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 0;
  font-size: 14.5px;
}

.row.err {
  color: var(--danger);
  justify-content: flex-start;
}

.dim {
  color: var(--text-dim);
  font-size: 13.5px;
}

.seg {
  display: inline-flex;
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
}

.seg-btn {
  padding: 6px 16px;
  border: none;
  background: transparent;
  color: var(--text-dim);
  font-size: 13.5px;
  cursor: pointer;
}

.seg-btn.active {
  background: var(--accent);
  color: #fff;
  font-weight: 600;
}

.ok-badge {
  color: var(--ok);
  font-weight: 600;
}

.mini-link {
  color: var(--accent-text);
  margin-left: 8px;
}
</style>
