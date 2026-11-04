#!/usr/bin/env sh
# Headless three-way comparison (naive vs hash vs sightings) over the link
# presets, in virtual time. Writes out/compare.json and out/compare.md.
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
"$ROOT/bin/sim" -scenario "$HERE/scenario.json" -dedup "$DEDUP" -bps "$BPS" \
  -strategies prioritized -duration "$DURATION" -sample "$SAMPLE" \
  -out "$OUT/compare.json" -md "$OUT/compare.md"
echo "wrote $OUT/compare.json and $OUT/compare.md"
