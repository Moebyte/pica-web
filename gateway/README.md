# pica-gateway（阶段 1）

PicaComic B/S 改造的 Go 网关：单二进制，给浏览器里的 Flutter Web 客户端透明转发漫画站 API 与图片，同时负责 Cookie 保管、SSRF 防护、出站代理、登录认证和静态站点托管。

- 模块：`picagateway`（Go 1.25）
- 唯一外部依赖：`modernc.org/sqlite`（纯 Go，无 cgo）
- 本目录只包含网关；Flutter Web 兼容层在 `../app`，构建产物放在 `../web`

## 目录

```
gateway/
├── cmd/pica-gateway/main.go      # 启动、配置、优雅退出
├── internal/config/              # config.json + 环境变量
├── internal/secret/              # 主密钥加载/派生（.secret 0600）
├── internal/outbound/            # SSRF 策略、受控拨号、SOCKS5/HTTP 代理、Transport
├── internal/vault/               # SQLite Cookie Vault（AES-GCM、RFC6265）
├── internal/auth/                # HMAC 会话 cookie、密码常量时间比较
├── internal/ratelimit/           # per-IP 令牌桶
├── internal/imgproxy/            # 图片 token 签名 + 磁盘缓存
├── internal/downloads/           # 服务端下载队列（job.json 持久化、断点续传）
├── internal/jmimage/             # JM 竖向分块还原（image/jpeg SubImage 重排）
├── internal/srcapi/              # 语义源 API（Source 接口 + registry + jm/picacg 实现）
├── internal/server/              # 路由、tunnel、图片、下载、认证、静态文件
├── config.example.json
└── README.md
```

## 构建与测试

```bash
cd ~/pica-web/gateway
gofmt -l .            # 应为空
go vet ./...
go build -o pica-gateway ./cmd/pica-gateway
go test ./...
go test -race ./...
```

依赖下载后即可离线编译。可用 `-version` 查看版本，`-config` 指定配置文件（默认 `config.json`，文件不存在时使用默认值 + 环境变量）。

## 配置

`config.example.json` 可直接复制为 `config.json`。MVP 模板已包含 password `change-me`、`webDir=../web`、`metaSetCookies=true`、`userAgent=""`，以及 nhentai / e-hentai / exhentai / ehgt / hitomi / picacomic / 18comic / wnacg / JM（bytepluses、jmapiproxy1-3.cc）的域名白名单。所有耗时字段单位为秒，所有大小字段单位为 MiB（`cacheTTL` 也是秒）。

| 字段 | 默认值 | 说明 |
|---|---|---|
| `listen` | `127.0.0.1:18081` | 监听地址；公网请经 nginx/caddy 反代 |
| `webDir` | `./web` | Flutter Web 构建产物目录，SPA fallback 到 `index.html` |
| `dataDir` | `./data` | vault.db、`.secret` 所在目录 |
| `secret` | `""` | 主密钥；为空时自动生成 `<dataDir>/.secret`（0600） |
| `password` | `""` | 登录密码；为空进入开发模式（见下） |
| `allowHosts` | `[]` | 出站域名白名单，支持 `example.com`、`*.example.com`；为空且 `allowAll=false` 时拒绝所有 |
| `allowAll` | `false` | 跳过域名白名单，但 SSRF 私网/重定向校验仍然生效 |
| `hostOverrides` | `{}` | 可选：`{"example.com":"203.0.113.10"}`，用固定 IP 拨号、TLS SNI 仍用原域名；默认关闭，覆盖 IP 仍要过 SSRF 校验 |
| `outboundProxy` | `""` | `socks5://user:pass@host:port` 或 `http://user:pass@host:port`；空为直连 |
| `cacheDir` | `<dataDir>/imgcache` | 图片磁盘缓存 |
| `cacheTTL` | `86400` | 图片缓存 TTL 秒；`0` 表示不过期 |
| `cacheMaxMB` | `512` | 图片缓存上限；`0` 关闭缓存（仍可代理） |
| `downloadsDir` | `<dataDir>/downloads` | 服务端下载根目录（目录结构见下） |
| `maxBodyMB` | `8` | `/api/tunnel` 请求体上限 |
| `maxRespMB` | `200` | 上游响应体上限 |
| `timeoutSec` | `120` | 单个上游请求（含流式读取）的总超时 |
| `rateLimit.rps` | `30` | per-IP 令牌桶速率；`<=0` 关闭限流 |
| `rateLimit.burst` | `60` | 令牌桶容量 |
| `cookieSecure` | `false` | 会话 cookie 的 `Secure`；HTTPS 反代时建议 true |
| `trustProxy` | `false` | 信任 `X-Forwarded-For` / `X-Real-IP` / `X-Forwarded-Proto`（仅反代场景） |
| `metaSetCookies` | `false` | 是否把上游原始 `Set-Cookie` 放进 `X-Pica-Meta`；MVP `config.example.json` 中为 `true`，代码默认 `false` |
| `imgTokenTTL` | `3600` | 图片签名 URL 默认有效期（秒），签发接口可传 `ttl`，最长 7 天 |
| `userAgent` | 桌面 Chrome UA | 非空时统一覆盖出站 User-Agent；置空则不覆盖，各源可通过 `X-Pica-Req-Headers` 自带 UA。MVP 示例配置为空 |
| `headerProfiles` | `{}` | 图片代理的可选请求头 profile，如 `{"ehentai":{"Referer":"https://e-hentai.org/"}}` |

环境变量（非空时覆盖配置文件）：

`PICA_LISTEN`、`PICA_WEB_DIR`、`PICA_DATA_DIR`、`PICA_SECRET`、`PICA_PASSWORD`、
`PICA_ALLOW_HOSTS`（逗号分隔）、`PICA_ALLOW_ALL`、`PICA_OUTBOUND_PROXY`、
`PICA_CACHE_DIR`、`PICA_CACHE_TTL`、`PICA_CACHE_MAX_MB`、`PICA_DOWNLOADS_DIR`、
`PICA_MAX_BODY_MB`、`PICA_MAX_RESP_MB`、`PICA_TIMEOUT_SEC`、`PICA_RATE_RPS`、`PICA_RATE_BURST`、
`PICA_COOKIE_SECURE`、`PICA_TRUST_PROXY`、`PICA_META_SET_COOKIES`、
`PICA_IMG_TOKEN_TTL`、`PICA_USER_AGENT`。

### 开发模式

`password` 为空时，所有 `/api/*`（包括 `/api/auth/status`）只允许 loopback（`127.0.0.1` / `::1`）访问，其余返回 403。此模式只用于本机调试。即使 `allowAll=true`，SSRF 私网校验照常执行。

## 运行

```bash
cd ~/pica-web/gateway
cp config.example.json config.json
# 修改 password、allowHosts、webDir
./pica-gateway -config config.json
```

启动日志为 JSON（slog），会提示 dev mode、allowAll 等风险。首次启动自动创建：

- `data/vault.db`（Cookie 库）
- `data/.secret`（0600，主密钥；删除它会导致已存 Cookie/会话/图片 token 全部失效）
- `data/imgcache/`（内容寻址图片缓存）
- `data/downloads/`（服务端下载根目录）

### 静态站点

把 Flutter Web 构建产物放到 `webDir`：

```bash
cd ~/pica-web/app && flutter build web --release
rsync -a build/web/ ~/pica-web/web/
```

任意非 `/api/*` 且不存在的路径回退到 `index.html`；带扩展名的缺失资源返回 404。`/assets/*` 使用一年 immutable 缓存，`index.html` 使用 `no-cache`。

新 Vue 前端（`../webui`，另一仓库）由 `webDir2` 托管在 `/v2/*`：默认取 `<webDir>/../webui/dist`，目录不存在时 `/v2/*` 返回 404（跳过，不影响主站）。SPA 回退与缓存策略与主站一致（`/v2/assets/*` immutable）；可用 `webDir2` / `PICA_WEB_DIR2` 覆盖：

```bash
cd ~/pica-web/webui && npm run build   # 产物 dist/
curl -s http://127.0.0.1:18081/v2/         # Vue index.html
```

## API 与 curl 示例

### 健康检查

```bash
curl -s http://127.0.0.1:18081/healthz
# {"ok":true,"uptime":12,"version":"0.1.0","downloads":{"queued":0,"running":0}}
```

### 登录 / 状态 / 登出

```bash
curl -s -c /tmp/pica.cookies \
  -H 'Content-Type: application/json' \
  -d '{"password":"change-me"}' \
  http://127.0.0.1:18081/api/auth/login

curl -s -b /tmp/pica.cookies http://127.0.0.1:18081/api/auth/status

# 也支持表单
curl -s -c /tmp/pica.cookies -d 'password=change-me' \
  http://127.0.0.1:18081/api/auth/login

curl -s -b /tmp/pica.cookies -c /tmp/pica.cookies -X POST \
  http://127.0.0.1:18081/api/auth/logout
```

会话 cookie：`pica_session`，HttpOnly、SameSite=Lax、路径 `/`、24 小时；HTTPS 下配置 `cookieSecure` 或经 `trustProxy` + `X-Forwarded-Proto` 自动带 Secure。上游 `Set-Cookie` 永远不会以真实响应头下发给浏览器。

密码模式下浏览器直接访问 `/` 会 302 到 `/login?next=...`，页面是内置的密码表单（无外部资源）；
登录成功写 cookie 后跳回原地址。`/api/*` 未登录返回 401；静态资源本身不鉴权，方便登录页加载。
dev 模式（`password` 为空）不会重定向，`/login` 直接跳回 `/`。

### 隧道

```bash
# GET：把目标 URL 和方法放在控制头里，响应状态码/响应头/body 与上游一致
curl -s -D /tmp/pica.hdr -o /tmp/pica.body \
  -b /tmp/pica.cookies \
  -X POST http://127.0.0.1:18081/api/tunnel \
  -H 'X-Target-URL: https://api.example.com/v1/search?q=test' \
  -H 'X-Target-Method: GET' \
  -H 'X-Pica-Follow: 1'
```

- `X-Target-URL` 必填，仅 `http` / `https`。
- `X-Target-Method` 默认 `GET`，允许 GET/HEAD/POST/PUT/PATCH/DELETE/OPTIONS。
- `X-Pica-Follow` 默认 `1`；`0` 表示不跟随重定向。此时若上游返回 3xx，隧道 HTTP 状态强制写 `200`（避免浏览器 fetch 自动跟随），真实状态和 `Location` 保留在 `X-Pica-Meta.status` / `X-Pica-Meta.headers` 中，body 照原样回传。非 3xx 状态不变。
- 请求 body 原样转发（≤ `maxBodyMB`，支持 multipart/binary），`Content-Type` 等端到端头保留。
- 原始隧道请求里的 `Cookie` / `User-Agent` / `Referer` / `Origin` 一律忽略；这些浏览器受限头只能通过 `X-Pica-Req-Headers` 提供（见下）。
- `X-Pica-Req-Headers` = base64url(JSON map，值为 string 或 string[])，用于携带上游 `Cookie`、`User-Agent`、`Referer`、`Origin` 及其它自定义头；非法 base64/JSON/头名/头值返回 400 `invalid_req_headers`。
- 即使出现在 `X-Pica-Req-Headers` 中，`Host`、`Content-Length`、`Accept-Encoding`、hop-by-hop、`X-Pica-*`、`X-Target-*`、`Proxy-*` 仍会丢弃；`Accept-Encoding` 统一交给 Go Transport 自动协商，避免客户端拿到压缩体。
- 若 `X-Pica-Req-Headers` 提供了非空 `Cookie`，优先使用客户端的，vault 不覆盖；否则由 vault 按域名注入。上游 `Set-Cookie` 无论哪种情况都写入 vault。
- 响应 body 流式回传并逐块 Flush；`Range`/`If-Range` 与 206/`Content-Range` 原样保留。
- 上游 4xx/5xx 原样返回；网关自身失败返回 JSON，例如网络错误 502。
- `Connection`、`Keep-Alive`、`Proxy-*`、`TE`、`Trailer`、`Transfer-Encoding`、`Upgrade` 以及 `Connection` 中列出的头会双向剥离。

带 `X-Pica-Req-Headers` 的示例：

```bash
REQ_HEADERS=$(python3 - <<'PY'
import base64, json
h = {
    "User-Agent": "Mozilla/5.0 ...",
    "Referer": "https://site.example/",
    "Cookie": "sid=...; uid=...",
    "Accept": "application/json",
}
print(base64.urlsafe_b64encode(json.dumps(h).encode()).decode().rstrip("="))
PY
)
curl -s -b /tmp/pica.cookies -X POST http://127.0.0.1:18081/api/tunnel \
  -H 'X-Target-URL: https://site.example/api/list' \
  -H 'X-Target-Method: GET' \
  -H "X-Pica-Req-Headers: $REQ_HEADERS"
```

解码元数据：

```bash
python3 - <<'PY'
import base64, json, re
hdr = open('/tmp/pica.hdr', encoding='latin1').read()
value = re.search(r'(?im)^x-pica-meta:\s*(\S+)', hdr).group(1)
raw = base64.urlsafe_b64decode(value + '=' * (-len(value) % 4))
print(json.dumps(json.loads(raw), indent=2, ensure_ascii=False))
PY
```

`X-Pica-Meta` 内容：

```json
{
  "status": 200,
  "headers": {"Content-Type": ["application/json"], "Content-Length": ["1234"]},
  "setCookies": [],
  "finalUrl": "https://api.example.com/v1/search?q=test"
}
```

`headers` 是过滤后的响应头（不含 Set-Cookie / hop-by-hop），键为 Go 规范化形式。代码默认 `setCookies` 为空数组（Cookie 只进服务端 vault）；MVP `config.example.json` 设置 `metaSetCookies=true`，此时 Meta 会带原始 `Set-Cookie` 供客户端展示登录态。若不需要，改回 `false`。

### 图片代理

先签发 token（需要登录）：

```bash
curl -s -b /tmp/pica.cookies \
  'http://127.0.0.1:18081/api/img/sign?url=https%3A%2F%2Fexample.com%2Fcover.png&profile=ehentai&ttl=3600'
# {"ok":true,"token":"...","url":"/api/img/...","expiresAt":"..."}
```

再取图（token 是 `/api/img/{token}` 的路径段）：

```bash
curl -s -b /tmp/pica.cookies -D - -o /tmp/cover.png \
  'http://127.0.0.1:18081/api/img/<token>'

# Range 命中磁盘缓存时由 ServeContent 处理，未命中时 Range 透传上游
curl -s -b /tmp/pica.cookies -H 'Range: bytes=0-1023' \
  -o /tmp/part.bin 'http://127.0.0.1:18081/api/img/<token>'
```

- token = `base64url(payload) + "." + base64url(HMAC-SHA256)`，payload 含 `url`、可选 `profile`、`exp`。
- 仅允许 GET；签名无效/过期返回 403 JSON。
- 只缓存 `image/*`（且无 `Content-Encoding`、非 Range 请求）；磁盘按 `sha256(url)` 内容寻址。
- 缓存 TTL + LRU 淘汰由 `cacheTTL` / `cacheMaxMB` 控制；命中返回 `X-Cache: HIT`，未命中 `MISS`，Range/非图片 `BYPASS`。
- SVG 或非图片响应附加 `Content-Security-Policy: sandbox`，避免同源直开执行脚本。

### 服务端下载（Downloads API）

浏览器端解析好每页图片 URL 后，把整个任务提交给网关下载落盘；网关复用同一出站链路（allowHosts/SSRF/Cookie Vault/出站代理），保证下载与阅读的登录态一致。全部端点需要登录（与 tunnel 相同）。

#### 提交任务

```bash
curl -s -b /tmp/pica.cookies -H 'Content-Type: application/json' \
  -d '{
    "id": "jm-123456-ep1",
    "title": "示例漫画",
    "source": "jm",
    "comicId": "123456",
    "ep": "1",
    "epName": "第1話",
    "pages": [
      {"url": "https://cdn.example/00001.jpg",
       "headers": {"Referer": "https://18comic.vip/", "User-Agent": "Mozilla/5.0 ..."},
       "jm": {"epsId": "1", "scrambleId": "220880", "bookId": "00001"}}
    ],
    "createdAt": 1690000000
  }' \
  http://127.0.0.1:18081/api/downloads/jobs
# {"ok":true,"job":{"id":"jm-123456-ep1",...,"total":1,"done":0,"failed":0,"status":"queued","dir":"jm/123456/1"}}
```

- 请求体上限 4 MiB（pages 可达几千条）。`id` 由客户端生成（建议 `源-comicId-ep`）。
- **幂等**：重复提交同 id 时，若任务 `queued/running/done` 直接 200 返回现状，不重复下载；`error/partial` 状态的任务重新提交会从头重下（重试语义）。
- `pages[i].headers` 为该页级请求头：`Referer`/`User-Agent` 等原样发送；`Cookie` 若提供则优先使用，否则由 vault 注入；hop-by-hop、`Host`、`Content-Length`、`Accept-Encoding`、`X-Pica-*`/`X-Target-*`/`Proxy-*` 始终丢弃。
- `pages[i].jm` 存在时启用 JM 竖向分块还原（见下）。
- 同一 `source/comicId/ep` 目录已被其它 id 占用时返回 409 `dir_conflict`。
- 执行：单实例 2 个并发 job；每页最多重试 3 次（退避 2s/8s/30s）；单页超时 60s、大小上限 30MB；页失败记录后继续下一页，最后标 `partial`（全部失败标 `error`）。进度实时写内存，并在每页完成时落盘 `job.json`。

#### 任务列表 / 删除

```bash
# 列表按 createdAt 倒序
curl -s -b /tmp/pica.cookies http://127.0.0.1:18081/api/downloads/jobs
# {"ok":true,"jobs":[{"id":"jm-123456-ep1","title":"...","source":"jm","comicId":"123456",
#   "ep":"1","epName":"第1話","total":24,"done":24,"failed":0,"status":"done","error":"","dir":"jm/123456/1"}]}

# 取消进行中任务；files=true 连同已下载文件一起删除（默认 false 保留文件）
curl -s -b /tmp/pica.cookies -X DELETE \
  'http://127.0.0.1:18081/api/downloads/jobs?id=jm-123456-ep1&files=true'
# {"ok":true}   （id 不存在返回 404）
```

`status`：`queued` | `running` | `done` | `error` | `partial`。

#### 取回文件

```bash
curl -s -b /tmp/pica.cookies -o page.jpg \
  'http://127.0.0.1:18081/api/downloads/file?path=jm/123456/1/001.jpg'

# 支持 Range（如前端做虚拟滚动/预览）
curl -s -b /tmp/pica.cookies -H 'Range: bytes=0-1023' \
  'http://127.0.0.1:18081/api/downloads/file?path=jm/123456/1/001.jpg'
```

- `path` 必须是 downloads 根目录内的相对路径；含 `..`、绝对路径、指向符号链接或目录一律拒绝（400/403/404）。
- Content-Type 按扩展名（`.jpg` → `image/jpeg`），未知扩展名回退 `application/octet-stream`（不嗅探）。

#### 目录结构与断点恢复

```
<downloadsDir>/
└── <source>/<comicId>/<ep>/
    ├── 001.jpg      # 页序号 3 位 + 扩展名（URL/Content-Type 推断，默认 jpg）
    ├── 002.jpg
    └── job.json     # 任务元数据 + 每页状态（每页完成时原子写）
```

- 启动时扫描所有 `job.json` 重建队列：`done/error/partial` 只恢复列表不重跑；`queued/running`（中断）重新入队，已 `ok` 的页跳过。
- `/healthz` 与 `GET /api/downloads/healthz` 返回 `downloads.queued` / `downloads.running`。

#### JM 图片还原

页带 `jm` 字段时按 JM 规则计算分块数 `num`：

- `epsId < scrambleId` → 0（不乱序）；`epsId < 268850` → 10；
- `epsId > 421926` → `md5(epsId+bookId)` 末字符 `%8*2+2`；否则 `%10*2+2`（`bookId` = 图片文件名去扩展名，如 `00001`）。

`num>1` 时把 JPEG 纵向均分 `num` 块（余数归最后一块）逆序重排后以质量 85 重新编码；`num<=1` 原样保存；webp/png 等非 JPEG 不做重排，原样保存并记 warning（`_getSegmentationNum`/`_segmentationPicture` 从 Dart `image_recombine.dart` 移植）。

### 语义源 API（Semantic source API）

`internal/srcapi` 把支持的漫画源聚合成一套语义化端点，服务端完成源站协议（JM 的 token/AES 响应解密、picacg 的 HMAC 签名）与分页/字段归一，前端只消费统一契约。全部 `requireAuth`；`{src}` ∈ `jm | picacg`。

| 端点 | 说明 |
| --- | --- |
| `GET /api/src/sources` | 源列表：`{key,name,needLogin,loggedIn}` |
| `GET /api/src/{src}/tabs` | 探索页 tab（jm：home/latest；picacg：recommend/latest） |
| `GET /api/src/{src}/explore?tab=&page=N` | tab 列表页 → `{comics:[ComicBrief],page,maxPage}` |
| `GET /api/src/{src}/search?q=&page=N` | 搜索（jm 另支持 `sort=mr/mv/mp/mfm/tp`，默认 mr） |
| `GET /api/src/{src}/comic/{id}` | 详情 → `{comic:Comic}`（含 chapters/tags 分组） |
| `GET /api/src/{src}/comic/{id}/pages?ep=<chapterId>` | 章节图片 → `{pages:["/api/img/<token>",...]}` |
| `POST /api/src/{src}/login` | body `{"username","password"}`；jm 固定 400 `not_supported` |
| `POST /api/src/{src}/logout` | picacg 清除凭据；jm no-op |

`ComicBrief` = `{id,title,subtitle,author,cover,pageCount,chapters,tags}`；`Comic` 在此之上增加 `favorites`、`uploadDate`（unix 秒）、`tags`（源原生分组：jm 为 作者/作品/登场人物/标签，picacg 为 分类/标签）、`chapters:[{id,title,order}]`、`thumbs`（当前两源均为空数组）。封面/正文图一律返回**服务端签名好的 `/api/img/<token>` 相对 URL**，`<img>` 直接可用，命中 imgproxy 磁盘缓存。

**JM（禁漫天堂）认证说明**：jm v1 API 免登录可浏览，因此 `login` 端点返回 400。请求头（`token=md5(time+"18comicAPPContent")`、`tokenparam="time,2.0.11"` 等）与响应 AES-256-ECB 解密从 Dart `jm_network` 原样移植。API 线路从加密服务器列表（`*.bytepluses.com`）获取：取线路 → 内存缓存 → 全线失败时重取，多线路任选可用者（出厂域名表已整体失效）。图片 CDN 从 `/setting` 的 `img_host` 刷新（每小时一次，默认 `cdn-msp.jmapiproxy3.cc`）。`pages` 端点签发的 token 带 `jm:{epsId,scrambleId,bookId}`，`/api/img/<token>` 缓存 MISS 时对 JPEG 做 `jmimage.Recombine` 分块还原后再缓存返回；webp（JM 目前大部分图源已是 webp）原样返回并记 warning。

**PicACG 认证说明**：必须登录。`login` 用 `{username,password}` 调 `auth/sign-in`（`username` 即哔咔邮箱），成功后把 `{username,password,token,name}` 存入加密 KV（`vault.PutStore(tenant,"src_picacg_cred",...)`），响应 `{"ok":true,"name":"<昵称>"}`；凭据错误返回 401 `{"ok":false,"error":"invalid email or password"}`。token 失效（上游 401）时用存的用户名密码自动重登一次，仍失败则清除 token：`sources` 里 `loggedIn:false`，浏览端点返回 401 `login_required`，前端引导重新登录。

```bash
# 源列表
curl -s -b /tmp/pica.cookies http://127.0.0.1:18081/api/src/sources
# {"ok":true,"sources":[{"key":"jm","name":"禁漫天堂","needLogin":false,"loggedIn":false},
#                        {"key":"picacg","name":"PicACG","needLogin":true,"loggedIn":false}]}

# JM 最新（免登录）
curl -s -b /tmp/pica.cookies 'http://127.0.0.1:18081/api/src/jm/explore?tab=latest&page=1'

# JM 详情 → 章节页数（ep = chapters[].id）
curl -s -b /tmp/pica.cookies http://127.0.0.1:18081/api/src/jm/comic/1487029 | jq '.comic.chapters'
curl -s -b /tmp/pica.cookies 'http://127.0.0.1:18081/api/src/jm/comic/1487029/pages?ep=1487029' | jq '.pages | length'

# JM 搜索（可选排序 mr/mv/mp/mfm/tp）
curl -s -b /tmp/pica.cookies 'http://127.0.0.1:18081/api/src/jm/search?q=%E6%9D%B1%E6%96%B9&sort=mr&page=1'

# PicACG 登录 / 登出 / 推荐
curl -s -b /tmp/pica.cookies -H 'Content-Type: application/json' \
  -d '{"username":"you@example.com","password":"…"}' http://127.0.0.1:18081/api/src/picacg/login
curl -s -b /tmp/pica.cookies 'http://127.0.0.1:18081/api/src/picacg/explore?tab=recommend'
curl -s -b /tmp/pica.cookies -X POST http://127.0.0.1:18081/api/src/picacg/logout
```

出站全部走 `policy.ValidateURL` + 共享受控 Transport（域名校验照旧，需在 `allowHosts` 放行源站域名，见 `config.example.json`）；上游非 2xx 记 `src upstream status` 日志（4xx Info / 5xx Warn，带 target）。JM 图片 token 使用内置 `jm` header profile（`Referer: https://localhost/` 等，启动时自动注册，可在 config 覆盖）。

新增源只需实现 `srcapi.Source` 接口并在 `NewRegistry` 注册，HTTP 层不用动。

### Cookie 查看 / 清除

```bash
curl -s -b /tmp/pica.cookies \
  'http://127.0.0.1:18081/api/cookies?host=example.com'
# {"ok":true,"host":"example.com","cookies":[{"host":".example.com","name":"sid","path":"/","expires":...,"secure":true,"httpOnly":true,"masked":"ab…yz"}]}

curl -s -b /tmp/pica.cookies -X DELETE \
  'http://127.0.0.1:18081/api/cookies?host=example.com'
```

只返回脱敏值（首尾各 2 字符，短值全掩码），不返回明文。

## Cookie Vault

- SQLite 表：

```sql
cookies(tenant, host, name, path, value, expires, secure, http_only, updated_at,
        PRIMARY KEY (tenant, host, name, path))
```

- `tenant` 固定 `default`（单用户），所有 API 都为多租户预留参数。
- `Set-Cookie` 支持 `Max-Age` / `Expires` / `Path` / `Domain`；`Domain` 必须与请求 host 域匹配，否则丢弃。
- 域 cookie 以 `.example.com` 形式存储（匹配 apex 和子域）；无 Domain 的 host-only cookie 只匹配精确 host。
- 请求前按 RFC6265 的 domain/path/secure/expiry 规则筛选并注入 `Cookie`，路径更长者优先；仅当请求没有客户端提供的 `Cookie` 时才注入（客户端 Cookie 优先）。
- `value` 用 AES-GCM 加密，密钥由主密钥 HMAC 派生；过期行定期清理。
- 会话 cookie（无 Max-Age/Expires）在服务端长期保留，便于刷新页面后维持上游登录态。

## 安全模型

- 仅 http/https；拒绝 userinfo、IP 字面量、localhost、`.local` / `.internal`、metadata 域名。
- DNS 解析后逐 IP 拒绝：`0/8, 10/8, 100.64/10, 127/8, 169.254/16, 172.16/12, 192.168/16, 198.18/15, 224/4, 240/4`；IPv6 `::1`、`fc00::/7`、`fe80::/10`、`ff00::/8`、IPv4-mapped。
- 拨号使用已校验的 IP（不是重新解析的域名），防 DNS rebinding；TLS 证书仍按原域名 + SNI 校验。
- 每次重定向重新做 URL/白名单/DNS 校验；最多 10 跳（图片 5 跳）。
- 出站代理：SOCKS5（含用户名密码）与 HTTP CONNECT。代理连接通常以已校验 IP 为目标；HTTP 代理需允许对目标端口 CONNECT。
- 日志只记录方法、路径（图片 token 已脱敏）、状态、耗时、IP；不记录 Cookie / Authorization / 请求头。
- 请求体 ≤ `maxBodyMB`；响应 ≤ `maxRespMB`（Content-Length 已知时直接 502，未知时截断并记日志）；总超时 `timeoutSec`。
- 服务端下载与 `file` 接口：下载页与 tunnel 走同一 `ValidateURL` + 受控拨号 + 重定向复验；`/api/downloads/file` 只放行 downloads 根目录内的常规文件（拒绝 `..`/绝对路径/符号链接/目录，`Lstat` + 前缀双重校验）；`source/comicId/ep` 作为路径段强校验（禁分隔符与 `..`）。

## Flutter Web 对接（TunnelAdapter 契约）

阶段 1 的 Dio 适配器只需要替换传输层，业务代码不变。

请求：

```text
POST /api/tunnel
X-Target-URL: https://site.example/api/path?x=1
X-Target-Method: GET|POST|...
X-Pica-Follow: 1|0
X-Pica-Req-Headers: base64url(JSON map<string, string|string[]>)
<原始 body、Content-Type、业务头>
```

`X-Pica-Req-Headers` 说明：

- 浏览器不能设置 `Cookie` / `User-Agent` / `Referer` / `Origin` / `Host`；适配器把这些上游头编码进该 JSON map。值可以是 string 或 string[]。
- 原始隧道请求中的 `Cookie` / `User-Agent` / `Referer` / `Origin` 会被忽略；即使出现在 map 里，`Host`、`Content-Length`、`Accept-Encoding`、hop-by-hop、`X-Pica-*`、`X-Target-*`、`Proxy-*` 也会被丢弃。
- `Accept-Encoding` 不转发，统一由网关 Transport 自动协商（通常是 gzip 并自动解压），客户端拿到的始终是未压缩 body。
- 若 map 中 `Cookie` 非空，上游使用客户端提供的 Cookie，vault 不再注入；否则由 vault 注入。上游 `Set-Cookie` 始终写入 vault。
- `userAgent` 配置非空时仍会覆盖 map 中的 UA；MVP 示例配置为空，让各源使用自己的 UA。
- 非法 base64 / JSON / 头名 / 头值返回 400 `invalid_req_headers`（带 `X-Pica-Error` 头）。

响应：

- HTTP status = 上游 status（4xx/5xx 也不改写）。例外：`X-Pica-Follow: 0` 且上游为 3xx 时隧道状态写 200，真实 3xx 在 `X-Pica-Meta.status`，避免浏览器自动跟随。
- 响应头 = 上游端到端头（保留 `Content-Type`、`Content-Length`、`Content-Range`、`Location`、`ETag` 等），去掉 hop-by-hop 和 `Set-Cookie`。
- body = 上游原始字节流；适配器照常交给 Dio 解析。
- `X-Pica-Meta` = 无 padding 的 base64url JSON，字段 `status` / `headers` / `setCookies` / `finalUrl`。

Dart 解码示例：

```dart
import 'dart:convert';

Map<String, dynamic>? decodePicaMeta(String? value) {
  if (value == null || value.isEmpty) return null;
  final normalized = base64Url.normalize(value);
  final raw = utf8.decode(base64Url.decode(normalized));
  return jsonDecode(raw) as Map<String, dynamic>;
}
```

适配器要点：

- 不读取、不存储 `Set-Cookie`；服务端 vault 已处理。登录态通过 `GET /api/auth/status` 判断，用 `POST /api/auth/login` 登录（cookie HttpOnly）。
- `X-Pica-Follow` 与 Dio 的 `followRedirects` 对齐；不跟随时从 `X-Pica-Meta.status` 和 `X-Pica-Meta.headers.Location` 读取真实 3xx，隧道 HTTP 状态固定为 200（防止浏览器自动跟随），拿到后再次经隧道请求（网关会复验 SSRF）。
- 浏览器的 `Cookie` / `User-Agent` / `Referer` / `Origin` 不要直接放在 fetch 头里；统一放进 `X-Pica-Req-Headers`，否则会被忽略。
- 不要把 `X-Pica-Meta` 当成 body；body 始终是上游内容。
- 图片：调 `/api/img/sign` 拿签名 URL，再让浏览器加载（同源会自动带会话 cookie）；或继续走隧道。
- 401 表示会话失效，应跳登录；403 `target_blocked` 表示目标不在白名单或被 SSRF 拦截；413 请求体超限；429 需要退避重试；502 上游网络错误。
- 网关自身产生的错误带 `X-Pica-Error` 响应头（值为错误码）和 JSON body，可与上游透传的同状态码响应区分。
- 部署要求同源（Flutter Web 与网关同一域名）。跨源 `flutter run` 开发服务器不在本阶段支持范围内（未实现 CORS 凭据）。

## systemd 草案

```ini
[Unit]
Description=PicaComic gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=pica
Group=pica
WorkingDirectory=/etc/pica
ExecStart=/opt/pica/pica-gateway -config /etc/pica/config.json
Restart=on-failure
RestartSec=3
Environment=PICA_SECRET=
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/pica
CapabilityBoundingSet=
AmbientCapabilities=

[Install]
WantedBy=multi-user.target
```

配置中 `dataDir` / `cacheDir` 指向 `/var/lib/pica`；`listen` 保持 `127.0.0.1:18081`。nginx 反代示例：

```nginx
location / {
    proxy_pass http://127.0.0.1:18081;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_buffering off;          # 保持 tunnel 流式
    proxy_read_timeout 120s;
    client_max_body_size 8m;
}
```

反代时配置 `trustProxy: true`、`cookieSecure: true`，并确保只有反代能访问监听端口。

## 已知限制

- 传输层之外已有语义源 API（`/api/src/*`，仅 jm/picacg）；没有 Cloudflare 挑战求解（FlareSolverr 等）。
- 服务端下载：单实例内固定 2 并发 job（不可配）；`job.json` 与页面文件同目录，多进程同时写同一 `downloadsDir` 不受支持；JM 还原会对 JPEG 有一次质量 85 的重编码（有轻微代际损失）；JM 的 webp/png 不还原（原样保存）；取消的任务重启后不会自动恢复（已从列表移除）；`file` 接口可读取目录内的任意文件（包括 `job.json`，属预期行为）。
- 不支持 WebSocket / 101 Upgrade；`Upgrade` 头会被剥离。
- 请求体在内存中缓冲（≤8 MiB）以支持 307/308 重放；不是流式上传。
- 上游响应超过 `maxRespMB` 且没有 Content-Length 时，已发出的头部无法撤回，客户端只会收到截断的 body（日志告警）。
- 原始隧道请求的 `Referer` / `Origin` / `User-Agent` / `Cookie` 不透传（避免浏览器自动带上网关来源）；需要时通过 `X-Pica-Req-Headers` 提供，图片防盗链用 `headerProfiles`。
- 图片缓存键为 `sha256(url)`（同一 URL 不同 profile 共享缓存）；带 `jm` 参数的 token 使用独立键空间（缓存的是重排后的字节）。
- 语义源 API：jm `/latest` 无总页数，`maxPage` 采用"有内容即 page+1"的启发式；jm/picacg 的 `thumbs` 均为空数组（两源 API 均不提供缩略图，Flutter 端同样没有）；JM 现役图源基本为 webp，重排路径仅对 JPEG 生效（webp 原样返回 + warning）；picacg 凭据以可逆加密存于本机 vault，注意保管 dataDir。
- 单实例、单租户；SQLite 不适合多进程同时写。
- 会话是无状态 HMAC cookie，登出只清浏览器 cookie；已签发的 token 在 24h 内理论上仍有效（没有服务端吊销表）。
- 静态文件未做 gzip/br（交给 nginx）。
- 图片签名接口是本实现补充的端点（`GET /api/img/sign`），否则客户端无法自行生成 token。

## 验收验证

```bash
cd ~/pica-web/gateway
go build ./... && go test ./... && go test -race ./...

./pica-gateway -config config.example.json &
curl -s http://127.0.0.1:18081/healthz

# 登录（示例配置 password=change-me）
curl -s -c /tmp/pica.cookies -H 'Content-Type: application/json' \
  -d '{"password":"change-me"}' http://127.0.0.1:18081/api/auth/login

# 隧道（目标需在 allowHosts 中）
curl -s -D /tmp/pica.hdr -o /tmp/pica.body -b /tmp/pica.cookies \
  -X POST http://127.0.0.1:18081/api/tunnel \
  -H 'X-Target-URL: https://example.com/' \
  -H 'X-Target-Method: GET'

# 无会话时应 401
curl -s -o /dev/null -w '%{http_code}\n' -X POST \
  http://127.0.0.1:18081/api/tunnel -H 'X-Target-URL: https://example.com/'
```
