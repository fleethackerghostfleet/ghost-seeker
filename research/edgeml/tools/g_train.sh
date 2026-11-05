#!/usr/bin/env bash
# Workstream G: sequential YOLO11 fine-tunes on the DEV-only dataset built by g_build_dataset.py.
# Usage: bash research/edgeml/tools/g_train.sh ~/data/yolo_g/data.yaml ~/runs/g
set -euo pipefail
DATA=${1:-$HOME/data/yolo_g/data.yaml}
OUT=${2:-$HOME/runs/g}
EPOCHS=${EPOCHS:-30}
run() { # name weights imgsz batch
  local name=$1 w=$2 sz=$3 bs=$4
  [ -f "$OUT/$name/weights/best.pt" ] && { echo "skip $name"; return; }
  yolo detect train model="$w" data="$DATA" imgsz="$sz" epochs="$EPOCHS" batch="$bs" workers=8 cache=ram \
    patience=8 close_mosaic=5 single_cls=True project="$OUT" name="$name" exist_ok=True plots=True \
    seed=0 deterministic=False 2>&1 | tail -5
}
run yolo11n-640 yolo11n.pt 640 32
run yolo11n-960 yolo11n.pt 960 16
run yolo11s-640 yolo11s.pt 640 32
echo ALL_DONE
