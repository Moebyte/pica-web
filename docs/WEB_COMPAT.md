# PicaComic Web 编译兼容改造

日期：2026-10-31
范围：`~/pica-web/app`（未改动 `~/pica-web/gateway`）
Flutter：3.47.9 / Dart 3.13.7（`flutter`）

## 验收结果

- `flutter build web --release`：**exit code 0**，产物在 `app/build/web/`。
- `flutter run -d web-server --web-port 18083`：dev server 正常启动（`lib/main.dart is being served at ...`），
  headless Chrome 能加载并执行应用。
- 运行时验证（release 产物 + headless Chrome / Playwright）：
  - Welcome 页正常渲染（非白屏）；
  - Playwright 走完完整 onboarding（欢迎 → 勾选声明 → 主题 → 列表 → 阅读 → 源选择 → 完成），
    成功进入主界面，`localStorage` 中 `firstUse[3]=1`，无 page error；
  - 主界面显示 History(0)、Accounts、Favorite image、Downloaded、Tools；
  - 探索页正常渲染内置源 Tab（Picacg/Eh/JM/hitomi/HtComic/nhentai）和分类标签；
  - 浏览器 IndexedDB 中出现 `pica_comic_web`，说明 sqlite3 wasm + IndexedDB VFS 生效；
  - 隧道适配器用本地 mock gateway 验证了请求头编码、body、X-Pica-Meta 解析、Set-Cookie 回填、
    二进制响应、网关错误、follow=0 状态、同源绕过（全部通过）；
  - 用真实 gateway（本机 18084，dev loopback）跑通了：
    - nhentai 列表 + 关键词搜索 + 封面；
    - HtComic 列表 + 关键词搜索 + 封面 + 详情页；
    所有请求均经 `/api/tunnel` 转发；HtComic 图片 CDN 需要在网关 allowHosts 中加入 `qy0.ru`。
  - 上游 Cloudflare 拦截的页面（nhentai 详情、HtComic 阅读页）在 UI 中显示错误/验证提示，不白屏、无 page error。
  - 未能完成的 E2E：nhentai/HtComic 阅读页（上游 Cloudflare challenge）、hitomi（本环境 DNS 解析失败）。

## 设计原则

1. 只在 Web 分支退化，原生实现全部保留；Web 分支统一走 `lib/web_compat/` 下的条件导入。
2. 优先 no-op stub，不删除业务逻辑，保持 API 形状，让 UI 不崩。
3. 运行期所有真实文件/平台插件调用都用 `kIsWeb` / `App.isWeb` 短路；`dart:io` 在 Web 可编译，
   但 `Platform.*`、`File/Directory` 在 Web 会抛 `UnsupportedError`，因此不能只靠编译通过。

## 新增文件

| 文件 | 作用 |
| --- | --- |
| `lib/web_compat/sqlite.dart` | SQLite 门面，条件导出 io/web 实现 |
| `lib/web_compat/sqlite_io.dart` | 原生实现：包装 `package:sqlite3` FFI，`openDb/select/execute/dispose` |
| `lib/web_compat/sqlite_web.dart` | Web 实现：`sqlite3.wasm` + IndexedDB VFS（失败退内存 VFS，再失败退 no-op DB） |
| `lib/web_compat/jscore.dart` | QuickJS 门面，条件导出 |
| `lib/web_compat/jscore_web.dart` | Web no-op `FlutterQjs` / `JSInvokable` stub |
| `lib/web_compat/zip.dart` | zip_flutter 门面，条件导出 |
| `lib/web_compat/zip_web.dart` | Web no-op `ZipFile`（调用抛 `UnsupportedError`，由上层 try/catch 处理） |
| `lib/web_compat/dio_adapter.dart` | Dio 适配器门面，条件导出 |
| `lib/web_compat/dio_adapter_io.dart` | 原生：`IOHttpClientAdapter` / `Http2Adapter`（保持原逻辑） |
| `lib/web_compat/dio_adapter_web.dart` | Web：`TunnelAdapter`（跨域走 `/api/tunnel`，同源交 `BrowserHttpClientAdapter`） |
| `lib/web_compat/tunnel_protocol.dart` | 纯 Dart 隧道协议函数（base64url、header 归一化、Meta 解析、同源判断） |
| `test/web_compat/tunnel_protocol_test.dart` | 上述纯函数单测（14 个用例） |
| `web/sqlite3.wasm` | sqlite3 2.1.0 官方 release 的 wasm 二进制（671 KB），必须与 pubspec 的 `sqlite3` 版本一致 |

> 升级 `sqlite3` 时需要同步替换 `web/sqlite3.wasm`（从
> `sqlite3.dart` 对应 release 下载）。部署在子路径时需保证 `sqlite3.wasm` 可通过站点根/`base href` 访问。

## 修改文件清单

| 文件 | 做法 |
| --- | --- |
| `lib/foundation/app.dart` | `App.isWeb`；所有 `isAndroid/isIOS/isWindows/isLinux/isMacOS/isDesktop/isMobile` 先判 `kIsWeb` 再访问 `Platform`（Web 上 `Platform.*` 会抛异常）；`App.init()` 在 Web 返回虚拟路径 `/pica/data`、`/pica/cache` |
| `lib/base.dart` | `pathSep` Web 返回 `/`；设置读写 Web 改用 `shared_preferences`（跳过 `File`）；`clearAppdata` 跳过文件删除 |
| `lib/init.dart` | 启动先 `await initWebDatabase()`；Web 跳过日志文件、`_checkOldData`、hosts/代理配置文件、`startClearCache`、`JsEngine().init()`；其余初始化顺序不变 |
| `lib/network/cookie_jar.dart` | `CookieJarSql` 改用 SQLite 门面；`dart:io Cookie` 本身是纯 Dart 实现，Web 可用；Web 下真实 Cookie 头由浏览器/tunnel 接管（见降级） |
| `lib/network/download.dart` | Web 下载目录为虚拟路径，跳过 `newDownload.json`、目录扫描与文件写入；`download.db` 仍使用 wasm DB |
| `lib/foundation/history.dart` | 改用 SQLite 门面；Web 跳过 `history_temp.db`/`history.json` 迁移 |
| `lib/foundation/local_favorites.dart` | 改用 SQLite 门面；Web 跳过旧 JSON/DB 迁移；`clearAll` 改为 drop 所有 folder 表；封面落盘逻辑仍在 try/catch 内（Web 忽略） |
| `lib/foundation/cache_manager.dart` | SQLite 门面；Web 不创建目录、不读写缓存文件；`findCache` 返回 null；`CachingFile` 仅内存缓冲，`writeCache/checkCache` no-op |
| `lib/foundation/image_manager.dart` | 改用 `web_compat/jscore.dart`（Web stub） |
| `lib/foundation/js_engine.dart` | 改用 `web_compat/jscore.dart`；Web 不初始化 QuickJS；`runCode()` 在 `_engine == null` 时返回 null（Web no-op，修复 onboarding「完成」崩溃） |
| `lib/comic_source/comic_source.dart` | Web 跳过 `comic_source` 目录扫描（不加载 JS 源）；内置 6 源照常加载；`loadData/saveData` Web 改用 `shared_preferences` |
| `lib/comic_source/parser.dart` | Web 下 `parse/createAndParse` 直接抛 `ComicSourceParseException`（不支持 JS 源） |
| `lib/network/app_dio.dart` | 新增 `createPicaDio()`（原生 IO adapter / Web TunnelAdapter）；`logDio` 保留日志与代理逻辑；9 处直接 `Dio(...)` 改为 `createPicaDio(...)`；Web 跳过 `rule.json` 改 host 与 `Set-Cookie` 校验 |
| `lib/network/cookie_jar.dart` | 逐条 try/catch 解析 `Set-Cookie`（畸形 cookie 跳过并记 warning）；`loadForRequestCookieHeader` 对 `domain == null` 做防御 |
| `lib/network/hitomi_network/search.dart` | Web 用进程内 `Map<String, Uint8List>` 缓存 `.nozomi` 索引，跳过 path_provider/File；原生磁盘缓存不变 |
| `lib/network/hitomi_network/fetch_data.dart` | 原始 `Dio` 改为 `createPicaDio` |
| `lib/network/hitomi_network/image.dart` | 原始 `Dio` 改为 `createPicaDio` |
| `lib/network/jm_network/jm_network.dart` | 3 处原始 `Dio` 改为 `createPicaDio` |
| `lib/network/picacg_network/methods.dart` | 原始 `Dio` 改为 `createPicaDio` |
| `lib/network/file_downloader.dart` | 原始 `Dio` 改为 `createPicaDio`（Web 下载禁用，但不再直连/炸掉） |
| `lib/network/update.dart` | Web `checkUpdate()` 直接返回 null，不请求 api.github.com |
| `lib/tools/io_tools.dart` | zip 门面；`checkDownloadPath` Web 直接返回 |
| `lib/network/eh_network/eh_download_model.dart` | zip 门面 |
| `lib/tools/cache_auto_clear.dart` | Web 直接返回（无文件缓存） |
| `lib/main.dart` | Web 跳过 `runWebViewTitleBarWidget`；`window_manager`/`FlutterDisplayMode` 原本就由 `App.isDesktop/isAndroid` 守卫 |
| `lib/pages/auth_page.dart` | Web 直接视为验证通过（无生物识别），避免 `local_auth` MissingPluginException |
| `lib/pages/settings/settings_page.dart` | Web 跳过 `FlutterDisplayMode` 调用 |
| `lib/tools/block_screenshot.dart` | Web no-op |

## 关键机制说明

### SQLite（Web）

- `web_compat/sqlite.dart` 用
  `export 'sqlite_io.dart' if (dart.library.js_interop) 'sqlite_web.dart';` 分流。
- Web 启动时 `initWebDatabase()`：
  1. `WasmSqlite3.loadFromUrl(Uri.parse('sqlite3.wasm'))`；
  2. `IndexedDbFileSystem.open(dbName: 'pica_comic_web')` 并 `registerVirtualFileSystem(makeDefault: true)`；
  3. IndexedDB 失败 → `InMemoryFileSystem`；
  4. wasm 加载失败 → no-op DB（`select count(...)` 返回 0，其余查询返回空），保证 UI 仍能启动。
- 对外仍保持同步 API：`openDb(path)`、`select(sql, params)`、`execute(sql, params)`、`dispose()`，
  因此 history/favorites/cookies/download/cache 五处 DB 代码只换了 import 与 `sqlite3.open`。
- 数据库路径仍是虚拟路径（如 `/pica/data/history.db`），实际存储由 VFS 抽象。

### QuickJS（Web）

- Web `FlutterQjs.evaluate()` 返回 stub `JSInvokable`，`dispatch/close/port.close` 均为 no-op。
- `JsEngine.init()` 在 Web 不执行；`ComicSource.init()` 不扫描 JS 源目录。
- 自定义 JS 漫画源在 Web 不可用；`ComicSourceParser` 明确抛“不支持”。

### 网关隧道（TunnelAdapter）

`lib/web_compat/dio_adapter_web.dart` 实现 `HttpClientAdapter`：

- 跨域 `http(s)` 请求 → `POST <gatewayBase>/api/tunnel`（`gatewayBase` 默认空 = 同源）：
  - `X-Target-URL` = `options.uri.toString()`；
  - `X-Target-Method` = `options.method`；
  - `X-Pica-Follow` = `options.followRedirects ? '1' : '0'`；
  - `X-Pica-Req-Headers` = base64url(无 padding JSON)，值为 `normalizeHeaderMap(options.headers)`
    （null 跳过、多值逗号连接、CR/LF 清洗）；`Cookie`/`User-Agent`/`Referer`/`Origin` 只放在这里，
    不写进真实 fetch 请求；
  - body 聚合 `requestStream` 为 `Uint8List`（GET/HEAD 为空），`credentials: 'include'`；
  - `cancelFuture` → `AbortController.abort()`。
- 同源请求（`/api/*`、静态资源、`gatewayBase` 自身）和非 http(s) URI 直接交给
  `BrowserHttpClientAdapter`，避免隧道自环。
- 响应：
  - **状态码以 `X-Pica-Meta.status` 为准**（Meta 缺失时才用 fetch 的真实 status）；
  - 响应头以 `X-Pica-Meta.headers` 为准（名称统一小写），`setCookies` 追加为 `set-cookie`，
    使现有 `CookieManagerSql.onResponse` 继续更新本地 jar；
  - body 用 `arrayBuffer()` 全量读取后以单块流返回，非 HEAD 响应把 `content-length` 修正为真实字节数；
  - `X-Pica-Error` 存在时抛 `DioException`（保留 status/body/headers）；401 给出明确的
    "Gateway authentication required" 提示；
  - 网络/abort 异常统一转 `DioException`（取消为 `DioExceptionType.cancel`）。
- 纯协议逻辑在 `tunnel_protocol.dart`，可 VM 单测；`test/web_compat/tunnel_protocol_test.dart`
  覆盖 header 归一化、base64url、Meta 解析、set-cookie 合并、同源判断、401 文案。
- 所有原始 `Dio(...)` 已替换为 `createPicaDio(...)`（`app_dio.dart`），因此 hitomi/jm/picacg/
  文件下载等旁路请求在 Web 上也自动走隧道；`logDio`/`AppHttpAdapter` 路径不变。
- 网关 `allowHosts` 需要包含目标站和图片 CDN；实测 HtComic 缩略图在 `qy0.ru`，nhentai 在
  `t*.nhentai.net`。`X-Pica-Req-Headers` 的网关侧应用仍待网关更新（客户端已按契约发送）。

### 文件系统

- `App.dataPath`/`App.cachePath` 在 Web 为虚拟路径；`pathSep` 为 `/`。
- 启动路径中所有真实文件操作均已 Web 短路：日志文件、旧数据迁移、缓存清理、下载目录检查、
  设置文件、下载队列 JSON、本地收藏迁移、缓存文件。
- `compute`/`Isolate.run` 在 Flutter Web 上会退化为主线程执行，相关路径本身也已被 Web 守卫。

### 平台插件

| 插件 | Web 行为 |
| --- | --- |
| `window_manager` | 仅 `App.isDesktop` 分支调用；Web 不执行 |
| `desktop_webview_window` | `runWebViewTitleBarWidget` Web 跳过；窗口 WebView 仅桌面可用 |
| `workmanager` | 仅 `App.isMobile` 分支调用 |
| `local_auth` | Web `AuthPage` 直接放行（视为验证成功） |
| `flutter_local_notifications` | `init/send/cancel` 均有移动端守卫，Web no-op |
| `flutter_image_gallery_saver` | 仅在 `isAndroid/isIOS` 分支保存；Web no-op |
| `flutter_file_dialog` | 仅在 `isMobile` 分支；Web 走 `file_selector` 的浏览器下载路径（文件导入/导出整体未适配） |
| `flutter_displaymode` | 仅 Android 守卫；设置页 Web 跳过 |
| `flutter_inappwebview` | 使用其自带 Web 实现 |
| `sqlite3_flutter_libs` | 无 Web 平台实现，但未直接 import；Web 不加载 |

## 已保留功能

- 6 个内置漫画源（Picacg/ehentai/JMComic/HTComic/nhentai）的入口、探索页、分类页、搜索/详情/阅读器 UI 与业务逻辑。
- Web 上的跨域请求全部经网关隧道；实测 nhentai 列表/封面、HtComic 列表/封面/详情页可正常加载。
- History、本地收藏、Cookie、下载记录的 SQL 读写接口（Web 落到 wasm SQLite / IndexedDB）。
- 设置、主题、多语言、标签翻译、内置源账号数据持久化（`shared_preferences`）。
- 图片解码与阅读缓存逻辑（Web 改为内存缓冲，最终字节随 `DownloadProgress.data` 下发）。
- Android/iOS/Linux/Windows/macOS 原有实现不受影响（条件导入默认走原生实现）。

## Web 降级功能

- **下载 / Zip 导出 / 数据导入导出**：无真实文件系统，禁用；zip stub 抛 `UnsupportedError`，调用方 catch 后返回失败。
- **自定义 JS 漫画源 / JS 图片后处理**：QuickJS 不可用，禁用。
- **磁盘图片缓存**：禁用，仅单次请求内存缓存；刷新后重新下载。
- **本地收藏封面落盘**：禁用，封面加载失败时显示占位/错误图标。
- **文件日志**：禁用（日志仅在内存的日志页可见）。
- **Cookie 实际注入/读取**：`CookieJarSql` 能存取 DB；Web 把 `Cookie` 放进 `X-Pica-Req-Headers`，由网关
  在服务端应用（网关当前仍会剥离/使用 vault；等 `X-Pica-Req-Headers` 落地后即可透传）。浏览器自身不再设置
  `Cookie` 请求头。
- **代理 / hosts / SNI 改写 / HTTP2**：客户端不再处理，统一由网关出站策略负责；Web 侧只走隧道。
- **通知、生物识别、后台任务、屏幕截图阻止、刷新率、窗口管理、相册保存**：no-op。
- **WebDAV 同步**：`webdav_client` 内部仍走 `dart:io` 适配器，Web 下未适配，调用会失败/被 catch，属于已知降级。

## 剩余运行时阻塞 / 风险

1. **Cloudflare / 反爬**：nhentai 详情页、HtComic 阅读页在上游触发 Cloudflare challenge，
   应用会显示 “Cloudflare verification required”/错误页而不是内容。需要网关侧 Cloudflare 方案
   （或将 cf_clearance 存入 vault），不是适配器问题。
2. **网关 `X-Pica-Req-Headers` 尚未落地**：客户端已按契约发送；当前网关仍剥离 `Origin`/`Referer`
   并使用配置 UA。落地后，登录态、防盗链 Referer、自定义 UA 才能完整透传。
3. **目标域名白名单**：网关 `allowHosts` 必须包含漫画站与图片 CDN；实测 HtComic 缩略图在
   `qy0.ru`，nhentai 在 `t*.nhentai.net`，hitomi 在 `*.hitomi.la` / `*.gold-usergeneratedcontent.net`。
4. **hitomi DNS**：当前执行环境解析 `ltn.hitomi.la` 失败（网关返回 `dns_error`），无法做 hitomi 真实
   E2E；客户端内存索引缓存已修复，待网络可达后即可用。
5. **部分深层文件路径**仍有 `File/Directory` 调用（如本地收藏封面、下载页封面、数据导入导出、WebDAV）。
   这些入口在 Web 尚未禁用，触发时会抛异常（多数被 try/catch 或 FutureBuilder 捕获，不至于白屏）。
6. `sqlite3.wasm` 为实验性 API：依赖 wasm 资产可访问；IndexedDB 写入是异步刷盘，异常关闭页面可能丢失最近数据。
7. `flutter run` 首帧前仍可能出现 WebGL context lost 的浏览器环境 warning（headless/软件渲染下），不影响应用逻辑。
8. 打包产物包含 `sqlite3.wasm`，部署时需一并上线；若用子路径部署，需确认 `base href` 与 wasm 相对路径。

## 下一步

1. **网关应用 `X-Pica-Req-Headers`**：按 `tunnel_protocol.dart` 的 JSON map 恢复 `Cookie` / `Referer` /
   `User-Agent` / `Origin`（以及其它端到端头），同时保留 vault 作为缺省 Cookie 来源。
2. **Cloudflare**：网关侧接入 FlareSolverr/浏览器求解或 cf_clearance vault；否则 nhentai/HtComic 的
   详情/阅读在部分网络下不可用。
3. **图片代理**：可选改走 `/api/img/sign`，让浏览器直接加载同源签名 URL（需要把 ImageManager 的图片
   URL 换成签名后的 URL）；当前隧道方案已可用，但会占用 tunnel 带宽。
4. **WebDAV**：替换 `webdav_client` 的 `dart:io` 适配器，或让 tunnel 暴露 WebDAV 代理端点。
5. **登录态持久化**：把 cookies/history/favorites 的权威数据迁到网关 vault，IndexedDB 仅作本地缓存。
6. **可选**：将 `flutter build web --wasm` 纳入验证；目前 wasm dry-run 警告来自 `win32`/`sqlite3` 的
   `package:js` 兼容层，不影响 dart2js 构建。

## 构建命令

```bash
cd ~/pica-web/app
/opt/flutter pub get
/opt/flutter test test/web_compat/tunnel_protocol_test.dart
/opt/flutter build web --release

# 部署（与网关同源）
# rsync -a build/web/ /path/to/gateway/web/
# ./pica-gateway -config config.json
```

`pubspec.yaml` 新增直接依赖 `web: ^1.0.0`（`package:web` + `dart:js_interop` 实现 fetch）。
