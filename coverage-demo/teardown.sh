#!/usr/bin/env bash
# Stop both servers and kill any lingering watchers. With WIPE=1, also remove
# the sandbox (homes/servers/human2). The events file and built binary are kept.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

stop_server "$SERVERS_DIR/main"
stop_server "$SERVERS_DIR/move"
pkill -f "$BIN watch" 2>/dev/null || true

if [ "${WIPE:-0}" = "1" ]; then
  rm -rf "$HOMES_DIR" "$SERVERS_DIR" "$RUN_DIR/human2"
fi
