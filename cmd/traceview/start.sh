#!/bin/bash
# traceview start script
# Usage: ./start.sh <sessions-root-dir> [port]

ROOT_DIR="${1:?Usage: $0 <sessions-root-dir> [port]}"
PORT="${2:-19999}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN="$SCRIPT_DIR/traceview"

if [ ! -f "$BIN" ]; then
    echo "ERROR: traceview binary not found at $BIN"
    exit 1
fi

# Kill existing traceview processes.
OLD_PID=$(pgrep -f "traceview" | grep -v $$ || true)
if [ -n "$OLD_PID" ]; then
    echo "Killing old traceview (pid: $OLD_PID)..."
    kill $OLD_PID 2>/dev/null
    sleep 1
    # Force kill if still running.
    kill -9 $OLD_PID 2>/dev/null || true
fi

# Start in background.
nohup "$BIN" -port "$PORT" "$ROOT_DIR" > traceview.log 2>&1 &
NEW_PID=$!
echo "traceview started (pid: $NEW_PID, port: $PORT, root: $ROOT_DIR)"
echo "Log: $SCRIPT_DIR/traceview.log"
