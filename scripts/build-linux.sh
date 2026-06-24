#!/usr/bin/env bash
# 在 ubuntu:22.04 容器里编译带 GUI 的 Linux 二进制（webview 需 CGO + webkit2gtk-4.0）。
#
# 为什么用 22.04：webview_go 这一版把 linux 的 pkg-config 钉死在 webkit2gtk-4.0，
# ubuntu 24.04 只剩 4.1 会编不过，22.04 才有 4.0（CI 也用 22.04 + libwebkit2gtk-4.0-dev）。
#
# 用法：
#   scripts/build-linux.sh                  # 默认输出 build/foundry-quota-sentinel-linux-amd64
#   OUTPUT=build/ocgt GO_VERSION=1.26.4 scripts/build-linux.sh
#
# 环境变量：
#   OUTPUT      输出路径（相对仓库根），默认 build/foundry-quota-sentinel-linux-amd64
#   GO_VERSION  Go 版本，默认从 go.mod 的 `go` 行读取（取不到则 1.26.4）
#   IMAGE       基础镜像，默认 ubuntu:22.04
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

OUTPUT="${OUTPUT:-build/foundry-quota-sentinel-linux-amd64}"
IMAGE="${IMAGE:-ubuntu:22.04}"
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

echo ">>> 镜像 $IMAGE / Go $GO_VERSION / 输出 $OUTPUT"

docker run --rm \
  -v "$REPO_ROOT:/src" \
  "${MOUNT_MOD[@]}" \
  -w /src "$IMAGE" bash -c "
set -e
export DEBIAN_FRONTEND=noninteractive
echo '>>> apt: gtk3 + webkit2gtk-4.0 dev'
apt-get update -qq
apt-get install -y -qq --no-install-recommends ca-certificates curl build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.0-dev >/dev/null
echo '>>> 下载 Go ${GO_VERSION}'
curl -fsSL https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz | tar -C /usr/local -xz
export PATH=/usr/local/go/bin:\$PATH
${GOMOD_ENV}
export GOCACHE=/tmp/gocache CGO_ENABLED=1
echo \">>> \$(go version)\"
go build -o '/src/${OUTPUT}' .
chown ${HOST_UID}:${HOST_GID} '/src/${OUTPUT}'
echo '>>> BUILD DONE'
"

echo ">>> 完成: $OUTPUT"
file "$OUTPUT"
