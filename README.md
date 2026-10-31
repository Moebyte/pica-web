# pica-web — PicaComic B/S 化

把开源 Flutter 漫画客户端 [PicaComic](https://github.com/Pacalini/PicaComic)（MIT）改造成浏览器可用的 B/S 应用。
**路线 1 + 3**：阶段 1 用原 Flutter UI 编译成 Web + Go 网关隧道；阶段 2 再做 Vue 3 前端和 Go 语义 API。
首版范围：**在线阅读 + 收藏/历史**。

> **当前进度（2026-10-31）**：首版已跑通并上线——**https://picacg.example.com/**
> （`pica-gateway.service` 常驻）。hitomi 与 nhentai 的探索/详情/阅读/翻页、
> 收藏、历史、刷新持久化均通过真实浏览器 E2E；构建与单测全绿。
> 详见 [`docs/STATUS.md`](docs/STATUS.md)。

## 目录

```
pica-web/
├── app/          # PicaComic fork（Flutter，Web 兼容改造）
├── gateway/      # Go 网关（隧道/Cookie Vault/SSRF/图片/认证）
├── web/          # Flutter Web 构建产物（deploy/build.sh 生成）
├── config.dev.json
├── pica-gateway  # 网关二进制
├── docs/
│   ├── ARCHITECTURE.md
│   └── WEB_COMPAT.md
└── deploy/
    ├── build.sh
    ├── pica-gateway.service
    └── nginx-pica.conf
```

## 快速开始

```bash
# 1. 构建（Flutter 3.47.9 + Go 1.25；CanvasKit 本地托管）
./deploy/build.sh

# 2. 本机开发运行（网关 dev 模式只允许 loopback 访问 /api）
./pica-gateway -config config.dev.json

# 3. 浏览器打开
#    http://127.0.0.1:18081/
#
# 线上（已部署）：
#    https://picacg.example.com/    → /opt/pica（pica-gateway.service）
```

生产部署：`deploy/pica-gateway.service` + `deploy/nginx-pica.conf`，网关只监听 127.0.0.1，
由 nginx 终止 TLS；认证二选一（网关密码或 nginx basic auth）。

### 已验证

| 能力 | 状态 |
|---|---|
| hitomi 探索/详情/阅读/翻页 | ✅ 真实浏览器 E2E |
| nhentai 探索/搜索/详情/阅读（API v2） | ✅ 真实浏览器 E2E |
| 收藏（本地收藏夹） | ✅ 收藏/已收藏状态切换 |
| 阅读历史 | ✅ 刷新后仍在（IndexedDB） |
| **账号服务端同步** | ✅ 登录一次，任意设备/浏览器打开即已登录（凭据+Cookie+相关设置存网关加密存储） |
| **收藏/历史/图片收藏同步** | ✅ 多设备共享，删除可传播（服务端整库版本同步） |
| **服务端下载** | ✅ 详情页下载到服务器磁盘，Web 内浏览已下载漫画（JM 含图片重排） |
| onboarding 全流程 | ✅ |
| 网关登录页 / 隧道 / Cookie Vault / SSRF | ✅ 单测 + curl |
| 绅士漫画列表/搜索/详情 | ⚠️ 可用；阅读页可能遇 Cloudflare |
| picacg / ehentai | ⚠️ 未验证 |
| jmcomic（禁漫天堂） | ✅ 探索/详情/阅读/图片重组/登录流程已验证（2026-10-31，含域名自动刷新） |
| 下载 / 自定义 JS 源 / WebDAV | ❌ 首版不提供 |

## 阶段 1 架构

```
浏览器（Flutter Web）
  → Dio TunnelAdapter
  → POST /api/tunnel（X-Target-URL / X-Target-Method / X-Pica-Req-Headers）
  → Go 网关：鉴权 → 白名单/SSRF → Cookie Vault 注入 → 出站（可选 SOCKS5）
  → 漫画站；响应流式回传，Meta 带回 status/headers/setCookies
```

## 已知边界（阶段 1）

- 只支持内置 6 源；自定义 JS 源不可用（QuickJS 无 Web 实现）。
- 下载/导出/WebDAV/通知/生物识别/后台任务在 Web 端降级或禁用。
- 图片缓存为浏览器内存；服务端图片缓存可选用 `/api/img`。
- 单用户、单实例；多用户/多租户留给阶段 2。
- Cloudflare 交互式挑战尚未接 FlareSolverr；被挑战时需人工处理或后续补。

## 上游与许可

- PicaComic：MIT，Copyright (c) 2023 Nyne，fork 自 wgh136/PicaComic。
- 本目录新增代码（网关/部署/文档）默认按 MIT 使用；第三方源的内容版权与站点条款由使用者自行承担。

---

## Attribution & License

- `app/` 基于 [Pacalini/PicaComic](https://github.com/Pacalini/PicaComic)（MIT，upstream of [wgh136/PicaComic](https://github.com/wgh136/PicaComic)），基线 commit `a9c896d`，其原始 LICENSE 保留在 `app/LICENSE`。
- 其余部分（`gateway/`、`deploy/`、`docs/`）：MIT，见根目录 LICENSE，Copyright (c) 2026 Moebyte。

## Disclaimer

- 本项目仅用于个人学习与自建部署；所有漫画内容与版权归属各源站，使用者需自行遵守当地法律与源站条款。
- 部分源站包含成人内容，请确认你已到达法定年龄且所在辖区允许访问。
- 不对源站可用性、账号风控后果作任何保证。

*PicaComic, self-hosted in the browser: Flutter Web UI + a Go gateway (transparent tunnel, cookie vault, server-side downloads with JM image recombination, encrypted account/data sync).*
