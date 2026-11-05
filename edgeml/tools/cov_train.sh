#!/usr/bin/env bash
# Coverage retrain: multi-class YOLO11 fine-tunes from COCO weights on the dataset built by cov_build_detector_dataset.py.
# Keeps the 80-class COCO head (no single_cls); maritime vessels are extra COCO `boat` examples, YOLO11x pseudo-labels
# keep the other classes alive on maritime frames, COCO val2017 is replayed. best.pt is chosen on YOLO val (dev only).
# Usage: bash edgeml/tools/cov_train.sh ~/data/yolo_cov/data.yaml ~/runs/cov
set -euo pipefail
DATA=${1:-$HOME/data/yolo_cov/data.yaml}
OUT=${2:-$HOME/runs/cov}
EPOCHS=${EPOCHS:-15}
CACHE=${CACHE:-disk}
run() { # name weights imgsz batch lr0 freeze
  local name=$1 w=$2 sz=$3 bs=$4 lr=$5 fz=$6
  [ -f "$OUT/$name/weights/best.pt" ] && { echo "skip $name"; return; }
  yolo detect train model="$w" data="$DATA" imgsz="$sz" epochs="$EPOCHS" batch="$bs" workers=12 cache="$CACHE" \
    optimizer=SGD lr0="$lr" warmup_epochs=1 freeze="$fz" patience=6 close_mosaic=4 \
    project="$OUT" name="$name" exist_ok=True plots=True seed=0 deterministic=False 2>&1 | tail -5
}
run yolo11n-cov-640 yolo11n.pt 640 32 0.002 0
run yolo11n-cov-640-fz10 yolo11n.pt 640 32 0.002 10
echo ALL_DONE
