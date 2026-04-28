# shellcheck shell=bash
# Moon Bridge common script library.
# Source at top of each script: source "${ROOT_DIR}/scripts/lib/common.sh"
#
# Expected variables before sourcing (set by caller script):
#   ROOT_DIR    resolved project root
#   LOG_FILE    path to log file (fallback: /dev/null)
#   CONFIG_FILE path to config.yml (optional, used by server scripts)

if [[ -z "${BASH_SOURCE[0]}" || "${BASH_SOURCE[0]}" == "$0" ]]; then
  echo "This file is a library and should be sourced, not executed directly." >&2
  exit 1
fi

: "${LOG_FILE:=/dev/null}"

# ── Logging ──────────────────────────────────────────────

log()       { printf '%s\n' "$*" | tee -a "$LOG_FILE"; }
log_error() { printf '%s\n' "$*" | tee -a "$LOG_FILE" >&2; }

# ── Prerequisites ────────────────────────────────────────

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    log_error "missing required command: $1"
    exit 1
  fi
}

# ── Path resolution ──────────────────────────────────────

resolve_root_dir() {
  ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[1]}")/.." && pwd)"
  readonly ROOT_DIR
}

# ── Address parsing ──────────────────────────────────────

# Sets HOST, PORT, BASE_ADDR from "$1" (format: "host:port" or ":port")
parse_addr() {
  local addr="${1:?parse_addr: address required}"
  if [[ "$addr" == :* ]]; then
    HOST="127.0.0.1"
    PORT="${addr#:}"
  else
    HOST="${addr%:*}"
    PORT="${addr##*:}"
  fi
  BASE_ADDR="${HOST}:${PORT}"
}

# ── Config file ──────────────────────────────────────────

check_config_file() {
  if [[ ! -f "${CONFIG_FILE:-}" ]]; then
    log_error "missing config file: ${CONFIG_FILE:-<unset>}"
    exit 1
  fi
}

# ── Port check ────────────────────────────────────────────

ensure_port_free() {
  if (echo > "/dev/tcp/${HOST}/${PORT}") >/dev/null 2>&1; then
    log_error "port already in use: ${HOST}:${PORT}"
    exit 1
  fi
}

# ── Build ─────────────────────────────────────────────────

setup_build_cache() {
  export CGO_ENABLED="${CGO_ENABLED:-0}"
  export GOCACHE="${GOCACHE:-"${ROOT_DIR}/.cache/go-build"}"
}

build_moonbridge() {
  local output="${1:?build_moonbridge: output path required}"
  log "Building Moon Bridge"
  (
    cd "$ROOT_DIR"
    go build -o "$output" ./cmd/moonbridge
  ) 2>&1 | tee -a "$LOG_FILE"
}

# ── Server management ─────────────────────────────────────

SERVER_PID=""

wait_for_server() {
  local deadline=$((SECONDS + 30))
  while (( SECONDS < deadline )); do
    if ! kill -0 "$SERVER_PID" >/dev/null 2>&1; then
      log_error "Moon Bridge exited before it became ready on ${BASE_ADDR}"
      return 1
    fi
    if (echo > "/dev/tcp/${HOST}/${PORT}") >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  log_error "Moon Bridge did not start on ${BASE_ADDR}"
  return 1
}

cleanup_server() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" >/dev/null 2>&1; then
    log "Stopping Moon Bridge"
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
    log "Moon Bridge stopped"
  fi
}

register_server_cleanup() {
  trap cleanup_server EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  trap 'exit 129' HUP
}

start_server_background() {
  log "Starting Moon Bridge on ${BASE_ADDR}"
  log "Moon Bridge log: ${LOG_FILE}"
  (
    cd "$ROOT_DIR"
    "$SERVER_BIN" --config "$CONFIG_FILE"
  ) >> "$LOG_FILE" 2>&1 &
  SERVER_PID="$!"
  wait_for_server || return $?
}

extract_server_metadata() {
  MODE="$("$SERVER_BIN" --config "$CONFIG_FILE" --print-mode 2>>"$LOG_FILE")"
  ADDR="$("$SERVER_BIN" --config "$CONFIG_FILE" --print-addr 2>>"$LOG_FILE")"
  parse_addr "$ADDR"
}

# ── Env file (for split-start workflows) ───────────────────

load_env_file() {
  local env_file="${1:?load_env_file: env file path required}"
  if [[ -f "$env_file" ]]; then
    source "$env_file"
  else
    log_error "moonbridge env file not found at ${env_file}"
    exit 1
  fi
  if [[ -z "${MOONBRIDGE_ADDR:-}" ]]; then
    log_error "moonbridge not configured in ${env_file}"
    exit 1
  fi
  parse_addr "$MOONBRIDGE_ADDR"
}

verify_moonbridge_alive() {
  if ! (echo > "/dev/tcp/${HOST}/${PORT}") >/dev/null 2>&1; then
    log_error "Moon Bridge not reachable on ${HOST}:${PORT}"
    exit 1
  fi
}

validate_mode() {
  local current="${1:?validate_mode: current mode required}"
  shift
  local allowed=("$@")
  if [[ ${#allowed[@]} -eq 0 ]]; then
    return 0
  fi
  for mode in "${allowed[@]}"; do
    [[ "$current" == "$mode" ]] && return 0
  done
  log_error "moonbridge mode must be one of: ${allowed[*]}, got: ${current}"
  exit 1
}

# ── Codex config generation ───────────────────────────────

generate_codex_config() {
  local model_alias="${1:?generate_codex_config: model alias required}"
  local codex_home="${2:?generate_codex_config: codex home required}"
  local base_url="${3:-http://${BASE_ADDR}/v1}"

  "$SERVER_BIN" \
    --config "$CONFIG_FILE" \
    --print-codex-config "$model_alias" \
    --codex-base-url "$base_url" \
    --codex-home "$codex_home" \
    2>>"$LOG_FILE" \
    > "${codex_home}/config.toml"
}

# Copies [tui].status_line from a global Codex config to a target config.toml.
append_codex_status_line() {
  local target_config="${1:?append_codex_status_line: target config required}"
  local global_config="${2:-${HOME}/.codex/config.toml}"

  if [[ ! -f "$global_config" ]]; then
    log "No global Codex config found at ${global_config}; status_line not copied"
    return
  fi

  local status_line
  status_line="$(
    awk '
      /^\[/ { in_tui = ($0 == "[tui]"); capture = 0 }
      in_tui && /^[[:space:]]*status_line[[:space:]]*=/ { capture = 1; print; if ($0 ~ /\]/) capture = 0; next }
      in_tui && capture { print; if ($0 ~ /\]/) capture = 0; next }
    ' "$global_config"
  )"

  if [[ -z "$status_line" ]]; then
    log "No [tui].status_line found in ${global_config}; status_line not copied"
    return
  fi

  {
    printf '\n[tui]\n'
    printf '%s\n' "$status_line"
  } >> "$target_config"
  log "Copied Codex status_line from ${global_config}"
}

# ── Claude Code config generation ─────────────────────────

prepare_claude_settings() {
  local target_settings="${1:?target settings.json path required}"
  local env_file="${2:?target env file path required}"
  local base_url="${3:?base URL required}"
  local global_settings="${4:-${HOME}/.claude/settings.json}"
  local model="${5:-}"

  python3 - "$global_settings" "$target_settings" "$env_file" "$base_url" "$model" <<'PYEOF'
import json, os, shlex, sys
from pathlib import Path

source_path = Path(sys.argv[1])
target_path = Path(sys.argv[2])
env_path = Path(sys.argv[3])
base_url = sys.argv[4]
model = sys.argv[5]
model_placeholders = {"", "provider-model-name", "replace-with-provider-model-name", "replace-with-real-model-name"}

settings = {}
loaded_source = False
if source_path.exists():
    try:
        settings = json.loads(source_path.read_text())
        settings = settings if isinstance(settings, dict) else {}
        loaded_source = True
    except json.JSONDecodeError as exc:
        raise SystemExit(f"failed to parse {source_path}: {exc}") from exc

env = settings.get("env")
env = {str(k): str(v) for k, v in env.items()} if isinstance(env, dict) else {}
env["ANTHROPIC_BASE_URL"] = base_url
env["ANTHROPIC_AUTH_TOKEN"] = "moonbridge-proxy-placeholder"
env.pop("ANTHROPIC_API_KEY", None)
env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
settings["includeCoAuthoredBy"] = False

if model and model not in model_placeholders:
    env["ANTHROPIC_MODEL"] = model
    env["ANTHROPIC_CUSTOM_MODEL_OPTION"] = model
    settings["model"] = model
elif "model" not in settings:
    env.pop("ANTHROPIC_MODEL", None)
    env.pop("ANTHROPIC_CUSTOM_MODEL_OPTION", None)

settings["env"] = env
target_path.parent.mkdir(parents=True, exist_ok=True)
target_path.write_text(json.dumps(settings, ensure_ascii=False, indent=2) + "\n")
os.chmod(target_path, 0o600)

lines = []
for key in ["ANTHROPIC_AUTH_TOKEN","ANTHROPIC_API_KEY","ANTHROPIC_BASE_URL","ANTHROPIC_MODEL","ANTHROPIC_CUSTOM_MODEL_OPTION","CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"]:
    value = env.get(key)
    if value is not None:
        lines.append(f"export {key}={shlex.quote(value)}")
effective_model = env.get("ANTHROPIC_MODEL") or settings.get("model") or ""
if effective_model:
    lines.append(f"export MOONBRIDGE_EFFECTIVE_CLAUDE_MODEL={shlex.quote(str(effective_model))}")
env_path.write_text("\n".join(lines) + "\n")
os.chmod(env_path, 0o600)

if loaded_source:
    print(f"Seeded Claude Code settings from {source_path} with placeholder ANTHROPIC_AUTH_TOKEN")
else:
    print(f"No global Claude Code settings found at {source_path}; using placeholder AUTH_TOKEN")
if model and model in model_placeholders:
    print(f"Ignoring placeholder developer.proxy.anthropic.model={model!r}; using default model")
PYEOF

  # shellcheck source=/dev/null
  source "$env_file"
}
