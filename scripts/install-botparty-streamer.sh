#!/usr/bin/env bash
set -euo pipefail

TARGET="${BOTPARTY_STREAMER_TARGET:-/usr/local/bin/botparty-streamer}"
ARCH="native"
INSTALL="true"
VERSION="${BOTPARTY_STREAMER_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || printf 'dev')}"
OUTPUT_DIR="${BOTPARTY_STREAMER_OUTPUT_DIR:-dist}"

usage() {
  cat >&2 <<'EOF'
usage: install-botparty-streamer.sh [options]

Options:
  --arch TARGET     native, linux-amd64, linux-arm64, linux-arm,
                    darwin-amd64, darwin-arm64, windows-amd64,
                    windows-arm64, linux-all or all
  --version VERSION version embedded in the binary (default: current Git tag)
  --target PATH     installation path (default: /usr/local/bin/botparty-streamer)
  --no-install      build without installing; required for cross-builds
  -h, --help        show this help

Short aliases amd64, arm64 and arm select the matching Linux target.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --arch)
      ARCH="${2:?missing value for --arch}"
      shift 2
      ;;
    --version)
      VERSION="${2:?missing value for --version}"
      shift 2
      ;;
    --target)
      TARGET="${2:?missing value for --target}"
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

if [[ ! -f go.mod ]] || ! grep -q '^module github.com/onlybrezel/botparty-streamer$' go.mod; then
  echo "run this script from the repository root" >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go toolchain not found" >&2
  exit 1
fi

mkdir -p "$OUTPUT_DIR"
LDFLAGS="-s -w -X main.version=${VERSION}"

build_target() {
  local goos="$1"
  local goarch="$2"
  local output="$3"
  local goarm="${4:-}"

  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$output" ./cmd/botparty-streamer
}

build_linux_amd64() {
  build_target linux amd64 "$OUTPUT_DIR/botparty-streamer-${VERSION}-linux-amd64"
}

build_linux_arm64() {
  build_target linux arm64 "$OUTPUT_DIR/botparty-streamer-${VERSION}-linux-arm64"
}

build_linux_arm() {
  build_target linux arm "$OUTPUT_DIR/botparty-streamer-${VERSION}-linux-armv7" 7
}

build_darwin_amd64() {
  build_target darwin amd64 "$OUTPUT_DIR/botparty-streamer-${VERSION}-darwin-amd64"
}

build_darwin_arm64() {
  build_target darwin arm64 "$OUTPUT_DIR/botparty-streamer-${VERSION}-darwin-arm64"
}

build_windows_amd64() {
  build_target windows amd64 "$OUTPUT_DIR/botparty-streamer-${VERSION}-windows-amd64.exe"
}

build_windows_arm64() {
  build_target windows arm64 "$OUTPUT_DIR/botparty-streamer-${VERSION}-windows-arm64.exe"
}

case "$ARCH" in
  native)
    go build -trimpath -ldflags "$LDFLAGS" -o "$OUTPUT_DIR/botparty-streamer" ./cmd/botparty-streamer
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
    echo "unsupported target: $ARCH" >&2
    usage
    exit 1
    ;;
esac

if [[ "$INSTALL" != true ]]; then
  echo "build complete: $OUTPUT_DIR" >&2
  exit 0
fi

if [[ "$ARCH" != native ]]; then
  echo "cross-builds require --no-install" >&2
  exit 1
fi

SOURCE="$OUTPUT_DIR/botparty-streamer"
if install -m 0755 "$SOURCE" "$TARGET" 2>/dev/null; then
  echo "installed $TARGET" >&2
  exit 0
fi

if ! command -v sudo >/dev/null 2>&1; then
  echo "cannot write $TARGET; choose a writable path with --target" >&2
  exit 1
fi

sudo install -m 0755 "$SOURCE" "$TARGET"
echo "installed $TARGET" >&2
