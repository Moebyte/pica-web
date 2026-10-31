# PicaComic WebUI (v2)

PicaComic B/S 新一代前端，替代 Flutter Web，挂载在同域 `/v2/` 路径下。
纯 Vite + Vue 3（`<script setup>` + TypeScript strict）+ vue-router + Pinia，无组件库、无
Tailwind，全部手写 CSS；暗色默认、可切换亮色（localStorage 记住）。

## 目录结构

```
webui/
├── index.html                 # 入口（含防 FOUC 的主题恢复脚本）
├── vite.config.ts             # base '/v2/'，dev 端口 5180，/api 与 /healthz 代理到 127.0.0.1:18081
├── mock/
│   ├── plugin.ts              # dev/preview 中间件：网关语义 API 未就绪时 mock /api/src/*
│   └── data.ts                # 确定性 mock 数据（jm/picacg 漫画池、标签、章节、SVG 封面/页图）
├── src/
│   ├── api/                   # 纯 fetch 封装（credentials include，错误归一化为 ApiError）
│   │   ├── http.ts            #   通用请求 + ApiError
│   │   ├── sources.ts         #   /api/src/*（sources/tabs/explore/search/comic/pages/login/logout）
│   │   ├── store.ts           #   /api/store/history|favorites（GET 值为 JSON 字符串、PUT 原文回写）
│   │   ├── downloads.ts       #   /api/downloads/*
│   │   ├── health.ts          #   /healthz
│   │   └── imgsign.ts         #   /api/img/sign 封面签名（带缓存 + 并发 4 的小队列，防 favorites 爆闸）
│   ├── stores/
│   │   ├── app.ts             #   源列表/当前源、主题、阅读模式偏好（localStorage）
│   │   └── history.ts         #   阅读历史：节流写回（每 5 页 / 换章 / 离开 / pagehide keepalive）
│   ├── composables/
│   │   ├── usePagedList.ts    #   分页列表状态机（首页/追加/错误/结束）
│   │   ├── useInfiniteSentinel.ts  # IntersectionObserver 无限滚动哨兵
│   │   └── useScrollRestore.ts     # keep-alive 页面滚动位置恢复（监听式，避开 router 滚动复位竞态）
│   ├── components/            # ComicCard/ComicGrid/LazyImg/SignedImg/ErrorRetry/EmptyState/ConfirmDialog/nav…
│   ├── pages/                 # Explore/Search/ComicDetail/Reader/Login/Favorites/History/Downloads/Settings
│   ├── router/                # createWebHistory('/v2/')，9 个路由；reader 为 fullscreen（隐藏 Shell）
│   ├── styles/main.css        # 主题变量（dark/light）+ 基础组件样式
│   └── utils/format.ts        # 日期/相对时间/万级数字格式化
├── e2e/e2e.py                 # Playwright E2E（45 项断言，见下）
└── e2e-shots/                 # E2E 截图（暗/亮/移动端/阅读器等）
```

## 开发

```bash
export PATH=/www/server/nodejs/v24.15.0/bin:$PATH
npm install
npm run dev        # http://localhost:5180/v2/  （/api、/healthz 代理到 127.0.0.1:18081）
```

Mock 行为：dev server 启动后每 60s 探测一次真实网关的 `GET /api/src/sources`；
一旦网关语义 API 上线（返回 sources 数组），mock 自动停用、全部转发真实网关，
store/downloads/img 一类请求从不拦截。可用 `MOCK_UPSTREAM` 覆盖探测地址。

## 构建 / 部署

```bash
npm run build      # vue-tsc -b（strict 零错误）+ vite build，产物在 dist/，base=/v2/
```

`dist/` 即静态产物，部署到网关同域的 `/v2/` 下即可（同源 fetch，无需额外配置）。

## E2E

```bash
python3 e2e/e2e.py    # 需要 dev server 已在 :5180 运行
```

覆盖（45 项断言全部通过，2026-10-31）：
- 探索首屏 20 卡、无限滚动加载第 2 页、tab 切换、keep-alive 滚动恢复
- 搜索（jm sort 下拉、picacg 隐藏 sort）、标签 chip 跳搜索
- 详情（元信息/标签分组/章节列表/预览）、阅读器（图片渲染+解码、进度标签、连续滚动翻页）
- **历史写回真实网关**：每 5 页节流 PUT（读到第 5 页时 store 已出现 page=5）、离开 reader
  PUT（page=6）、pagehide keepalive PUT；picacg 行 type=0、jm 行 type=2、readEpisode=章节
  id、ep 为 1 基序号、time 为毫秒
- 历史页倒序渲染、点击带 ep 继续；收藏/下载页渲染；设置页网关健康（真实 /healthz）
- 登录页错误账号 401 文案、demo/demo 成功跳回；主题切换 + localStorage 持久化
- 移动端 390x844：底部 tab、侧栏隐藏、网格 ≤2 列、阅读器单页模式触摸滑动翻页

## 数据契约要点

- 错误统一 `{ok:false,error}`；同源 fetch `credentials: 'include'`
- `/api/store/*`：GET 外层 `{ok,key,value}` 且 value 为 JSON 字符串；PUT body 为该 JSON 原文
- history 行 `type`：**picacg=0，jm=2**；`time` 毫秒、`ep` 1 基、`readEpisode` 为章节 id
- favorites 的 `cover_path` 为源站 URL，展示前经 `/api/img/sign?url=…&ttl=86400` 包装
  （src/api/imgsign.ts 带缓存与并发上限）

## 已知限制（v1）

- 收藏页只读（按 v1 范围）；收藏封面依赖源站可达，不可达时显示占位图
- 语义 API（/api/src/*）后端上线前由 mock 插件兜底（见上，自动切换）
- 阅读器单页模式键盘（←→/空格）与触摸滑动已实现；连续模式空格走浏览器原生滚动
