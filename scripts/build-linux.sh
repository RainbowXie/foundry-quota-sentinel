#!/usr/bin/env bash
# 在 docker 里编译带 GUI 的 Linux 二进制（webview 需 CGO + webkit2gtk）。
#
# 支持两套 webkit ABI（并行、不兼容，设备各取所需）：
#   WEBKIT=4.0  ubuntu:22.04 + libwebkit2gtk-4.0-dev（老发行版，如 Ubuntu 20.04/22.04、Mint 21）
#   WEBKIT=4.1  ubuntu:24.04 + libwebkit2gtk-4.1-dev（新发行版，如 Ubuntu 24.04、Mint 22）
#
# webview_go 这一版把 cgo 的 pkg-config 钉死在 webkit2gtk-4.0；编 4.1 时用一个
# pkg-config shim 把 webkit2gtk-4.0 重定向到 webkit2gtk-4.1（已实测能编、能链到 4.1）。
#
# 用法：
#   scripts/build-linux.sh                                  # 默认 4.0
#   WEBKIT=4.1 OUTPUT=build/fqs-wk41 scripts/build-linux.sh
#
# 环境变量：
#   WEBKIT      4.0 | 4.1，默认 4.0
#   OUTPUT      输出路径（相对仓库根），默认 build/foundry-quota-sentinel-linux-amd64
#   GO_VERSION  Go 版本，默认从 go.mod 读取（取不到则 1.26.4）
#   GO_MIRROR   Go 下载镜像（默认官方 go.dev，国内可用 tuna）
#   IMAGE       基础镜像，默认按 WEBKIT 选 22.04 / 24.04
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

WEBKIT="${WEBKIT:-4.0}"
OUTPUT="${OUTPUT:-build/foundry-quota-sentinel-linux-amd64}"
case "$WEBKIT" in
  4.0) DEFAULT_IMAGE="ubuntu:22.04"; WK_DEV="libwebkit2gtk-4.0-dev"; USE_SHIM=0 ;;
  4.1) DEFAULT_IMAGE="ubuntu:24.04"; WK_DEV="libwebkit2gtk-4.1-dev"; USE_SHIM=1 ;;
  *) echo "WEBKIT 只能是 4.0 或 4.1（收到 '$WEBKIT'）" >&2; exit 2 ;;
esac
IMAGE="${IMAGE:-$DEFAULT_IMAGE}"
# Go 下载镜像：默认官方 go.dev；国内构建可用 tuna 镜像加速
# （https://mirrors.tuna.tsinghua.edu.cn/golang/，路径结构同官方）。
GO_BASE="${GO_MIRROR:-https://go.dev/dl}"
if [ -z "${GO_VERSION:-}" ]; then
  GO_VERSION="$(awk '/^go [0-9]/{print $2; exit}' go.mod 2>/dev/null)"
  GO_VERSION="${GO_VERSION:-1.26.4}"
fi

# 本机 module cache（存在则只读挂载进容器，省去重复下载依赖）
HOST_MODCACHE="$(go env GOMODCACHE 2>/dev/null || true)"
MOUNT_MOD=()
GOMOD_ENV=""
if [ -n "$HOST_MODCACHE" ] && [ -d "$HOST_MODCACHE" ]; then
  MOUNT_MOD=(-v "$HOST_MODCACHE:/gomod:ro")
  GOMOD_ENV="export GOMODCACHE=/gomod GOFLAGS=-mod=mod;"
fi

HOST_UID="$(id -u)"
HOST_GID="$(id -g)"

mkdir -p "$(dirname "$OUTPUT")"

echo ">>> 镜像 $IMAGE / webkit $WEBKIT / Go $GO_VERSION / 输出 $OUTPUT"

# 编 4.1 时注入 pkg-config shim，把 webkit2gtk-4.0 重定向到 4.1
SHIM_SETUP=""
if [ "$USE_SHIM" = "1" ]; then
  SHIM_SETUP='mkdir -p /tmp/pcshim; printf "Name: webkit2gtk-4.0\nDescription: shim->4.1\nVersion: 2.44.0\nRequires: webkit2gtk-4.1\n" > /tmp/pcshim/webkit2gtk-4.0.pc; export PKG_CONFIG_PATH=/tmp/pcshim:/usr/lib/x86_64-linux-gnu/pkgconfig:/usr/share/pkgconfig;'
fi

docker run --rm \
  -v "$REPO_ROOT:/src" \
  "${MOUNT_MOD[@]}" \
  -w /src "$IMAGE" bash -c "
set -e
export DEBIAN_FRONTEND=noninteractive
echo '>>> apt: gtk3 + ${WK_DEV}'
apt-get update -qq
apt-get install -y -qq --no-install-recommends ca-certificates curl build-essential pkg-config libgtk-3-dev ${WK_DEV} >/dev/null
echo '>>> 下载 Go ${GO_VERSION}'
curl -fsSL ${GO_BASE}/go${GO_VERSION}.linux-amd64.tar.gz | tar -C /usr/local -xz
export PATH=/usr/local/go/bin:\$PATH
${GOMOD_ENV}
export GOCACHE=/tmp/gocache CGO_ENABLED=1
${SHIM_SETUP}
echo \">>> \$(go version)\"
go build -ldflags='-s -w' -o '/src/${OUTPUT}' .
chown ${HOST_UID}:${HOST_GID} '/src/${OUTPUT}'
echo '>>> BUILD DONE'
"

echo ">>> 完成: $OUTPUT"
file "$OUTPUT"
