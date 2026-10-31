#!/usr/bin/env bash
# 构建 Flutter Web + Go 网关，并同步静态产物。
# 用法：./deploy/build.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FLUTTER_BIN="${FLUTTER_BIN:-/opt/flutter}"

echo "==> Flutter Web release build"
cd "$ROOT/app"
"$FLUTTER_BIN" pub get
# 本地托管 CanvasKit，避免运行时依赖 gstatic（内网/离线部署更稳）
"$FLUTTER_BIN" build web --release --no-web-resources-cdn

echo "==> 同步静态产物到 $ROOT/web"
rm -rf "$ROOT/web"
mkdir -p "$ROOT/web"
cp -a "$ROOT/app/build/web/." "$ROOT/web/"

echo "==> Vue webui build（阶段2 /v2/ 前端）"
export PATH=/www/server/nodejs/v24.15.0/bin:$PATH
cd "$ROOT/webui"
[ -d node_modules ] || npm ci --no-audit --no-fund || npm install --no-audit --no-fund
npm run build

echo "==> Go gateway build"
cd "$ROOT/gateway"
go build -trimpath -ldflags "-s -w" -o "$ROOT/pica-gateway" ./cmd/pica-gateway

install -d "$ROOT/../pica-web" 2>/dev/null || true
echo "==> 完成"
ls -lh "$ROOT/pica-gateway"
du -sh "$ROOT/web" "$ROOT/webui/dist"
