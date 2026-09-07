#!/usr/bin/env bash
# scripts/export-sdk.sh
# 根据 pkg/sdk/export-manifest.json 规范，将 pkg/sdk 导出为独立 Go 模块。
# 负责：
# 1. 拷贝代码树到目标目录；
# 2. 生成独立的 go.mod；
# 3. 将内部 import 路径从 "foundry-quota-sentinel/pkg/sdk/..." 转换为目标 module 路径（如 "github.com/ethan/quota-sdk-go/..."）；
# 4. 运行 go test 验证独立编译与测试通过。

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="$REPO_ROOT/pkg/sdk/export-manifest.json"

if [ ! -f "$MANIFEST" ]; then
    echo "错误: 未找到 $MANIFEST" >&2
    exit 1
fi

# 前置依赖检查：解析 export-manifest.json 必须依赖 jq 工具
if ! command -v jq >/dev/null 2>&1; then
    echo "错误: 导出脚本需要 jq 支持，请先安装 jq" >&2
    exit 1
fi

TARGET_DIR="${1:-${EXPORT_DIR:-$REPO_ROOT/build/quota-sdk-go}}"
MODULE_NAME="$(jq -r '.module' "$MANIFEST")"
GO_VERSION="$(jq -r '.go_version' "$MANIFEST")"

echo "=== 正在导出 SDK 到: $TARGET_DIR ==="
echo "Module:     $MODULE_NAME"
echo "Go Version: $GO_VERSION"

rm -rf "$TARGET_DIR"
mkdir -p "$TARGET_DIR"

# 拷贝 pkg/sdk 内所有源码与文档
cp -a "$REPO_ROOT/pkg/sdk/"* "$TARGET_DIR/"
rm -f "$TARGET_DIR/export-manifest.json"

# 生成独立 go.mod
cat <<EOF > "$TARGET_DIR/go.mod"
module $MODULE_NAME

go $GO_VERSION

require (
	github.com/gorilla/websocket v1.5.3
	golang.org/x/sys v0.45.0
)
EOF

# 重写内部包 import 路径
# 说明：macOS 的 BSD sed 要求 -i 必须提供备份扩展名（例如 -i ''），
# 而 GNU sed 直接接受 -i。为了在 macOS 与 Linux 下均能平滑运行，统一使用备份后缀再清理，
# 规避各操作系统平台下 sed -i 参数解析歧义。
find "$TARGET_DIR" -type f -name "*.go" | while read -r file; do
    sed -i.bak "s|\"foundry-quota-sentinel/pkg/sdk/|\"$MODULE_NAME/|g" "$file"
    rm -f "${file}.bak"
done

echo "=== 正在校验独立导出的 SDK ==="
(
    cd "$TARGET_DIR"
    go mod tidy
    CGO_ENABLED=0 go test ./...
)

echo "=== SDK 成功导出至 $TARGET_DIR 且独立测试全绿 ==="
