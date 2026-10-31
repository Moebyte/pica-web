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

echo "==> Go gateway build"
cd "$ROOT/gateway"
go build -trimpath -ldflags "-s -w" -o "$ROOT/pica-gateway" ./cmd/pica-gateway

echo "==> 完成"
ls -lh "$ROOT/pica-gateway"
du -sh "$ROOT/web"
