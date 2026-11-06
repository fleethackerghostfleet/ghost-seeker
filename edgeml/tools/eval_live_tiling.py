"""Eval-only, official-video-disjoint SeaDronesSee clean/v3 comparison. No fitting.

Prepare a manifest, download its gcs_uri objects to --images (numeric basename.jpg),
then run CPU inference. See reports/LIVE_TILING_MOB.md for the frozen protocol.
"""
from __future__ import annotations

import argparse
from collections import defaultdict
from dataclasses import asdict
import hashlib
import json
from pathlib import Path
import time

import cv2
import numpy as np

from edgeml.detectors.live_tiled import LiveTiledDetector, LiveTilingConfig
from edgeml.runtime.detectors import load_detector


def rows(path):
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def prepare(catalog, official, per_video=24):
    sources = {}
    for path in official:
        for image in json.loads(path.read_text())["images"]:
            assert image["id"] not in sources
            sources[image["id"]] = image
    groups = defaultdict(list)
    excluded = 0
    for image in rows(catalog / "images/seadronessee.jsonl"):
        upstream = sources[int(image["image_id"].rsplit("/", 1)[1])]
        source = upstream["source"]
        if "video" not in source:
            excluded += 1
            continue
        video = "/".join(source[k] for k in ("drone", "folder_name", "video"))
        groups[video].append(dict(image, official_video=video, frame_no=source["frame_no"]))
    selected, heldout, reference = [], [], []
    for video, images in sorted(groups.items()):
        u = int(hashlib.sha256(("live-tiles-v1:" + video).encode()).hexdigest()[:8], 16) / 2**32
        if u >= .25:
            reference.append(video)
            continue
        heldout.append(video)
        images.sort(key=lambda x: (x["frame_no"], x["image_id"]))
        selected.extend(images[i] for i in np.linspace(0, len(images) - 1, min(per_video, len(images)), dtype=int))
    assert not set(heldout) & set(reference)
    by_id = {x["image_id"]: x for x in selected}
    for image in selected:
        image["gt"] = []
    for ann in rows(catalog / "annotations/seadronessee.jsonl"):
        if ann["image_id"] in by_id:
            by_id[ann["image_id"]]["gt"].append({"label": ann["label"], "box": ann["box"]})
    return {"salt": "live-tiles-v1", "heldout_videos": heldout, "reference_videos_not_used": reference,
            "excluded_still_frames_without_video": excluded, "frames": selected,
            "official_sha256": {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in official}}


def iou(a, b):
    iw = max(0, min(a[0] + a[2], b[0] + b[2]) - max(a[0], b[0]))
    ih = max(0, min(a[1] + a[3], b[1] + b[3]) - max(a[1], b[1]))
    inter = iw * ih
    return inter / (a[2] * a[3] + b[2] * b[3] - inter + 1e-9)


def category(label, mob=False):
    if mob:
        return "mob" if label in {"swimmer", "person in water"} else None
    if label in {"swimmer", "person", "person in water"}:
        return "person"
    if label in {"boat", "jetski"}:
        return "vessel"
    return None


def counts(gt, predictions, *, size="all", mob=False, group=None):
    """Score-greedy one-to-one IoU .5; ignore matches outside a requested size slice.

    COCO-small uses native-pixel area <1024. Strict <32px requires both sides <32.
    Slice precision ignores unmatched predictions outside the slice (COCO convention).
    """
    def in_size(box):
        return size == "all" or (box[2] * box[3] < 1024 if size == "small" else max(box[2:]) < 32)

    gt = [g for g in gt if category(g["label"], mob) and (group is None or category(g["label"], mob) == group)]
    predictions = [p for p in predictions if category(p["label"], mob)
                   and (group is None or category(p["label"], mob) == group)]
    used, tp, fp = set(), 0, 0
    for p in sorted(predictions, key=lambda p: p["score"], reverse=True):
        eligible = [i for i, g in enumerate(gt) if i not in used
                    and category(g["label"], mob) == category(p["label"], mob) and iou(g["box"], p["box"]) >= .5]
        # Prefer in-slice GT, as in COCO's non-ignored-before-ignored matching.
        best = max(eligible, key=lambda i: (in_size(gt[i]["box"]), iou(gt[i]["box"], p["box"])), default=None)
        if best is not None:
            used.add(best)
            tp += int(in_size(gt[best]["box"]))
        elif in_size(p["box"]):
            fp += 1
    return np.array([tp, fp, sum(in_size(g["box"]) for g in gt)], dtype=int)


def metric(total):
    tp, fp, n = map(int, total)
    return {"tp": tp, "fp": fp, "gt": n, "precision": tp / (tp + fp) if tp + fp else None,
            "recall": tp / n if n else None}


def run(manifest, images, model, threads):
    cv2.setNumThreads(1)
    config = LiveTilingConfig()
    base = load_detector(model, engine="ort", threads=threads, score_thr=.05)
    for _ in range(5):
        base.detect(np.zeros((720, 1280, 3), np.uint8))
    records, current = [], None
    for index, frame in enumerate(manifest["frames"]):
        path = images / (frame["image_id"].rsplit("/", 1)[1] + ".jpg")
        assert hashlib.sha256(path.read_bytes()).hexdigest() == frame["sha256"], path
        image = cv2.imread(str(path))
        assert image.shape[:2] == (frame["height"], frame["width"]), path
        if frame["official_video"] != current:
            current = frame["official_video"]
            tiled = LiveTiledDetector(base, config)
        record = {"image_id": frame["image_id"], "official_video": current, "gt": frame["gt"], "modes": {}}
        modes = [("native", base), ("tiled", tiled)]
        for mode, detector in (modes if index % 2 == 0 else modes[::-1]):
            start = time.perf_counter()
            detections = detector.detect(image)
            ms = (time.perf_counter() - start) * 1000
            record["modes"][mode] = {"ms": ms, "tiles": tiled.n_calls - 1 if mode == "tiled" else 0,
                                     "predictions": [asdict(d) for d in detections]}
        records.append(record)
        if (index + 1) % 24 == 0:
            print(f"{index + 1}/{len(manifest['frames'])} frames", flush=True)
    result = {"config": asdict(config), "model_sha256": hashlib.sha256(model.read_bytes()).hexdigest(),
              "threads": threads, "frames": len(records), "videos": manifest["heldout_videos"], "thresholds": {}}
    for threshold in (.35, .8618552088737488):
        result["thresholds"][str(threshold)] = {}
        for mode in ("native", "tiled"):
            totals = defaultdict(lambda: np.zeros(3, dtype=int))
            for record in records:
                preds = [p for p in record["modes"][mode]["predictions"] if p["score"] >= threshold]
                for size in ("all", "small", "lt32"):
                    totals[size] += counts(record["gt"], preds, size=size)
                    totals["vessel_" + size] += counts(record["gt"], preds, size=size, group="vessel")
                totals["mob"] += counts(record["gt"], preds, mob=True)
            result["thresholds"][str(threshold)][mode] = {k: metric(v) for k, v in totals.items()}
    result["latency_ms"] = {mode: dict(zip(("p50", "p95"), map(float, np.percentile(
        [r["modes"][mode]["ms"] for r in records], [50, 95])))) for mode in ("native", "tiled")}
    result["tile_histogram"] = {str(n): sum(r["modes"]["tiled"]["tiles"] == n for r in records) for n in range(3)}
    result["budget_overruns"] = sum(r["modes"]["tiled"]["ms"] > config.budget_ms for r in records)
    return result, records


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    sub = ap.add_subparsers(dest="command", required=True)
    p = sub.add_parser("prepare")
    p.add_argument("--catalog", type=Path, required=True)
    p.add_argument("--official", type=Path, nargs=2, required=True)
    p.add_argument("--per-video", type=int, default=24)
    p.add_argument("--out", type=Path, required=True)
    p = sub.add_parser("run")
    p.add_argument("--manifest", type=Path, required=True)
    p.add_argument("--images", type=Path, required=True)
    p.add_argument("--model", type=Path, required=True)
    p.add_argument("--threads", type=int, default=4)
    p.add_argument("--out", type=Path, required=True)
    a = ap.parse_args()
    if a.command == "prepare":
        result = prepare(a.catalog, a.official, a.per_video)
    else:
        result, records = run(json.loads(a.manifest.read_text()), a.images, a.model, a.threads)
        a.out.with_suffix(".predictions.json").write_text(json.dumps(records))
    a.out.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({k: v for k, v in result.items() if k != "frames"}, indent=2))


if __name__ == "__main__":
    main()
