# 当前状态（2026-10-31）

## 一句话

PicaComic 已经可以在浏览器里跑起来：**Flutter Web UI + Go 网关隧道**，
hitomi 与 nhentai 的探索、详情、阅读、翻页均通过真实端到端验证；
收藏、阅读历史、刷新后持久化也已验证。首版（在线阅读 + 收藏/历史）达成。

---

## 1. 已验证

### 1.1 构建与单测

| 项目 | 结果 |
|---|---|
| `flutter build web --release --no-web-resources-cdn` | 通过（`flutter`，3.47.9；CanvasKit 本地托管，不依赖 gstatic） |
| Flutter 单测 | 21 个全部通过（tunnel 协议 14 + nhentai API 映射 7） |
| Go 网关 `go vet` / `go test -race ./...` | 全部通过 |
| 静态产物 | `app/build/web` → `~/pica-web/web`，网关直接托管 |

### 1.2 真实浏览器 E2E（Playwright + headless Chrome）

环境：`http://127.0.0.1:18081/`，网关 dev 模式，Flutter Web CanvasKit。

**hitomi**
- 探索页加载分类/最新列表，galleryblock 与缩略图经隧道返回 200；
- 点开漫画 → 详情页（画师/分类/3D 标签/缩略图）正常；
- 点「从头开始」→ 阅读器 `1/270`，第一页真实渲染；
- `ArrowRight` 翻到 2/270、3/270，图片按页加载；
- 收藏 → 新建收藏夹 `WebFav` → 收藏成功，按钮变为「已收藏」；
- 阅读后回「我」，历史记录 `(1)`；**刷新页面后历史仍为 (1)**（IndexedDB 持久化）。

**nhentai（API v2，绕开 HTML 的 Cloudflare 挑战）**
- 探索页 Popular/Latest 列表正常，缩略图 `t.nhentai.net` 加载；
- 详情页 28P、分类/语言/标签/画师分组正常；
- 「从头开始」→ 阅读器 `1/28`，`ArrowRight` 翻到 `2/28`，图片 `i.nhentai.net` 加载；
- 关键词搜索 `yuri`、点击标签进入分类页均返回结果；
- 底部滚动分页请求 `galleries?page=2..10` 正常。

**其他**
- onboarding 全流程（欢迎 → 声明勾选 → 主题/列表/阅读设置 → 源选择 → 完成）可进入主界面；
- 网关密码模式：`/` 302 → `/login`，表单登录后回跳，静态资源与 `/api/*` 行为正确；
  密码登录 → Flutter Web 主界面 → hitomi 探索（39 个隧道请求 200/206）完整跑通；
- 网关隧道：GET/POST、二进制图片、Range、Cookie vault、`X-Pica-Req-Headers`、
  follow=0 的 302 → 传输 200 + Meta 302、SSRF 拦截均有单测/curl 验证。

### 1.3 截图

> 截图未包含在公开仓库中（含源站内容）。

### 1.4 已验证的网关能力

- 透明隧道（`POST /api/tunnel`）流式回传、二进制、Range、重定向；
- 浏览器受限头上行：`X-Pica-Req-Headers`（Cookie/UA/Referer/Origin/自定义头）；
- Cookie Vault（SQLite + AES-GCM，RFC6265 域/路径匹配，客户端 Cookie 优先、缺失时注入）；
- 域名白名单 + SSRF（私网/回环/metadata/DNS rebinding/重定向复验）；
- 出站 SOCKS5/HTTP 代理配置；
- 密码登录页 `/login`、HMAC 会话 cookie、dev 模式 loopback 限制；
- 图片签名代理 `/api/img/{token}` + 磁盘缓存（本期未作为主链路）。

---

## 2. 当前可用源

| 源 | 状态 | 说明 |
|---|---|---|
| hitomi | **完全可用** | 探索/详情/阅读/翻页已验证；索引 CDN 为 `*.gold-usergeneratedcontent.net` |
| nhentai | **完全可用** | Web 分支走 API v2；搜索/分类/详情/阅读已验证 |
| 绅士漫画（htcomic） | 部分可用 | 列表/搜索/详情据 agent 验证可用；阅读页可能触发 Cloudflare，需 FlareSolverr |
| picacg | 未验证 | 需要账号登录；API host 已加白名单 |
| e-hentai / exhentai | 未验证 | Cloudflare + SNI 嗅探；当前网关直连大概率失败 |
| jmcomic | **可用**（2026-10-31 全链路验证） | 修了三层：① 白名单补齐 JM 轮换域名（live 服务器列表 `cdnhjk.net / cdnutc.cc / cdnutc.me / cdngwc.net / cdngwc.club` + 内置默认 `cdntwice.org / cdnsha.org / cdnaspa.cc / cdnntr.cc`，共 52 条）；② App 内置默认域名表已死（/promote 全 404），`JmNetwork` 增加**启动/登录时自动从官方服务器列表刷新域名**（仅当仍是出厂默认且开了自动选线路）；③ JM 图片分块重排依赖 `Isolate`+写盘，Web 不可用，已加主线程纯 Dart 重组分支。验证：探索列表/详情（JM1485385）/阅读器 1/59 图片正常渲染、假账号登录到达上游（4 线路探测 401 + 真实登录 401 密码错误），网关零拦截。**JM 仍会换域名**：再遇 403/404 先看 `journalctl -u pica-gateway | grep rejected` |

---

## 3. 已知限制（首版）

- **单用户、单实例**；tenant 字段已预留，但无多用户 UI/隔离。
- **自定义 JS 漫画源不可用**：QuickJS 无 Web 实现；Web 端 `JsEngine` 为 no-op。
- **下载/导出/WebDAV**：Web 端禁用或降级；文件系统相关入口多数已短路。
- **通知/生物识别/后台任务/截图拦截/窗口管理**：Web no-op。
- **Cloudflare 交互式挑战**：尚未接 FlareSolverr/Playwright；nhentai 通过 API v2 规避，
  ehentai/htcomic 等 HTML 站点仍可能被挑战。
- **隧道响应在浏览器内整包缓冲**（`arrayBuffer`），大文件/超长漫画会占内存；
  后续可改流式 `ReadableStream`。
- **Cookie 双写**：为兼容现有登录态，`metaSetCookies=true` 让客户端 jar 也保存 Cookie；
  阶段 2 可改为仅服务端 vault，减少 XSS 面。
- **图片缓存**：浏览器端仅内存缓存（刷新丢失）；服务端 `/api/img` 缓存未作为主链路。
- **源站条款/账号风险**：高频抓取可能触发站点风控；请遵守各站条款。

---

## 4. 部署

> **已上线（2026-10-31）**：`https://picacg.example.com/` 已在 你的服务器可用。
> 域名走 Cloudflare DNS-only A 记录，LE 证书自动续期，网关以 `pica-gateway.service`
> 常驻（`/opt/pica`，数据 `/var/lib/pica`，LAN 内免密）。浏览器 E2E 已在该域名下复测通过
> （nhentai 阅读器 1/28，37 个隧道请求全 200）。
>
> 日常操作：`systemctl status|restart pica-gateway`；改配置后 `systemctl restart pica-gateway`；
> 更新前端 `rsync -a --delete ~/pica-web/web/ /opt/pica/web/`；要加访问密码改
> `/opt/pica/config.json` 的 `password` 后重启（浏览器访问 `/` 会跳内置登录页）。

### 4.1 构建

```bash
cd ~/pica-web
./deploy/build.sh          # Flutter Web + Go 网关 + 同步 web/
```

### 4.2 运行（开发 / 本机验证）

```bash
./pica-gateway -config config.dev.json
# 本机浏览器打开 http://127.0.0.1:18081/
# 远程机器用 SSH 隧道：ssh -L 18081:127.0.0.1:18081 <server>
```

dev 模式（`password` 为空）只允许 loopback 访问 `/api/*`，不要直接暴露公网。

### 4.3 生产（nginx + systemd + 密码）

1. 创建用户与目录：

```bash
useradd --system --home /opt/pica --shell /usr/sbin/nologin pica
install -d -o pica -g pica /opt/pica /var/lib/pica
```

2. 部署文件：

```bash
cp pica-gateway /opt/pica/
rsync -a web/ /opt/pica/web/
cp deploy/config.prod.example.json /opt/pica/config.json
# 修改：password（必改）；webDir/dataDir/cacheDir 已按 /opt/pica、/var/lib/pica 填好；
#       如不走 HTTPS 反代，把 cookieSecure/trustProxy 改回 false；按需增删 allowHosts
chown -R pica:pica /opt/pica /var/lib/pica
```

3. systemd：

```bash
cp deploy/pica-gateway.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now pica-gateway
curl -s http://127.0.0.1:18081/healthz
```

4. nginx：参考 `deploy/nginx-pica.conf`，配好证书后访问 `https://<域名>/`；
   未登录会跳 `/login`（网关自带密码页）。若改用 nginx Basic Auth，可把
   `config.json` 的 `password` 留空（此时务必保证网关只监听 127.0.0.1）。

### 4.4 配置要点

- `allowHosts` 必须覆盖源站与图片/CDN 域名；当前模板已含 nhentai / hitomi /
  ehentai / picacg / 18comic / wnacg / JM CDN / hitomi gold CDN / hath.network。
- `metaSetCookies=true` 是首版兼容模式；`userAgent=""` 让各源使用自己的 UA。
- `trustProxy=true` + `cookieSecure=true` 仅在使用 HTTPS 反代时开启。
- 出站代理：`outboundProxy` 支持 `socks5://user:pass@host:port`，可复用现有节点。

---

## 5. 下一步

1. **Cloudflare**：接 FlareSolverr 或 cf_clearance vault，恢复 ehentai/htcomic 等 HTML 源。
2. **服务端下载**：浏览器端解析页面列表后调服务端下载 API，落到磁盘/115。
3. **自定义 JS 源**：服务端 QuickJS/Node sidecar，复用 `assets/init.js` API。
4. **阶段 2 Vue 3 前端 + Go 语义 API**：参考 PicaComicNow，逐个移植源，替换 Flutter Web。
5. **安全/体验**：Cookie 服务端化、流式隧道、图片服务端缓存、多用户。

---

## 6. 外部依赖清单（2026-10-31 复核，已做断网实测）

用 Playwright 拦截全部非本域请求实测（等价断外网）：

| 环节 | 依赖 | 状态 |
|---|---|---|
| App 外壳（HTML/JS/CanvasKit/skwasm/sqlite3.wasm/图标） | 无 | ✅ 全部由网关本地发布（`--no-web-resources-cdn`） |
| 中日韩文字 | ~~fonts.gstatic.com 运行期下载 Noto~~ | ✅ **已修复**：`fonts/NotoSansSC-Regular.ttf`（11MB，3 万字形）打包进应用，`ThemeData.fontFamilyFallback: ["Noto Sans SC"]`；断网实测 0 外联、中文正常（修复前实测 57 次外联尝试且中文变豆腐块） |
| 域名解析 | Cloudflare 公网 DNS（`*.local`→私网 IP，本仓既定模式） | ✅ 按既定模式维持（2026-10-31 本人确认：`local.example.com` 的内网 IP 记录统一放 CF，**不加** AdGuard 本地覆盖；断外网属可接受场景） |
| 证书续期 | Let's Encrypt + CF API（每 60-90 天） | ⚠️ 仅续期时需要外网，不影响日常运行 |
| 漫画内容/隧道出站 | 源站本身 | 业务必需 |

代价：Web 包体 48MB→58MB（+字体 11MB）；原生构建也会带上这份字体。

---

## 7. Console「报错」速查（2026-10-31，JM 登录后排查实录）

浏览器控制台里的三类信息，只有第一类需要动代码（已修）：

| 现象 | 定性 | 处置 |
|---|---|---|
| `Permissions-Policy: Unrecognized feature 'run-ad-auction'...` | 上游策略头被隧道透传到本域，浏览器不认识就告警 | ✅ 已修：网关剥离文档级策略头（Permissions-Policy / CSP / Report-To / NEL / HSTS / X-Frame-Options / COOP/COEP/CORP / X-XSS-Protection / **Clear-Site-Data**）。顺带堵掉真实隐患：上游若发 `Clear-Site-Data` 会清空本站 localStorage/IndexedDB（设置/历史/收藏） |
| `GET https://c.1password.com/richicons/... 404` | 用户浏览器的 1Password 扩展给自己密码库条目拉图标 | 与本应用无关，忽略 |
| `POST /api/tunnel 401 (Unauthorized)` ×4 | **设计内信号**：JM 登录前的线路探测（selectDomain 并发 POST 4 条线路 `/login`，预期回 401=线路活着）；Chrome 会把一切 4xx 打进 console | 无需处理。网关已加 `tunnel upstream status` 日志（≥400 带目标地址，5xx 为 WARN），今后任何 4xx/5xx 都能 `journalctl -u pica-gateway | grep 'upstream status'` 对上具体端点 |

---

## 8. 账号态服务端同步（2026-10-31 上线）

**目的**：账号信息存服务端，任何设备/浏览器打开 `picacg.example.com` 即处于已登录状态。

### 机制

```
登录/登出/源数据变化（任意设备）
  → scheduleAccountSync()（2s 去抖）
  → PUT /api/store/account   （网关 SQLite，AES-GCM 落库）
        blob = { implicitData, 账号相关设置, 各源 data(含 account/token), CookieJar 全量导出 }

新设备打开页面
  → init: GET /api/store/account（404=首台设备，静默）
  → 套用到内存 + localStorage + CookieJar
  → ComicSource.init / 各源 reLogin 接手 → UI 显示已登录
```

- 网关侧：`GET/PUT/DELETE /api/store/{key}`（key 限 `^[a-z0-9][a-z0-9_-]{0,31}$`，单值 ≤512KB，
  与 Cookie Vault 同库同密钥加密；测试含"明文不入库"断言）。
- 客户端：`lib/network/gateway_sync.dart`；钩子复用 `Webdav.uploadData` seam
  （所有登录流程都经 `appdata.writeData()` 走到这里）+ `ComicSource.saveData` +
  `writeImplicitData`；仅 Web 生效，原生行为不变。
- 推送从 SharedPreferences 直接读源数据，不依赖 `ComicSource.sources` 是否初始化，
  避免启动早期把服务端账号误清空；对端 `account:null`（登出）本地跟随。

### 已验证（E2E）

1. curl 向服务端写入含假 JM 账号 + 假 Cookie 的 blob → **全新浏览器上下文**打开站点：
   `GET /api/store/account` 200，localStorage 出现 `comic_source_data_jm/nhentai`，
   settings[85]（JM 域名表）同步，账号卡片显示"已登录"，JM reLogin 自动触发
   （假凭据 401 属预期）。
2. 反向：设备 B 完成恢复后自动回推，服务端 blob `updated` 刷新、Cookie 导出回环完整。
3. 测试数据已 `DELETE /api/store/account` 清除；首台真实设备登录后即建立正式账号态。

### 安全说明

blob 内含各源凭据明文（JM 用户名/密码——App 本身就这样存 reLogin 凭据；picacg token）。
传输走同源 HTTPS，落库 AES-GCM（密钥派生自主密钥，`data/.secret`）。信任边界与
Cookie Vault 相同：拿到网关会话即可读——LAN 免密模式下等同于"在内网即可信"。
如需收紧：给 `/opt/pica/config.json` 设 `password` 后重启。

---

## 9. 服务端下载 + 收藏/历史同步（2026-10-31 晚，上线）

### 9.1 服务端下载

浏览器解析页面 URL，网关下载落盘 `/var/lib/pica/downloads/<source>/<comicId>/<ep>/`。

- 端点：`POST/GET/DELETE /api/downloads/jobs`、`GET /api/downloads/file?path=`（防穿越、Range）；
  jobs 列表带 `files`（真实文件名，免扩展名探测）；`/healthz` 含队列状态。
- 队列：2 并发 job、每页 3 次退避重试、断点续传（job.json 持久化，重启恢复，done 不重跑）。
- 出站复用网关链路：白名单 + SSRF + Cookie Vault（登录态与阅读一致）。
- **JM 图片分块重排已移植到 Go**（`internal/jmimage`，公式 4 分支 + jpeg 竖切逆序重排，
  合成图单测像素级验证）；nhentai/hitomi 无需重排。
- 客户端：详情页"下载"在 Web 改为提交服务端任务（含章节选择）；"已下载"卡片打开
  服务端下载页（轮询进度、浏览已下载图片、删除任务/文件）。原生下载链路不变。
- E2E：真实 nhentai 任务 5/5 done，文件字节与直连一致；浏览器 UI 提交→列表→浏览全通。

### 9.2 收藏/历史/图片收藏同步

GatewaySync 扩展为多键：`account` / `history` / `favorites`（均为网关加密存储，
限额 7MiB/键）。

- 导出/导入在两张表行级进行：`history`+`image_favorites`、收藏夹各表+`folder_order`
  （manager 新增 `exportRows/importRows/isEmptyForSync`）。
- 一致性：每键带 `updated` 版本，本地记 `gsync.rev.<key>`。服务端较新 → **整库替换**
  （删除可跨设备传播）；首次双端都有数据 → 并集合并后回推；变化 2s 去抖推送 + 60s 兜底。
- 触发点全部复用现有 seam：任何收藏/历史写操作本来就调用 `Webdav.uploadData()`，
  Web 分支即推送。
- E2E：设备 A 阅读+建收藏夹收藏 → 服务端出现数据 → 设备 B（全新浏览器）显示
  历史记录(1) 且收藏夹 SyncFolder 连同条目完整恢复。

### 9.3 现在的完整形态

任意设备打开 `https://picacg.example.com/`：账号已登录、收藏/历史/图片收藏同步、
可搜索阅读（hitomi/nhentai/JM）、可把漫画下载到服务器并浏览。数据全在服务端
（`/var/lib/pica`：vault.db 加密存储 + downloads/ 图片），浏览器只是缓存。
