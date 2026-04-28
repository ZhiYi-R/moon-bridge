#!/usr/bin/env bash
# One-click: build Moon Bridge -> start -> generate Codex config -> launch Codex.
set -euo pipefail

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  :  # running directly
else
  echo "Do not source this script; run it directly." >&2
  return 1
fi

usage() {
  cat <<EOF
Usage: $(basename "$0") --project-directory <dir> [--codex-home <dir>]

Required:
  --project-directory <dir>   Workspace to launch Codex in

Optional:
  --codex-home <dir>          Codex home directory (default: ${CODEX_HOME:-$HOME/.codex})
EOF
  exit 1
}

CODEX_HOME_DIR="${CODEX_HOME:-$HOME/.codex}"
PROJECT_DIR=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --codex-home)        CODEX_HOME_DIR="$2"; shift 2 ;;
    --project-directory) PROJECT_DIR="$2";     shift 2 ;;
    *)                   usage ;;
  esac
done

if [[ -z "$PROJECT_DIR" ]]; then
  echo "Error: --project-directory is required"
  usage
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="${MOONBRIDGE_CONFIG:-"${ROOT_DIR}/config.yml"}"
SERVER_BIN="${ROOT_DIR}/.cache/start-codex/moonbridge"
LOG_FILE="${ROOT_DIR}/logs/moonbridge-codex.log"

source "${ROOT_DIR}/scripts/lib/common.sh"

mkdir -p "$(dirname "$LOG_FILE")"
: > "$LOG_FILE"

require_command go
require_command codex
setup_build_cache

check_config_file
build_moonbridge "$SERVER_BIN"

extract_server_metadata
validate_mode "$MODE" Transform CaptureResponse

MODEL_ALIAS="$("$SERVER_BIN" --config "$CONFIG_FILE" --print-codex-model 2>>"$LOG_FILE")"
if [[ -z "$MODEL_ALIAS" ]]; then
  log_error "default_model or developer.proxy.response.model required for Codex"
  exit 1
fi

ensure_port_free
mkdir -p "$CODEX_HOME_DIR"
generate_codex_config "$MODEL_ALIAS" "$CODEX_HOME_DIR" "http://${BASE_ADDR}/v1"

register_server_cleanup
start_server_background

export CODEX_HOME="$CODEX_HOME_DIR"
export MOONBRIDGE_CLIENT_API_KEY="${MOONBRIDGE_CLIENT_API_KEY:-local-dev}"

log "Starting Codex with CODEX_HOME=${CODEX_HOME_DIR}"
log "Workspace: ${PROJECT_DIR}"
log "Mode: ${MODE}"
log "Model: ${MODEL_ALIAS}"

codex_args=(
  --sandbox workspace-write
  --ask-for-approval on-request
  --cd "$PROJECT_DIR"
)

set +e
codex "${codex_args[@]}"
CODEX_STATUS=$?
set -e

log "Codex exited with status ${CODEX_STATUS}"
exit "$CODEX_STATUS"
