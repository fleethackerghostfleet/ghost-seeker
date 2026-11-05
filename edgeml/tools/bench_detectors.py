#!/usr/bin/env python
"""Workstream B benchmark runner: zero-shot detectors over catalog eval sets.

Example (dev sweep):
  python edgeml/tools/bench_detectors.py --run-id 20261104-2100-b-dev640 --split dev \
      --sets seaships7000:5,singapore_maritime:2,smd_vis_onboard_sample:1 \
      --models yolov8n,yolov8s,yolo11n,yolo11s,rtdetrv2-r18,dfine-s,yolox-tiny,nanodet-plus,yolov8s-world,owlv2-b16,gdino-t,argus-yolov8s \
      --imgsz 640 --out ~/runs --gcs gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs

Outputs (CONTRACTS §4):  <out>/<run_id>/config.json, <out>/<run_id>/<model>/<set>__<split>/dets.jsonl,
                         <out>/<run_id>/metrics.json   (+ optional `gcloud storage rsync` to --gcs)
"""
from __future__ import annotations

import argparse
import json
import os
import platform
import subprocess
import sys
import time
from pathlib import Path

import cv2
import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
REPO = Path(__file__).resolve().parents[2]

from edgeml.data.catalog import eval_set
from edgeml.detectors import EXCLUDE, EXCLUDE_DEV, HorizonBandDetector, TiledDetector, build
from edgeml.eval.detection import best_threshold, evaluate
from edgeml.types import Detection


def git_sha() -> str:
    try:
        return subprocess.check_output(["git", "rev-parse", "--short", "HEAD"], text=True).strip()
    except Exception:
        f = REPO / "GIT_SHA"
        return f.read_text().strip() if f.exists() else "unknown"


def hardware() -> dict:
    hw = {"host": platform.node(), "cpu": platform.processor() or platform.machine(), "python": platform.python_version()}
    try:
        import torch

        hw["torch"] = torch.__version__
        if torch.cuda.is_available():
            hw["gpu"] = torch.cuda.get_device_name(0)
    except Exception:
        pass
    return hw


def run_model_on_set(det, images, dets_path: Path, horizon_from_gt: bool = False):
    """Run `det` over images, write dets.jsonl, return (dets_by_image, latency stats)."""
    dets_by_image: dict[str, list[Detection]] = {}
    lat = []
    dets_path.parent.mkdir(parents=True, exist_ok=True)
    with open(dets_path, "w") as f:
        for n, im in enumerate(images):
            img = cv2.imread(im.local_path)
            if img is None:
                print(f"  !! missing {im.local_path}", file=sys.stderr)
                continue
            if horizon_from_gt and isinstance(det, HorizonBandDetector):
                det.horizon_override = im.horizon["Y"] if im.horizon else None
            ds = det.detect(img)
            lat.append(det.last_ms)
            dets_by_image[im.image_id] = ds
            f.writelines(json.dumps({"image_id": im.image_id, "box": [round(v, 1) for v in d.box],
                                    "score": round(d.score, 4), "label": d.label, "label_id": d.label_id}) + "\n" for d in ds)
            if n % 200 == 0:
                print(f"  {n}/{len(images)}  {np.median(lat[-200:]):.1f} ms", flush=True)
    lat = np.array(lat[5:]) if len(lat) > 10 else np.array(lat)
    stats = {"ms_p50": float(np.percentile(lat, 50)), "ms_p95": float(np.percentile(lat, 95)),
             "ms_mean": float(lat.mean()), "n": len(lat)}
    if hasattr(det, "n_calls"):
        stats["model_calls_per_frame"] = det.n_calls
    return dets_by_image, stats


def load_dets(path: Path) -> dict[str, list[Detection]]:
    out: dict[str, list[Detection]] = {}
    with open(path) as f:
        for line in f:
            r = json.loads(line)
            out.setdefault(r["image_id"], []).append(Detection(box=r["box"], score=r["score"], label=r["label"],
                                                                label_id=r.get("label_id")))
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--run-id", required=True)
    ap.add_argument("--split", choices=["dev", "test"], default="dev")
    ap.add_argument("--sets", default="seaships7000:5,singapore_maritime:2,smd_vis_onboard_sample:1",
                    help="dataset_id:stride,... (stride only honoured on dev)")
    ap.add_argument("--models", required=True)
    ap.add_argument("--imgsz", type=int, default=640)
    ap.add_argument("--score-thr", type=float, default=0.05)
    ap.add_argument("--tiling", choices=["none", "sahi", "hband"], default="none")
    ap.add_argument("--tile", type=int, default=640)
    ap.add_argument("--band-frac", type=float, default=0.2, help="hband: band height as a fraction of H (0.2 = +/-10%%)")
    ap.add_argument("--n-tiles", type=int, default=3, help="hband: tiles across the band (0 = square tiles of band height)")
    ap.add_argument("--overlap", type=float, default=0.2)
    ap.add_argument("--no-full", action="store_true", help="tiling without the full-frame pass")
    ap.add_argument("--horizon-gt", action="store_true", help="hband: use catalog GT horizon when present")
    ap.add_argument("--taus", default=None, help="json {model: tau} chosen on dev; required for --split test")
    ap.add_argument("--catalog", default=os.path.expanduser("~/data/catalog"))
    ap.add_argument("--data-root", default=os.path.expanduser("~/data"))
    ap.add_argument("--out", default=os.path.expanduser("~/runs"))
    ap.add_argument("--gcs", default=None)
    ap.add_argument("--device", default=None)
    ap.add_argument("--reuse", action="store_true", help="reuse existing dets.jsonl if present")
    args = ap.parse_args()

    run_dir = Path(args.out) / args.run_id
    run_dir.mkdir(parents=True, exist_ok=True)
    sets = []
    for s in args.sets.split(","):
        ds, _, stride = s.partition(":")
        sets.append((ds, int(stride or 1)))
    fixed_taus = json.loads(Path(args.taus).read_text()) if args.taus else {}
    if args.split == "test":
        assert fixed_taus, "test runs need --taus chosen on dev"

    config = {"run_id": args.run_id, "workstream": "b", "split": args.split, "imgsz": args.imgsz,
              "score_thr": args.score_thr, "tiling": args.tiling, "tile": args.tile, "band_frac": args.band_frac,
              "include_full": not args.no_full, "models": args.models.split(","), "sets": sets,
              "git_sha": git_sha(), "hardware": hardware(), "started": time.strftime("%Y-%m-%dT%H:%M:%S")}
    (run_dir / "config.json").write_text(json.dumps(config, indent=2))

    metrics_path = run_dir / "metrics.json"
    metrics = json.loads(metrics_path.read_text()) if metrics_path.exists() else {}
    images_cache = {}
    for mname in config["models"]:
        print(f"== {mname}", flush=True)
        try:
            _run_model(mname, sets, args, run_dir, images_cache, metrics, metrics_path, fixed_taus)
        except Exception as e:  # keep the sweep going; the failure is recorded for the report
            import traceback

            traceback.print_exc()
            metrics.setdefault(mname, {})["error"] = f"{type(e).__name__}: {e}"
            metrics_path.write_text(json.dumps(metrics, indent=1))
        try:
            import torch

            torch.cuda.empty_cache()
        except Exception:
            pass
    if args.gcs:
        subprocess.run(["gcloud", "storage", "rsync", "-r", str(run_dir), f"{args.gcs.rstrip('/')}/{args.run_id}"], check=False)
    print("done", run_dir)


def _run_model(mname, sets, args, run_dir, images_cache, metrics, metrics_path, fixed_taus):
        det = None
        for ds, stride in sets:
            if ds in EXCLUDE.get(mname, set()) or (args.split == "dev" and ds in EXCLUDE_DEV.get(mname, set())):
                print(f"  skip {ds} ({args.split}): {mname} was trained on it")
                continue
            key = (ds, stride)
            if key not in images_cache:
                images_cache[key] = eval_set(ds, args.split, args.catalog, args.data_root, stride=stride if args.split == "dev" else 1)
            images = images_cache[key]
            tag = f"{ds}__{args.split}"
            dets_path = run_dir / mname / tag / "dets.jsonl"
            if args.reuse and dets_path.exists():
                dets_by_image = load_dets(dets_path)
                stats = metrics.get(mname, {}).get(tag, {}).get("latency", {})
                info = metrics.get(mname, {}).get(tag, {}).get("model", {})
            else:
                if det is None:
                    base = build(mname, imgsz=args.imgsz, device=args.device, score_thr=args.score_thr)
                    if args.tiling == "sahi":
                        det = TiledDetector(base, tile=args.tile, overlap=args.overlap, include_full=not args.no_full)
                    elif args.tiling == "hband":
                        det = HorizonBandDetector(base, band_frac=args.band_frac, n_tiles=args.n_tiles, overlap=args.overlap,
                                                  include_full=not args.no_full)
                    else:
                        det = base
                print(f"  {tag}: {len(images)} images", flush=True)
                dets_by_image, stats = run_model_on_set(det, images, dets_path, horizon_from_gt=args.horizon_gt)
                info = det.info()
            entry = {"model": info, "latency": stats, "n_images": len(images)}
            if args.split == "dev":
                bt = best_threshold(images, dets_by_image, "vessel")
                tau = bt["tau"]
                entry["dev_threshold"] = bt
            else:
                tau = fixed_taus[mname] if isinstance(fixed_taus[mname], (int, float)) else fixed_taus[mname][ds]
            entry["tau"] = tau
            entry["vessel"] = evaluate(images, dets_by_image, tau, "vessel")
            entry["allobj"] = evaluate(images, dets_by_image, tau, "allobj")
            metrics.setdefault(mname, {})[tag] = entry
            v = entry["vessel"]
            print(f"  {tag}: AP50={v['mAP50']:.3f} AP={v['mAP50-95']:.3f} R@tau={v['at_tau']['recall']:.3f} "
                  f"P@tau={v['at_tau']['precision']:.3f} tau={tau} p50={stats.get('ms_p50', float('nan')):.1f}ms "
                  f"R_small={v['at_tau']['recall_by_bucket']['small']:.2f} R_med={v['at_tau']['recall_by_bucket']['medium']:.2f}", flush=True)
            metrics_path.write_text(json.dumps(metrics, indent=1))


if __name__ == "__main__":
    main()
