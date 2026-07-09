#!/usr/bin/env bash
# Build the moonbridge Go binary as a Tauri externalBin sidecar.
# Output: desktop/src-tauri/binaries/moonbridge-<target-triple>[.exe]
#
# Env:
#   SKIP_WEBUI=1              — skip WebUI rebuild; require existing embed dist
#   TARGET / TAURI_ENV_TARGET_TRIPLE — override triple (for cross builds)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DESKTOP_DIR="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="${DESKTOP_DIR}/src-tauri/binaries"
mkdir -p "${OUT_DIR}"

# Resolve Rust/Tauri target triple (must match externalBin naming).
TRIPLE="${TAURI_ENV_TARGET_TRIPLE:-${TARGET:-}}"
if [[ -z "${TRIPLE}" ]]; then
  if command -v rustc >/dev/null 2>&1; then
    TRIPLE="$(rustc -vV | awk '/^host:/{print $2}')"
  else
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"
    case "${ARCH}" in
      arm64|aarch64) ARCH="aarch64" ;;
      x86_64|amd64) ARCH="x86_64" ;;
    esac
    case "${OS}" in
      darwin) TRIPLE="${ARCH}-apple-darwin" ;;
      linux) TRIPLE="${ARCH}-unknown-linux-gnu" ;;
      mingw*|msys*|cygwin*|windows*) TRIPLE="${ARCH}-pc-windows-msvc" ;;
      *)
        echo "unsupported platform: ${OS}/${ARCH}" >&2
        exit 1
        ;;
    esac
  fi
fi

EXT=""
case "${TRIPLE}" in
  *windows*) EXT=".exe" ;;
esac

# Map triple → GOOS/GOARCH for optional cross-compile.
case "${TRIPLE}" in
  aarch64-apple-darwin) export GOOS=darwin GOARCH=arm64 ;;
  x86_64-apple-darwin) export GOOS=darwin GOARCH=amd64 ;;
  aarch64-unknown-linux-gnu|aarch64-unknown-linux-musl) export GOOS=linux GOARCH=arm64 ;;
  x86_64-unknown-linux-gnu|x86_64-unknown-linux-musl) export GOOS=linux GOARCH=amd64 ;;
  aarch64-pc-windows-msvc) export GOOS=windows GOARCH=arm64 ;;
  x86_64-pc-windows-msvc) export GOOS=windows GOARCH=amd64 ;;
  *)
    # Host go build defaults when triple is unknown
    ;;
esac

OUT="${OUT_DIR}/moonbridge-${TRIPLE}${EXT}"

if [[ "${SKIP_WEBUI:-0}" == "1" ]]; then
  if [[ ! -f "${ROOT}/internal/service/webui/dist/index.html" ]]; then
    echo "SKIP_WEBUI=1 but embed dist missing: internal/service/webui/dist/index.html" >&2
    echo "Run without SKIP_WEBUI, or: make webui-build" >&2
    exit 1
  fi
  echo "==> SKIP_WEBUI=1, reusing existing embed dist"
else
  if [[ ! -d "${ROOT}/webui/node_modules" ]]; then
    echo "==> webui/node_modules missing; running npm --prefix webui install"
    npm --prefix "${ROOT}/webui" install
  fi
  echo "==> building WebUI + embedding into Go tree"
  make -C "${ROOT}" webui-build
fi

echo "==> building moonbridge sidecar -> ${OUT}"
# CGO free; strip like Docker for smaller bundles.
CGO_ENABLED=0 go build -C "${ROOT}" -trimpath -ldflags="-s -w" -o "${OUT}" ./cmd/moonbridge

# Convenience bare name (symlink on Unix, copy on Windows-ish envs).
BARE="${OUT_DIR}/moonbridge${EXT}"
if ln -sfn "$(basename "${OUT}")" "${BARE}" 2>/dev/null; then
  :
else
  cp -f "${OUT}" "${BARE}"
fi

echo "==> sidecar ready: ${OUT}"
ls -lh "${OUT}"
