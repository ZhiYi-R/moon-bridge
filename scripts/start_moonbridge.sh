#!/usr/bin/env bash
# Build and start the Moon Bridge server in background.
# Saves connection info to .moonbridge.env for client scripts to consume.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="${MOONBRIDGE_CONFIG:-"${ROOT_DIR}/config.yml"}"
SERVER_BIN="${ROOT_DIR}/.cache/moonbridge/moonbridge"
CACHE_DIR="${ROOT_DIR}/.cache/moonbridge"
LOG_FILE="${ROOT_DIR}/logs/moonbridge.log"
ENV_FILE="${ROOT_DIR}/.moonbridge.env"

source "${ROOT_DIR}/scripts/lib/common.sh"
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then :; else echo "Do not source this script; run it directly." >&2; return 1; fi

require_command go
check_config_file

mkdir -p "$(dirname "$LOG_FILE")" "$CACHE_DIR"
setup_build_cache

build_moonbridge "$SERVER_BIN"

extract_server_metadata
ensure_port_free

# Write env file for client scripts.
cat > "$ENV_FILE" <<EOF
MOONBRIDGE_ADDR="${ADDR}"
MOONBRIDGE_MODE="${MODE}"
MOONBRIDGE_DEFAULT_MODEL="$("$SERVER_BIN" --config "$CONFIG_FILE" --print-default-model 2>/dev/null || true)"
MOONBRIDGE_CODEX_MODEL="$("$SERVER_BIN" --config "$CONFIG_FILE" --print-codex-model 2>/dev/null || true)"
MOONBRIDGE_CLAUDE_MODEL="$("$SERVER_BIN" --config "$CONFIG_FILE" --print-claude-model 2>/dev/null || true)"
MOONBRIDGE_CONFIG_FILE="${CONFIG_FILE}"
MOONBRIDGE_SERVER_BIN="${SERVER_BIN}"
MOONBRIDGE_LOG_FILE="${LOG_FILE}"
EOF

cleanup() {
  cleanup_server
  rm -f "$ENV_FILE"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

start_server_background

log "Server running in background (PID ${SERVER_PID}). Press Ctrl+C to stop."
wait "$SERVER_PID"
