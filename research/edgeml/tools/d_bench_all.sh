#!/usr/bin/env bash
# Workstream D CPU leaderboard on the laptop-proxy VM. 4 threads pinned with taskset + runtime settings.
#   bash tools/d_bench_all.sh <export_root> <frames_dir> <out_dir> [duration_s=60] [cpus=0-3] [threads=4]
set -u
EXPORT=$1; FRAMES=$2; OUT=$3; DUR=${4:-60}; CPUS=${5:-0-3}; THREADS=${6:-4}
PY=${PY:-python}
mkdir -p "$OUT"
for d in "$EXPORT"/*/; do
  name=$(basename "$d")
  if [ -f "$d/model.onnx" ]; then
    taskset -c "$CPUS" $PY -m edgeml_research.runtime.bench --model "$d/model.onnx" --engine ort --frames "$FRAMES" --threads "$THREADS" --duration "$DUR" --out "$OUT/${name}-ort-fp32.json" 2>>"$OUT/err.log" | tail -1 | cut -c1-160
  fi
  for prec in fp32 fp16 int8; do
    [ -f "$d/$prec/model.xml" ] || continue
    taskset -c "$CPUS" $PY -m edgeml_research.runtime.bench --model "$d/$prec/model.xml" --engine openvino --frames "$FRAMES" --threads "$THREADS" --duration "$DUR" --out "$OUT/${name}-ov-${prec}.json" 2>>"$OUT/err.log" | tail -1 | cut -c1-160
  done
done
cat "$OUT"/*.json | $PY -c 'import sys,json; [print(json.dumps(json.loads(l))) for l in sys.stdin if l.strip()]' > "$OUT/leaderboard.jsonl" 2>/dev/null || true
