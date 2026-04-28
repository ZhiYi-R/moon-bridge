#!/usr/bin/env bash
# One-click: build Moon Bridge -> start (CaptureAnthropic) -> configure -> launch Claude Code.
set -euo pipefail

ROOT_DIR="$(cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)" && pwd)"
CONFIG_FILE="${MOONBRIDGE_CONFIG:-"${ROOT_DIR}/config.yml"}"
CLAUDE_CONFIG_DIR_VALUE="${ROOT_DIR}/FakeHome/ClaudeCode"
GLOBAL_CLAUDE_SETTINGS="${MOONBRIDGE_CLAUDE_SETTINGS:-"${HOME}/.claude/settings.json"}"
SERVER_BIN="${ROOT_DIR}/.cache/start-claude/moonbridge"
LOG_FILE="${ROOT_DIR}/logs/moonbridge-claude-code.log"
PROMPT="${1:-}"

source "${ROOT_DIR}/scripts/lib/common.sh"
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then :; else echo "Do not source this script; run it directly." >&2; return 1; fi

mkdir -p "$(dirname "$LOG_FILE")"
: > "$LOG_FILE"

require_command go
require_command claude
require_command python3
setup_build_cache

check_config_file
build_moonbridge "$SERVER_BIN"

extract_server_metadata
validate_mode "$MODE" CaptureAnthropic

MODEL="$("$SERVER_BIN" --config "$CONFIG_FILE" --print-claude-model 2>>"$LOG_FILE")"

ensure_port_free
mkdir -p "$CLAUDE_CONFIG_DIR_VALUE"

prepare_claude_settings \
  "${CLAUDE_CONFIG_DIR_VALUE}/settings.json" \
  "${CLAUDE_CONFIG_DIR_VALUE}/moonbridge-env.sh" \
  "http://${BASE_ADDR}" \
  "$GLOBAL_CLAUDE_SETTINGS" \
  "$MODEL" > >(tee -a "$LOG_FILE") 2>&1

register_server_cleanup
start_server_background

export CLAUDE_CONFIG_DIR="$CLAUDE_CONFIG_DIR_VALUE"

log "Starting Claude Code with CLAUDE_CONFIG_DIR=${CLAUDE_CONFIG_DIR}"
log "Workspace: ${ROOT_DIR}"
log "Anthropic base URL: http://${BASE_ADDR}"
if [[ -n "${MOONBRIDGE_EFFECTIVE_CLAUDE_MODEL:-}" ]]; then
  log "Model: ${MOONBRIDGE_EFFECTIVE_CLAUDE_MODEL}"
fi

set +e
if [[ -n "$PROMPT" ]]; then
  claude "$PROMPT"
else
  claude
fi
CLAUDE_STATUS=$?
set -e

log "Claude Code exited with status ${CLAUDE_STATUS}"
exit "$CLAUDE_STATUS"
