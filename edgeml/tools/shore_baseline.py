#!/usr/bin/env python3
"""Run RF-DETR 1.3.0 COCO Small on frozen VALID only, never locked test.

Uses existing detector wrappers: native full frame, or 640/20% tiles + full,
class-agnostic .5 NMS, max 300. Latency includes preprocessing/merge and GPU
synchronization, excludes disk IO; measured on decoded 1920x1080 frames.
"""
from __future__ import annotations

import argparse
import hashlib
import importlib.metadata
import json
import subprocess
import sys
import time
from pathlib import Path

import cv2
import numpy as np
import torch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
from edgeml.detectors.bakeoff import RFDetector
from edgeml.detectors.labels import agnostic
from edgeml.detectors.tiled import TiledDetector


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--valid", type=Path, required=True)
    ap.add_argument("--output", type=Path, required=True)
    ap.add_argument("--mode", choices=["native", "tiled"], required=True)
    ap.add_argument("--manifests", type=Path, required=True)
    args = ap.parse_args()
    coco_path = args.valid / "_annotations.coco.json"
    coco = json.loads(coco_path.read_text())
    if coco.get("info", {}).get("split") != "valid":
        ap.error("Baseline inference is restricted to the valid set")
    manifest = json.loads((args.manifests / "valid.json").read_text())
    if hashlib.sha256(coco_path.read_bytes()).hexdigest() != manifest["coco_sha256"]:
        raise ValueError("Frozen COCO hash mismatch")
    hashes = {}
    for shard in manifest["image_shards"]:
        raw = (args.manifests / shard["file"]).read_bytes()
        if hashlib.sha256(raw).hexdigest() != shard["sha256"]:
            raise ValueError("Frozen image manifest hash mismatch")
        hashes.update({row[0]: row[1] for row in json.loads(raw)["images"]})
    if {im["file_name"] for im in coco["images"]} != set(hashes):
        raise ValueError("Frozen image membership mismatch")
    assert importlib.metadata.version("rfdetr") == "1.3.0"
    if not torch.cuda.is_available():
        raise RuntimeError("Use the assigned A100 for baseline inference")
    args.output.mkdir(parents=True, exist_ok=False)
    torch.set_num_threads(4)
    base = RFDetector("RFDETRSmall", "rfdetr-small", device="cuda", score_thr=0.001)
    assert base.imgsz == 512, base.imgsz
    detector = base if args.mode == "native" else TiledDetector(base, tile=640, overlap=0.2, include_full=True, merge_iou=0.5, max_det=300)
    config = {"model": "RFDETRSmall", "rfdetr": "1.3.0", "weights": "COCO pretrained", "resolution": 512,
              "mode": args.mode, "candidate_score_floor": 0.001, "tile": 640 if args.mode == "tiled" else None,
              "overlap": 0.2 if args.mode == "tiled" else None, "include_full": True,
              "merge_nms_iou": 0.5 if args.mode == "tiled" else None, "max_det": 300 if args.mode == "tiled" else 300}
    # Warm both the native and tiled execution paths without involving any test data.
    first = cv2.imread(str(args.valid / coco["images"][0]["file_name"]))
    for _ in range(5):
        detector.detect(first)
    predictions, timings = [], []
    for i, im in enumerate(coco["images"]):
        raw = (args.valid / im["file_name"]).read_bytes()
        if hashlib.sha256(raw).hexdigest() != hashes[im["file_name"]]:
            raise ValueError(f"Frozen image hash mismatch: {im['file_name']}")
        image = cv2.imdecode(np.frombuffer(raw, dtype=np.uint8), cv2.IMREAD_COLOR)
        if image is None:
            raise ValueError(f"Missing image: {im['file_name']}")
        torch.cuda.synchronize()
        t0 = time.perf_counter()
        ds = detector.detect(image)
        torch.cuda.synchronize()
        elapsed = (time.perf_counter() - t0) * 1000
        if image.shape[:2] == (1080, 1920):
            timings.append(elapsed)
        predictions.extend({"image_id": im["id"], "category_id": 1, "bbox": list(map(float, d.box)), "score": float(d.score)}
                           for d in ds if agnostic(d.label) == "vessel" and min(d.box[2:]) > 0)
        if (i + 1) % 100 == 0:
            print(f"{args.mode}: {i + 1}/{len(coco['images'])}", flush=True)
    pred_path = args.output / "predictions.json"
    pred_path.write_text(json.dumps({"metadata": config, "detections": predictions}))
    latency = {"n_1080p": len(timings), "p50_ms": float(np.percentile(timings, 50)) if timings else None,
               "p95_ms": float(np.percentile(timings, 95)) if timings else None,
               "gpu": torch.cuda.get_device_name(0), "torch": torch.__version__,
               "valid_coco_sha256": hashlib.sha256(coco_path.read_bytes()).hexdigest(),
               "scope": "synchronized decode-excluded preprocessing+inference+NMS; five warmup frames excluded"}
    (args.output / "latency.json").write_text(json.dumps(latency, indent=2))
    subprocess.run([sys.executable, str(Path(__file__).with_name("shore_eval.py")), "--coco", str(coco_path),
                    "--predictions", str(pred_path), "--thresholds", str(args.output / "threshold.json"), "--fit-valid",
                    "--output", str(args.output / "metrics.json")], check=True)
    print(json.dumps(latency), flush=True)


if __name__ == "__main__":
    main()
