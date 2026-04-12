#!/usr/bin/env bash
set -euo pipefail

TARGET="${BOTPARTY_STREAMER_TARGET:-/home/pi/bin/botparty-streamer}"
ARCH="native"
INSTALL="true"
VERSION="v0.1.0"

usage() {
  cat >&2 <<'EOF'
usage: install-botparty-streamer.sh [--arch native|amd64|arm64|arm|all] [--version v0.1.0] [--no-install]

Defaults:
  --arch native
  install native binary to /home/pi/bin/botparty-streamer

Cross-build targets:
  amd64   linux/amd64
  arm64   linux/arm64  (Raspberry Pi 4/5 64-bit)
  arm     linux/armv7  (Raspberry Pi 2/3 32-bit)
  all     all three cross targets

Examples:
  ./scripts/install-botparty-streamer.sh
  ./scripts/install-botparty-streamer.sh --arch amd64 --version v0.1.0 --no-install
  ./scripts/install-botparty-streamer.sh --arch arm64 --version v0.1.0 --no-install
  ./scripts/install-botparty-streamer.sh --arch arm   --version v0.1.0 --no-install
  ./scripts/install-botparty-streamer.sh --arch all   --version v0.1.0 --no-install
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
  go build -o bin/botparty-streamer .
  if [[ ! -x "bin/botparty-streamer" ]]; then
    echo "build failed: bin/botparty-streamer missing" >&2
    exit 1
  fi
}

build_linux_amd64() {
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "bin/botparty-streamer-${VERSION}-linux-amd64" .
}

build_linux_arm64() {
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "bin/botparty-streamer-${VERSION}-linux-arm64" .
}

build_linux_arm() {
  CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o "bin/botparty-streamer-${VERSION}-linux-arm" .
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
  all)
    build_linux_amd64
    build_linux_arm64
    build_linux_arm
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
