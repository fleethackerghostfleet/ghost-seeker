#!/usr/bin/env sh
# Headless comparison (naive vs hash vs sightings vs tomer) over the link
# presets, in virtual time. Writes out/compare.json and out/compare.md.
# The tomer row needs a sidecar running --prioritizer tomer|both: set INFER_URL
# to reuse one, otherwise a model-free one is started on :9093 when algorithm/
# (PR #29) is checked out and python can import edgeml; else the row is skipped.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
OUT=${OUT:-$HERE/out}
BPS=${BPS:-9600,64000,256000,2000000}
DEDUP=${DEDUP:-all}
DURATION=${DURATION:-6m}
SAMPLE=${SAMPLE:-1m}
mkdir -p "$OUT"
[ -x "$ROOT/bin/sim" ] || (cd "$ROOT" && go build -o bin/ ./cmd/sim)
"$HERE/download.sh" >/dev/null
PY=${PY:-python}
SIDECAR_PID=
if [ "$DEDUP" = all ] && [ -z "${INFER_URL:-}" ]; then
  if [ -f "$ROOT/algorithm/ghost_priority/policy.py" ] && "$PY" -c "import edgeml.sidecar.tomer" 2>/dev/null; then
    "$PY" -m edgeml.sidecar --prioritizer tomer --listen 127.0.0.1:9093 >"$OUT/tomer-sidecar.log" 2>&1 &
    SIDECAR_PID=$!
    trap 'kill $SIDECAR_PID 2>/dev/null' EXIT
    INFER_URL=http://127.0.0.1:9093; export INFER_URL
    sleep 3
  else
    echo "tomer row skipped: needs algorithm/ghost_priority and the edgeml venv (or INFER_URL to a sidecar in --prioritizer tomer|both)" >&2
    DEDUP=naive,hash,sightings
  fi
fi
"$ROOT/bin/sim" -scenario "$HERE/scenario.json" -dedup "$DEDUP" -bps "$BPS" \
  -strategies prioritized -duration "$DURATION" -sample "$SAMPLE" \
  -out "$OUT/compare.json" -md "$OUT/compare.md"
echo "wrote $OUT/compare.json and $OUT/compare.md"
