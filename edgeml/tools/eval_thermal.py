"""Evaluate frozen thermal defaults on test/dev only; never tune on real IR.

Run from the repository root with the edgeml venv. Results/cache live outside the
source tree unless --output points at a report assets directory.
"""
from __future__ import annotations

import argparse
from collections import Counter, defaultdict
from dataclasses import asdict
import hashlib
import importlib.metadata
import json
import math
import os
from pathlib import Path
import platform
import time

import cv2
import numpy as np
import yaml

from edgeml.detectors.anomaly import _iou
from edgeml.detectors.thermal import ThermalFusionDetector, ThermalHotspotProposer, is_thermal
from edgeml.detectors.tiled import estimate_horizon_y
from edgeml.runtime.detectors import MARITIME_KEEP, load_detector
from edgeml.runtime.threads import pin_threads
from edgeml.types import Detection


def center_hit(a, b):
    def inside(box, point):
        return box[0] <= point[0] <= box[0] + box[2] and box[1] <= point[1] <= box[1] + box[3]
    return inside(a, (b[0] + b[2] / 2, b[1] + b[3] / 2)) or inside(b, (a[0] + a[2] / 2, a[1] + a[3] / 2))


def greedy_matches(predictions, gt, criterion="center"):
    """Score-descending, one-to-one; highest IoU eligible GT wins, then GT index."""
    if criterion not in {"center", "iou30", "iou50"}:
        raise ValueError(criterion)
    used, pairs = set(), []
    for i in sorted(range(len(predictions)), key=lambda i: (-predictions[i].score, i)):
        box = predictions[i].box
        candidates = [j for j, target in enumerate(gt) if j not in used and
                      (center_hit(box, target) if criterion == "center" else _iou(box, target) + 1e-9 >= {"iou30": .3, "iou50": .5}[criterion])]
        if candidates:
            j = max(candidates, key=lambda j: (_iou(box, gt[j]), -j))
            pairs.append((i, j))
            used.add(j)
    return pairs


def size_bucket(box):
    size = math.sqrt(max(0, box[2] * box[3]))
    return "small" if size < 32 else "medium" if size < 96 else "large"


def summarize(frames, criterion):
    counts = {key: Counter() for key in ("all", "small", "medium", "large")}
    for predictions, gt in frames:
        pairs = greedy_matches(predictions, gt, criterion)
        pi, gi = {i for i, _ in pairs}, {j for _, j in pairs}
        counts["all"].update(tp=len(pairs), fp=len(predictions) - len(pairs), fn=len(gt) - len(pairs))
        for _, j in pairs:
            counts[size_bucket(gt[j])]["tp"] += 1
        for i, pred in enumerate(predictions):
            if i not in pi:
                counts[size_bucket(pred.box)]["fp"] += 1
        for j, box in enumerate(gt):
            if j not in gi:
                counts[size_bucket(box)]["fn"] += 1
    result = {}
    for key, c in counts.items():
        tp, fp, fn = (c[k] for k in ("tp", "fp", "fn"))
        result[key] = dict(tp=tp, fp=fp, fn=fn, precision=tp / (tp + fp) if tp + fp else 0.,
                           recall=tp / (tp + fn) if tp + fn else 0., f1=2 * tp / (2 * tp + fp + fn) if 2 * tp + fp + fn else 0.)
    result["frames"] = len(frames)
    result["proposals_per_frame"] = sum(len(p) for p, _ in frames) / len(frames) if frames else 0.
    return result


def read_jsonl(path):
    return [json.loads(line) for line in Path(path).open() if line.strip()]


def image_path(root, row):
    prefix = "gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/"
    if not row["gcs_uri"].startswith(prefix):
        raise ValueError(row["gcs_uri"])
    return root / row["gcs_uri"][len(prefix):]


def read_image(path):
    image = cv2.imread(str(path))
    if image is None:
        raise FileNotFoundError(path)
    return image


def timed(fn, *args):
    start = time.perf_counter()
    value = fn(*args)
    return value, (time.perf_counter() - start) * 1000


def latency(values):
    return {"n": len(values), "mean": float(np.mean(values)), "p50": float(np.percentile(values, 50)),
            "p95": float(np.percentile(values, 95))} if values else {"n": 0}


class CachedBase:
    name = "cached-yolo"

    def __init__(self):
        self.dets = []

    def detect(self, _image):
        return self.dets


class YoloCache:
    def __init__(self, detector, path, identity):
        self.detector, self.path, self.identity = detector, path, identity
        if path.exists():
            saved = json.loads(path.read_text())
            if saved["identity"] != identity:
                raise ValueError("YOLO cache identity mismatch; use a new cache path")
            self.records = saved["records"]
        else:
            self.records = {}

    def detect(self, key, image):
        if key not in self.records:
            dets, ms = timed(self.detector.detect, image)
            self.records[key] = {"detections": [asdict(d) for d in dets], "ms": ms}
        row = self.records[key]
        return [Detection(**d) for d in row["detections"]], row["ms"]

    def save(self):
        self.path.write_text(json.dumps({"identity": self.identity, "records": self.records}))


def empty_crop(image, gt, rng):
    """One deterministic box-disjoint sea crop; never rescale or mine on outputs."""
    height, width = image.shape[:2]
    scale = min(1., 480 / width)
    gray = cv2.cvtColor(cv2.resize(image, (round(width * scale), round(height * scale))), cv2.COLOR_BGR2GRAY)
    horizon = ThermalHotspotProposer.horizon(gray)
    fallback = horizon is None
    horizon = estimate_horizon_y(image) if fallback else horizon / scale
    y0 = min(height, max(0, math.ceil(horizon)))
    cw, ch = min(320, width), min(240, height - y0)
    if cw < 64 or ch < 48:
        return None
    xs = sorted(set(range(0, width - cw + 1, max(1, cw // 2))) | {width - cw})
    ys = sorted(set(range(y0, height - ch + 1, max(1, ch // 2))) | {height - ch})
    candidates = [(x, y, cw, ch) for y in ys for x in xs]
    for index in rng.permutation(len(candidates)):
        x, y, w, h = candidates[index]
        if all(x + w <= b[0] - .1 * b[2] or x >= b[0] + 1.1 * b[2] or
               y + h <= b[1] - .1 * b[3] or y >= b[1] + 1.1 * b[3] for b in gt):
            return image[y:y + h, x:x + w].copy(), [x, y, w, h], fallback
    return None


def empty_metrics(records):
    n = len(records)
    mp = sum(r["megapixels"] for r in records)
    result = {"n": n, "megapixels": mp}
    for config in ("hotspot", "fusion", "live_reported"):
        count = sum(r[config] for r in records)
        result[config] = {"false_contacts": count, "per_crop": count / n if n else 0.,
                          "fraction_nonempty": sum(r[config] > 0 for r in records) / n if n else 0.,
                          "per_megapixel": count / mp if mp else 0.}
    return result


def engine_latency(args, ir_test, rgb_paths):
    from edgeml.sidecar.service import EdgemlEngine

    result = {}
    for dataset, paths in (("ir_test", [image_path(args.data_root, r) for r in ir_test]), ("rgb", rgb_paths)):
        engines = {False: EdgemlEngine(args.model, embed="none", threads=args.threads, engine="ort", thermal_fallback=False),
                   True: EdgemlEngine(args.model, embed="none", threads=args.threads, engine="ort", thermal_fallback={})}
        # Warm model execution, tracker initialization, night-lights and report encoding.
        warm = read_image(paths[0])
        for engine in engines.values():
            for _ in range(5):
                engine.analyze("warm", warm, None, [], False)
            engine.sources.clear()
        times, gate_times, thermal_times = defaultdict(list), [], []
        for i, path in enumerate(paths):
            image = read_image(path)
            # Alternate order to reduce systematic thermal-on/off drift.
            for enabled in ([False, True] if i % 2 == 0 else [True, False]):
                engine = engines[enabled]
                # Independent still images: do not fabricate an IR sequence or track GT.
                engine.sources.clear()
                _, ms = timed(engine.analyze, "still", image, None, [], False)
                times[str(enabled)].append(ms)
                if enabled:
                    thermal_times.append(engine.detector.last_timing_ms["thermal"])
            if dataset == "rgb":
                _, ms = timed(is_thermal, image)
                gate_times.append(ms)
            if (i + 1) % 100 == 0:
                print(f"engine {dataset}: {i + 1}/{len(paths)}", flush=True)
        result[dataset] = {"off": latency(times["False"]), "on": latency(times["True"]),
                           "paired_on_minus_off": latency([a - b for a, b in zip(times["True"], times["False"])]),
                           "thermal_stage_on": latency(thermal_times), "gate": latency(gate_times)}
    return result


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--data-root", type=Path, default=Path.home() / "data/clean_v3")
    ap.add_argument("--model", type=Path, default=Path.home() / "models/D/yolo11n-640/model.onnx")
    ap.add_argument("--output", type=Path, required=True)
    ap.add_argument("--cache", type=Path, required=True)
    ap.add_argument("--threads", type=int, default=4)
    ap.add_argument("--crop-count", type=int, default=200)
    ap.add_argument("--seed", type=int, default=73021)
    ap.add_argument("--skip-engine-latency", action="store_true")
    args = ap.parse_args()
    pin_threads(args.threads)
    args.output.mkdir(parents=True, exist_ok=True)
    args.cache.parent.mkdir(parents=True, exist_ok=True)
    catalog = args.data_root / "catalog"
    # Filter before any image is opened. No train frame is ever evaluated.
    rows = sorted((r for r in read_jsonl(catalog / "images/ir_maritime_vessels.jsonl")
                   if r["eval_split"] in {"test", "dev"}), key=lambda r: r["image_id"])
    ids = {r["image_id"] for r in rows}
    gt = defaultdict(list)
    for ann in read_jsonl(catalog / "annotations/ir_maritime_vessels.jsonl"):
        if ann["image_id"] in ids and len(ann.get("box", [])) == 4:
            gt[ann["image_id"]].append(ann["box"])
    identity = {"model_sha256": hashlib.sha256(args.model.read_bytes()).hexdigest(), "score_thr": .05,
                "keep_labels": sorted(MARITIME_KEEP), "threads": args.threads, "seed": args.seed,
                "catalog_sha256": hashlib.sha256((catalog / "images/ir_maritime_vessels.jsonl").read_bytes()).hexdigest()}
    yolo = load_detector(args.model, engine="ort", threads=args.threads, score_thr=.05)
    warm = read_image(image_path(args.data_root, rows[0]))
    for _ in range(5):
        yolo.detect(warm)
    cache = YoloCache(yolo, args.cache, identity)
    proposer, base35, base_live = ThermalHotspotProposer(), CachedBase(), CachedBase()
    fusion35, live = ThermalFusionDetector(base35), ThermalFusionDetector(base_live)
    frames = defaultdict(lambda: defaultdict(list))
    empty_frames, empty_boat_frames = defaultdict(list), defaultdict(list)
    timing = defaultdict(list)
    per_frame = []
    for i, row in enumerate(rows):
        image = read_image(image_path(args.data_root, row))
        key = row["image_id"] + ":" + row["sha256"]
        raw, yms = cache.detect(key, image)
        yd = [d for d in raw if d.score >= .35]
        props, pms = timed(proposer.detect, image)
        base35.dets, base_live.dets = yd, raw
        fused, _ = timed(fusion35.detect, image)
        live_dets, overhead = timed(live.detect, image)
        live_dets = [d for d in live_dets if d.score >= .35]
        boat_dets = [d for d in yd if d.label == "boat"]
        detections = {"hotspot": props, "yolo": yd, "yolo_boat": boat_dets, "fusion": fused, "live_reported": live_dets}
        for config, ds in detections.items():
            frames[row["eval_split"]][config].append((ds, gt[row["image_id"]]))
            if not yd:
                empty_frames[row["eval_split"] + "/" + config].append((ds, gt[row["image_id"]]))
            if not boat_dets:
                empty_boat_frames[row["eval_split"] + "/" + config].append((ds, gt[row["image_id"]]))
        if row["eval_split"] == "test":
            timing["proposer"].append(pms)
            timing["yolo_cached_inference"].append(yms)
            timing["fusion_reconstructed"].append(yms + overhead)
        per_frame.append({"image_id": row["image_id"], "split": row["eval_split"], "gt": len(gt[row["image_id"]]),
                          "detections": {k: [asdict(d) for d in v] for k, v in detections.items()}})
        if (i + 1) % 100 == 0:
            cache.save()
            print(f"metrics: {i + 1}/{len(rows)}", flush=True)
    cache.save()
    test = [r for r in rows if r["eval_split"] == "test"]
    rng = np.random.default_rng(args.seed)
    crop_records = []
    eligible = fallback_count = 0
    for i in rng.permutation(len(test)):
        row = test[i]
        image = read_image(image_path(args.data_root, row))
        sample = empty_crop(image, gt[row["image_id"]], rng)
        if sample is None:
            continue
        eligible += 1
        if len(crop_records) >= args.crop_count:
            continue
        crop, box, fallback = sample
        fallback_count += fallback
        raw, _ = cache.detect(f"crop:{args.seed}:{row['image_id']}:{box}", crop)
        base35.dets, base_live.dets = [d for d in raw if d.score >= .35], raw
        crop_records.append({"image_id": row["image_id"], "box": box, "horizon_fallback": fallback,
                             "megapixels": crop.shape[0] * crop.shape[1] / 1e6,
                             "hotspot": len(proposer.detect(crop)), "fusion": len(fusion35.detect(crop)),
                             "live_reported": sum(d.score >= .35 for d in live.detect(crop))})
    cache.save()
    smd_gt = {a["image_id"] for a in read_jsonl(catalog / "annotations/smd_vis_onboard_sample.jsonl") if len(a.get("box", [])) == 4}
    smd_empty = [r for r in read_jsonl(catalog / "images/smd_vis_onboard_sample.jsonl") if r["eval_split"] == "dev" and r["image_id"] not in smd_gt]
    smd_records = []
    for row in smd_empty:
        image = cv2.cvtColor(cv2.cvtColor(read_image(image_path(args.data_root, row)), cv2.COLOR_BGR2GRAY), cv2.COLOR_GRAY2BGR)
        raw, _ = cache.detect("smd-gray:" + row["image_id"], image)
        base35.dets, base_live.dets = [d for d in raw if d.score >= .35], raw
        smd_records.append({"megapixels": image.shape[0] * image.shape[1] / 1e6, "hotspot": len(proposer.detect(image)),
                            "fusion": len(fusion35.detect(image)), "live_reported": sum(d.score >= .35 for d in live.detect(image))})
    cache.save()
    # Independent RGB benchmark: FVessel dev clips, sorted paths every 20th frame.
    set_dir = args.data_root / "edgeml/eval/fvessel_clip10"
    splits = yaml.safe_load((Path(__file__).resolve().parents[1] / "configs/splits.yaml").read_text())
    rgb_paths = sorted(f for name in splits["dev"]["fvessel_clip10"] for f in (set_dir / name / "img1").glob("*.jpg"))[::20]
    rgb_gate_positives = 0
    for path in rgb_paths:
        image = read_image(path)
        rgb_gate_positives += is_thermal(image)
        for _ in range(10):
            _, ms = timed(is_thermal, image)
            timing["rgb_gate"].append(ms)
    criteria = ("center", "iou30", "iou50")
    results = {"identity": identity, "frozen_proposer": vars(proposer), "cpu": platform.processor(),
               "cpu_model": next((line.split(":", 1)[1].strip() for line in Path('/proc/cpuinfo').read_text().splitlines() if line.startswith('model name')), "unknown"),
               "affinity": sorted(os.sched_getaffinity(0)), "threads": args.threads,
               "versions": {name: importlib.metadata.version(name) for name in ("numpy", "onnxruntime", "boxmot")}, "opencv": cv2.__version__,
               "metrics": {split: {config: {c: summarize(fs, c) for c in criteria} for config, fs in configs.items()} for split, configs in frames.items()},
               "yolo_empty_subset": {key: {c: summarize(fs, c) for c in criteria} for key, fs in empty_frames.items()},
               "yolo_boat_empty_subset": {key: {c: summarize(fs, c) for c in criteria} for key, fs in empty_boat_frames.items()},
               "empty_ir": empty_metrics(crop_records) | {"eligible_source_frames": eligible, "horizon_fallback_crops": fallback_count},
               "smd_gray_tuning_negatives": empty_metrics(smd_records), "latency_ms": {k: latency(v) for k, v in timing.items()},
               "rgb_gate_positives": rgb_gate_positives,
               "rgb_benchmark_paths": [str(p.relative_to(args.data_root)) for p in rgb_paths]}
    if not args.skip_engine_latency:
        results["engine_latency_ms"] = engine_latency(args, test, rgb_paths)
    (args.output / "results.json").write_text(json.dumps(results, indent=2) + "\n")
    (args.output / "crops.json").write_text(json.dumps(crop_records, indent=2) + "\n")
    (args.output / "predictions.jsonl").write_text("".join(json.dumps(r) + "\n" for r in per_frame))
    print(json.dumps({"metrics": results["metrics"], "empty_ir": results["empty_ir"], "latency_ms": results["latency_ms"],
                      "engine_latency_ms": results.get("engine_latency_ms")}, indent=2))


if __name__ == "__main__":
    main()
