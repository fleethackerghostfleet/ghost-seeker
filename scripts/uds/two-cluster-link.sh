#!/usr/bin/env bash
set -euo pipefail
export PATH="$HOME/sdk/go/bin:$HOME/.local/bin:$PATH"
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
STATE=${STATE:-"$HOME/.local/share/uds-two-cluster"}
LOG_DIR=${LOG_DIR:-"$ROOT/docs/reports/uds-validation/logs"}
SOCKET="$STATE/link.sock"
BIN="$STATE/link-shaper"
PID_FILE="$STATE/link.pid"
mkdir -p "$STATE" "$LOG_DIR"
owned() {
  [[ -f "$PID_FILE" ]] && [[ "$(readlink "/proc/$(<"$PID_FILE")/exe" 2>/dev/null || sudo -n readlink "/proc/$(<"$PID_FILE")/exe" 2>/dev/null)" == "$BIN" ]]
}
case "${1:-status}" in
  start)
    if owned; then echo "Shaper already running: $(<"$PID_FILE")"; exit 0; fi
    case "${LINK_PORT:-443}" in 8105|8106|8109) echo 'Protected port' >&2; exit 1;; esac
    if [[ -S "$SOCKET" ]]; then echo "Control socket already exists: $SOCKET" >&2; exit 1; fi
    go build -o "$BIN" "$ROOT/scripts/uds/link-shaper"
    if (( ${LINK_PORT:-443} < 1024 )); then sudo -n setcap cap_net_bind_service=+ep "$BIN"; fi
    nohup "$BIN" -listen "${LINK_IP:-172.19.0.1}:${LINK_PORT:-443}" \
      -target "${LINK_TARGET:-127.0.0.1:9443}" -socket "$SOCKET" -bps "${BPS:-16000000}" \
      >> "$LOG_DIR/link-shaper.log" 2>&1 </dev/null &
    printf '%s\n' "$!" > "$PID_FILE"
    echo "Started shaper PID $(<"$PID_FILE")"
    ;;
  stop)
    if ! owned; then echo 'No owned shaper process; refusing to signal anything' >&2; exit 1; fi
    kill -TERM "$(<"$PID_FILE")"
    ;;
  set-rate)
    [[ "${2:-}" =~ ^[0-9]+$ ]] || { echo 'Usage: two-cluster-link.sh set-rate <bits/s>' >&2; exit 1; }
    owned || { echo 'No owned shaper process' >&2; exit 1; }
    curl -fsS --unix-socket "$SOCKET" -X POST "http://localhost/rate?bps=$2"
    ;;
  status)
    owned || { echo 'No owned shaper process' >&2; exit 1; }
    curl -fsS --unix-socket "$SOCKET" http://localhost/rate
    ;;
  *) echo 'Usage: two-cluster-link.sh start|stop|status|set-rate <bits/s>' >&2; exit 1;;
esac
