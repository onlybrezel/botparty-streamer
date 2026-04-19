#!/usr/bin/env bash
set -euo pipefail

TARGET="${BOTPARTY_STREAMER_TARGET:-/home/pi/bin/botparty-streamer}"
ARCH="native"
INSTALL="true"
VERSION="v0.1.3"
LDFLAGS="-s -w -X main.version=${VERSION}"

usage() {
  cat >&2 <<'EOF'
usage: install-botparty-streamer.sh [--arch native|amd64|arm64|arm|darwin-amd64|darwin-arm64|windows-amd64|windows-arm64|linux-all|all] [--version v0.1.1] [--no-install]

Defaults:
  --arch native
  install native binary to /home/pi/bin/botparty-streamer

Cross-build targets:
  amd64           linux/amd64
  arm64           linux/arm64   (Raspberry Pi 4/5 64-bit)
  arm             linux/armv7   (Raspberry Pi 2/3 32-bit)
  darwin-amd64    macOS Intel
  darwin-arm64    macOS Apple Silicon
  windows-amd64   Windows x86_64
  windows-arm64   Windows ARM64
  linux-all       all Linux release targets
  all             all supported release targets

Examples:
  ./scripts/install-botparty-streamer.sh
  ./scripts/install-botparty-streamer.sh --arch amd64 --version v0.1.1 --no-install
  ./scripts/install-botparty-streamer.sh --arch arm64 --version v0.1.1 --no-install
  ./scripts/install-botparty-streamer.sh --arch arm   --version v0.1.1 --no-install
  ./scripts/install-botparty-streamer.sh --arch linux-all --version v0.1.1 --no-install
  ./scripts/install-botparty-streamer.sh --arch all       --version v0.1.1 --no-install
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --arch)
      ARCH="${2:-}"
      shift 2
      ;;
    --version)
      VERSION="${2:-}"
      LDFLAGS="-s -w -X main.version=${VERSION}"
      shift 2
      ;;
    --no-install)
      INSTALL="false"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ ! -f "go.mod" ]] || ! grep -q '^module botparty-streamer$' go.mod; then
  echo "Run this script from the botparty-streamer repository root." >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go toolchain not found" >&2
  exit 1
fi

mkdir -p bin

build_native() {
  go build -ldflags "$LDFLAGS" -o bin/botparty-streamer .
  if [[ ! -x "bin/botparty-streamer" ]]; then
    echo "build failed: bin/botparty-streamer missing" >&2
    exit 1
  fi
}

build_target() {
  local goos="$1"
  local goarch="$2"
  local output="$3"
  local goarm="${4:-}"

  if [[ -n "$goarm" ]]; then
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
      go build -ldflags "$LDFLAGS" -o "$output" .
    return
  fi

  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -ldflags "$LDFLAGS" -o "$output" .
}

build_linux_amd64() {
  build_target linux amd64 "bin/botparty-streamer-${VERSION}-linux-amd64"
}

build_linux_arm64() {
  build_target linux arm64 "bin/botparty-streamer-${VERSION}-linux-arm64"
}

build_linux_arm() {
  build_target linux arm "bin/botparty-streamer-${VERSION}-linux-arm" 7
}

build_darwin_amd64() {
  build_target darwin amd64 "bin/botparty-streamer-${VERSION}-darwin-amd64"
}

build_darwin_arm64() {
  build_target darwin arm64 "bin/botparty-streamer-${VERSION}-darwin-arm64"
}

build_windows_amd64() {
  build_target windows amd64 "bin/botparty-streamer-${VERSION}-windows-amd64.exe"
}

build_windows_arm64() {
  build_target windows arm64 "bin/botparty-streamer-${VERSION}-windows-arm64.exe"
}

case "$ARCH" in
  native)
    build_native
    ;;
  amd64|linux-amd64)
    build_linux_amd64
    ;;
  arm64|linux-arm64|rpi)
    build_linux_arm64
    ;;
  arm|linux-arm|armv7|armhf)
    build_linux_arm
    ;;
  darwin-amd64|macos-amd64)
    build_darwin_amd64
    ;;
  darwin-arm64|macos-arm64)
    build_darwin_arm64
    ;;
  windows-amd64|win-amd64)
    build_windows_amd64
    ;;
  windows-arm64|win-arm64)
    build_windows_arm64
    ;;
  linux-all)
    build_linux_amd64
    build_linux_arm64
    build_linux_arm
    ;;
  all)
    build_linux_amd64
    build_linux_arm64
    build_linux_arm
    build_darwin_amd64
    build_darwin_arm64
    build_windows_amd64
    build_windows_arm64
    ;;
  *)
    echo "unsupported arch: $ARCH" >&2
    usage
    exit 1
    ;;
esac

if [[ "$INSTALL" != "true" ]]; then
  echo "Build complete for arch=$ARCH (no install requested)" >&2
  exit 0
fi

if [[ "$ARCH" != "native" ]]; then
  echo "install step is only supported with --arch native" >&2
  echo "Use --no-install when building cross targets." >&2
  exit 1
fi

TARGET_DIR="$(dirname "$TARGET")"
mkdir -p "$TARGET_DIR"
install -m 0755 bin/botparty-streamer "$TARGET" 2>/dev/null || {
  sudo install -m 0755 bin/botparty-streamer "$TARGET"
}
echo "Installed $TARGET from $(pwd)/bin/botparty-streamer" >&2
