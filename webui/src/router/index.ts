import { createRouter, createWebHistory } from 'vue-router'

const ExplorePage = () => import('@/pages/ExplorePage.vue')
const SearchPage = () => import('@/pages/SearchPage.vue')
const ComicDetailPage = () => import('@/pages/ComicDetailPage.vue')
const ReaderPage = () => import('@/pages/ReaderPage.vue')
const FavoritesPage = () => import('@/pages/FavoritesPage.vue')
const HistoryPage = () => import('@/pages/HistoryPage.vue')
const DownloadsPage = () => import('@/pages/DownloadsPage.vue')
const SettingsPage = () => import('@/pages/SettingsPage.vue')
const LoginPage = () => import('@/pages/LoginPage.vue')

export const router = createRouter({
  history: createWebHistory('/v2/'),
  routes: [
    { path: '/', name: 'explore', component: ExplorePage, meta: { title: '探索' } },
    { path: '/search', name: 'search', component: SearchPage, meta: { title: '搜索' } },
    {
      path: '/comic/:src/:id',
      name: 'comic',
      component: ComicDetailPage,
      meta: { title: '漫画详情' },
    },
    {
      path: '/read/:src/:id',
      name: 'read',
      component: ReaderPage,
      meta: { title: '阅读', fullscreen: true },
    },
    { path: '/favorites', name: 'favorites', component: FavoritesPage, meta: { title: '收藏' } },
    { path: '/history', name: 'history', component: HistoryPage, meta: { title: '历史' } },
    { path: '/downloads', name: 'downloads', component: DownloadsPage, meta: { title: '下载' } },
    { path: '/settings', name: 'settings', component: SettingsPage, meta: { title: '设置' } },
    { path: '/login/:src', name: 'login', component: LoginPage, meta: { title: '登录' } },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior(to, from, savedPosition) {
    if (savedPosition) return savedPosition
    if (to.path !== from.path) return { top: 0 }
    return {}
  },
})

router.afterEach((to) => {
  const title = to.meta.title as string | undefined
  document.title = title ? `${title} · PicaComic` : 'PicaComic'
})
