# PicaComic B/S 改造架构（路线 1 + 3）

> 目标：把 Flutter 客户端 PicaComic 变成浏览器可用的 B/S 应用。
> 路线：**阶段 1 = Flutter Web UI + Go 服务端隧道**（快速可用）；**阶段 2 = Vue 3 前端 + Go 语义 API**（长期形态）。两阶段共用同一套 Go 网关（认证 / Cookie / 图片 / 出站代理 / 限流）。
> 首版范围（已确认）：**在线阅读 + 收藏/历史**。下载到服务器、自定义 JS 源、WebDAV 同步后置。

---

## 1. 现状与硬约束（2026-10-31 实测）

| 事实 | 影响 |
|---|---|
| `flutter build web` 失败：`sqlite3` / `flutter_qjs` / `zip_flutter` 依赖 `dart:ffi` | 这三个依赖必须条件导入或替换 |
| `dart:io` 在 Web 能编译，但 `File/Directory/Cookie/Platform` 调用运行时抛 `UnsupportedError` | 不能只靠编译通过，必须做运行期平台分支 |
| 浏览器禁止设置 `Cookie`/`UA`/`Referer`/`Host`，且读不到 `Set-Cookie` | 所有 HTTP 必须经服务端隧道；Cookie 由服务端保管 |
| 漫画站普遍有 CORS / 防盗链 / Cloudflare | 服务端接管传输 + 图片代理 + CF 求解 |
| 内置 6 源不用 JS；自定义源依赖 QuickJS（无 Web 实现） | 首版砍自定义源；阶段 2 在服务端跑 |
| 存储层约 5000 行（cookie/cache/history/favorites/download）依赖 sqlite3 + 文件 | 服务端可直接复用；Web 端首版只保留 history/favorites 的轻量实现 |

## 2. 目标拓扑

```
浏览器
  ├─ 阶段 1：Flutter Web（原 UI，Dart→JS）
  └─ 阶段 2：Vue 3（PicaComicNow 参考）
          │  HTTPS（同源，无 CORS）
          ▼
  Nginx / Caddy（TLS、Basic Auth 可选）
          ▼
  pica-gateway（Go，单二进制）
  ├─ 静态站点：Flutter Web / Vue 构建产物
  ├─ 会话认证：HMAC cookie / Basic Auth
  ├─ /api/tunnel：透明 HTTP 隧道（流式、二进制、Range、重定向）
  ├─ /api/img/{token}：签名图片代理 + 磁盘缓存
  ├─ Cookie Vault：按 (tenant, host) 加密存 Cookie/UA
  ├─ 出站：直连 / SOCKS5（复用现有节点）/ 固定 IP
  ├─ SSRF 防护、域名白名单、限流
  ├─ Cloudflare：FlareSolverr 或 curl-impersonate（可选）
  └─ 阶段 2 语义 API：/api/sources/...（Go 源适配器）
```

### 2.1 阶段 1 数据流（Flutter Web）

```
Dio（现有业务代码不动）
  → TunnelAdapter（Web 专用 HttpClientAdapter）
  → POST /api/tunnel  {X-Target-URL, X-Target-Method, X-Pica-Follow}
  → gateway：鉴权 → 白名单/SSRF → 注入 Cookie/UA → 出站
  → 上游响应：status/headers/body 流式回传（X-Pica-Meta 带元数据）
  → 浏览器：Dio 拿到原始响应，现有解析代码照常工作
图片：ImageManager → Dio → 同一隧道；封面可走 /api/img/{token}
```

### 2.2 阶段 2 数据流（Vue）

```
Vue → /api/sources/{key}/search|comic|pages|...
     → Go 源适配器（逐个从 Dart 移植）
     → 复用 Cookie Vault / 出站 / 图片缓存
```

## 3. 组件与责任

### 3.1 pica-gateway（Go）

- 单二进制，`embed` 静态站点；systemd 托管。
- 监听 `127.0.0.1:18081`，公网只经 nginx。
- 配置：`config.json`（监听、认证、白名单、出站代理、缓存目录、CF solver）。
- 日志：结构化 JSON；敏感头/Cookie 脱敏。

### 3.2 Flutter Web（阶段 1）

保留：搜索、详情、排行/探索、在线阅读器、网络收藏、本地收藏、历史、基础设置。
砍掉/降级：下载管理、PDF/zip 导出导入、WebDAV、生物识别、通知、后台任务、截图拦截、窗口管理、自定义 JS 源、桌面 WebView。
兼容层：
- `lib/web_compat/`：条件导入平台实现。
- `TunnelAdapter`：Web 端替换 Dio IO adapter。
- `CookieJar`：Web 端 no-op，服务端注入。
- `Storage`：history/favorites 用 `shared_preferences`/IndexedDB；cache 用内存。
- 原生插件：条件导入 + no-op stub。

### 3.3 Vue 3（阶段 2）

- 参考 `FreeNowOrg/PicaComicNow`（Apache-2.0，仅 picacg）的 Nuxt/Nitro 结构，或纯 Vite + Vue。
- 首个源：picacg（可参考现成实现），随后 nhentai → htcomic → hitomi → jm → ehentai。
- 与 Flutter Web 共用 gateway 的认证/Cookie/图片/出站。

## 4. 安全基线

- 同源部署；API 必须登录（会话 cookie 或 nginx basic auth）。
- 隧道仅允许 http/https；禁止 userinfo/IP 字面量/localhost/私网/metadata；DNS 解析后逐 IP 校验并绑定拨号（防 rebinding）；每次重定向复验。
- 域名白名单按源维护；自定义源需显式批准。
- 请求体 ≤ 8MB，响应 ≤ 200MB，总超时 120s；per-user 限流。
- 上游 `Set-Cookie` 只进 vault，绝不下发到浏览器。
- Cookie/UA 加密存储；日志脱敏。

## 5. 阶段与验收

### Phase 0（已完成）
- 克隆、依赖、`flutter build web` 实测、阻塞清单、方案评审。

### Phase 1 — MVP（目标 3-6 周）
1. Go gateway：`/api/tunnel`、Cookie Vault、白名单/SSRF、静态站点、健康检查。
2. Flutter Web 兼容层：conditional imports、插件 stub、TunnelAdapter、内存缓存。
3. 历史/收藏：Web 端本地持久化。
4. 验收：
   - `flutter build web --release` 通过。
   - 浏览器打开首页，能搜索/打开详情/阅读内置源（至少 nhentai、htcomic、hitomi 三源）。
   - 登录态 Cookie 存服务端，刷新页面不掉登录。
   - 收藏/历史刷新后仍在。
   - 网关单测：SSRF、Cookie 解析、隧道流式、认证。

### Phase 2 — 语义 API + Vue（目标 8-16 周）
1. Go 源适配器：picacg → nhentai → htcomic → hitomi → jm → ehentai。
2. Vue 前端：首页/搜索/详情/阅读器/收藏/历史/设置。
3. 可选：服务端下载、WebDAV 同步、自定义 JS 源（QuickJS/Node sidecar）。

## 6. 风险

| 风险 | 缓解 |
|---|---|
| Flutter Web 体积/滚动性能 | 首版可接受；阶段 2 用 Vue 替换 |
| 浏览器 Cookie/CF 行为差异 | 服务端 vault + FlareSolverr；登录态服务端化 |
| sqlite3 WASM 多标签并发 | 首版只在单标签使用；写操作串行化 |
| ehentai/jm 复杂鉴权与图片重组 | 放到最后；阶段 1 不承诺 |
| 出口 IP 被站點封禁 | 复用现有 SOCKS5 池；per-host 限速 |
| 自定义源生态 | 阶段 2 服务端 QuickJS；首版明确不支持 |

## 7. 实施结果与调整（2026-10-31）

- **nhentai**：HTML 页面被 Cloudflare 挑战，但 API v2 可用；Web 分支改为
  `/api/v2/galleries`、`/api/v2/search`、`/api/v2/galleries/<id>`，图片走
  `i.nhentai.net`/`t.nhentai.net`，不再依赖 HTML/CF。
- **hitomi**：索引/图片 CDN 已迁到 `*.gold-usergeneratedcontent.net`，白名单已补；
  Web 端 `.nozomi` 索引改为进程内内存缓存。
- **隧道协议**：新增 `X-Pica-Req-Headers`（客户端把 Cookie/UA/Referer 等编码上行）；
  `follow=0` 时上游 3xx 以“传输层 200 + Meta 真实 3xx”回传，避免 fetch 自动跟随。
- **认证**：网关新增内置 `/login` 密码页；密码模式访问 `/` 会 302 到登录页。
- **验证**：hitomi/nhentai 的探索、详情、阅读、翻页、收藏、历史持久化均通过
  Playwright E2E；详见 [`STATUS.md`](STATUS.md)。

## 8. 目录约定

```
~/pica-web/
├── app/          # PicaComic fork（Web 兼容改造）
├── gateway/      # Go 网关
├── web/          # Flutter Web 构建产物（部署时生成）
├── docs/         # 本文档 / 计划 / 运维
└── deploy/       # systemd / nginx / 脚本
```
