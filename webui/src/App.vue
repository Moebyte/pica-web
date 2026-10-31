<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useAppStore } from '@/stores/app'
import { NAV_ITEMS } from '@/components/nav'

const app = useAppStore()
const { sources, currentSrc, sourcesLoaded, sourcesError, currentSource } = storeToRefs(app)
const route = useRoute()
const router = useRouter()

onMounted(() => {
  app.applyTheme()
  void app.loadSources()
})

const isDark = computed(() => app.theme === 'dark')

function switchSource(key: string): void {
  if (key === currentSrc.value) return
  app.setSource(key)
  // list pages are source-scoped; drop back to explore/search roots
  if (route.path === '/' || route.path.startsWith('/search')) {
    if (route.path.startsWith('/search')) router.replace({ path: '/search', query: { ...route.query, q: undefined } })
  }
}

const needLogin = computed(() => currentSource.value?.needLogin === true && !currentSource.value.loggedIn)
</script>

<template>
  <div class="shell" :class="{ fullscreen: route.meta.fullscreen }">
    <aside class="sidebar">
      <RouterLink to="/" class="brand">
        <span class="logo">P</span>
        <span class="brand-name">PicaComic</span>
      </RouterLink>
      <nav class="side-nav">
        <RouterLink
          v-for="item in NAV_ITEMS"
          :key="item.to"
          :to="item.to"
          class="side-link"
          :class="{ active: route.path === item.to }"
        >
          <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true" v-html="item.icon" />
          <span>{{ item.title }}</span>
        </RouterLink>
      </nav>
      <div class="side-footer">
        <button class="icon-btn" :title="isDark ? '切换到亮色' : '切换到暗色'" @click="app.toggleTheme()">
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round">
            <path v-if="isDark" d="M20 13.5A8 8 0 0 1 10.5 4 8 8 0 1 0 20 13.5z" />
            <template v-else>
              <circle cx="12" cy="12" r="4" />
              <path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6l1.4 1.4M17 17l1.4 1.4M18.4 5.6L17 7M7 17l-1.4 1.4" />
            </template>
          </svg>
        </button>
      </div>
    </aside>

    <div class="main">
      <header v-if="!route.meta.fullscreen" class="topbar">
        <div class="source-switch" role="tablist" aria-label="漫画源">
          <button
            v-for="s in sources"
            :key="s.key"
            class="source-pill"
            :class="{ active: s.key === currentSrc }"
            role="tab"
            :aria-selected="s.key === currentSrc"
            @click="switchSource(s.key)"
          >
            <span class="dot" :class="{ on: !s.needLogin || s.loggedIn }" />
            {{ s.name }}
          </button>
          <span v-if="!sourcesLoaded && !sourcesError" class="src-loading">…</span>
        </div>
        <div class="topbar-right">
          <span v-if="sourcesError" class="src-error" :title="sourcesError">源状态获取失败</span>
          <RouterLink v-if="needLogin" to="/login/picacg" class="btn small login-btn">
            登录 {{ currentSource?.name }}
          </RouterLink>
        </div>
      </header>

      <main class="content">
        <RouterView v-slot="{ Component }">
          <KeepAlive include="ExplorePage,SearchPage,FavoritesPage,HistoryPage,DownloadsPage">
            <component :is="Component" />
          </KeepAlive>
        </RouterView>
      </main>
    </div>

    <nav v-if="!route.meta.fullscreen" class="bottom-tabs">
      <RouterLink
        v-for="item in NAV_ITEMS"
        :key="item.to"
        :to="item.to"
        class="tab"
        :class="{ active: route.path === item.to }"
      >
        <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true" v-html="item.icon" />
        <span>{{ item.title }}</span>
      </RouterLink>
    </nav>
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  min-height: 100vh;
}

/* reader route takes over the whole viewport */
.shell.fullscreen .sidebar {
  display: none;
}

.sidebar {
  display: none;
}

.content {
  flex: 1;
  min-width: 0;
}

/* ---- top bar ---- */

.topbar {
  position: sticky;
  top: 0;
  z-index: 50;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  height: 54px;
  padding: 0 14px;
  background: var(--topbar-bg);
  backdrop-filter: blur(12px);
  border-bottom: 1px solid var(--border);
}

.source-switch {
  display: flex;
  align-items: center;
  gap: 8px;
}

.source-pill {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 6px 14px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--card);
  color: var(--text-dim);
  font-size: 13.5px;
  cursor: pointer;
  transition: all 0.15s;
}

.source-pill.active {
  background: var(--accent-soft);
  border-color: var(--accent);
  color: var(--accent-text);
  font-weight: 600;
}

.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-faint);
}

.dot.on {
  background: var(--ok);
}

.src-loading {
  color: var(--text-faint);
  font-size: 13px;
}

.topbar-right {
  display: flex;
  align-items: center;
  gap: 10px;
}

.src-error {
  color: var(--danger);
  font-size: 12.5px;
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.login-btn {
  background: var(--accent);
  border-color: transparent;
  color: #fff;
}

/* ---- bottom tabs (mobile) ---- */

.bottom-tabs {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 60;
  display: flex;
  background: var(--topbar-bg);
  backdrop-filter: blur(12px);
  border-top: 1px solid var(--border);
  padding-bottom: env(safe-area-inset-bottom);
}

.tab {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  padding: 7px 0 5px;
  color: var(--text-faint);
  font-size: 10.5px;
}

.tab.active {
  color: var(--accent-text);
}

/* ---- desktop ---- */

@media (min-width: 900px) {
  .sidebar {
    display: flex;
    flex-direction: column;
    width: 190px;
    position: sticky;
    top: 0;
    height: 100vh;
    border-right: 1px solid var(--border);
    background: var(--bg-soft);
    padding: 14px 10px;
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 10px 16px;
    color: var(--text);
    font-weight: 700;
    font-size: 16px;
  }

  .logo {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    height: 30px;
    border-radius: 9px;
    background: var(--accent);
    color: #fff;
    font-size: 17px;
  }

  .side-nav {
    display: flex;
    flex-direction: column;
    gap: 3px;
  }

  .side-link {
    display: flex;
    align-items: center;
    gap: 11px;
    padding: 9px 12px;
    border-radius: 10px;
    color: var(--text-dim);
    font-size: 14.5px;
  }

  .side-link:hover {
    background: var(--card-hover);
    color: var(--text);
  }

  .side-link.active {
    background: var(--accent-soft);
    color: var(--accent-text);
    font-weight: 600;
  }

  .side-footer {
    margin-top: auto;
    padding: 8px 10px;
    display: flex;
    justify-content: flex-end;
  }

  .bottom-tabs {
    display: none;
  }

  .icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    border-radius: 9px;
    border: 1px solid var(--border);
    background: var(--card);
    color: var(--text-dim);
    cursor: pointer;
  }

  .icon-btn:hover {
    background: var(--card-hover);
    color: var(--text);
  }
}

@media (max-width: 899.9px) {
  .icon-btn {
    display: none;
  }
}
</style>
