#!/usr/bin/env python3
"""Score frozen shore COCO sets. Fit score thresholds on valid ONLY.

Predictions: {"metadata": {mode configuration}, "detections": [COCO results]}.
Recall uses IoU .3 for GT area <1024, otherwise .5; AP50 always uses .5.
Non-vessel GT is crowd/ignore, matching bench_detectors' vessel view.
"""
from __future__ import annotations

import argparse
import collections
import contextlib
import hashlib
import io
import json
import math
import sys
from pathlib import Path

import numpy as np
from scipy.stats import beta

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
from edgeml.eval.detection import _coco_api, _iou_matrix

BUCKETS = ("tiny", "small", "medium", "large")


def bucket(area):
    return BUCKETS[0 if area < 256 else 1 if area < 1024 else 2 if area < 9216 else 3]


def cp_lower(successes, total, alpha=0.05):
    if not 0 <= successes <= total or total < 0 or not 0 < alpha < 1:
        raise ValueError("Invalid binomial counts/confidence")
    return None if total == 0 else 0.0 if successes == 0 else float(beta.ppf(alpha, successes, total - successes + 1))


def prepare(coco, predictions):
    images = {im["id"]: im for im in coco["images"]}
    if len(images) != len(coco["images"]):
        raise ValueError("Duplicate image ID")
    gt, dets = collections.defaultdict(list), collections.defaultdict(list)
    ids = set()
    for a in coco["annotations"]:
        if a["image_id"] not in images or a["id"] in ids:
            raise ValueError("Unknown image/duplicate annotation ID")
        ids.add(a["id"])
        gt[a["image_id"]].append(a)
    for d in predictions:
        if d["image_id"] not in images or d.get("category_id", 1) != 1:
            raise ValueError("Unknown image/category in predictions")
        b = d["bbox"]
        if len(b) != 4 or not all(math.isfinite(x) for x in b) or min(b[2:]) <= 0 or not 0 <= d["score"] <= 1:
            raise ValueError("Invalid prediction geometry/score")
        dets[d["image_id"]].append(d)
    events = []
    for iid in images:
        anns = gt[iid]
        ds = sorted(dets[iid], key=lambda d: -d["score"])
        overlaps = _iou_matrix(np.array([d["bbox"] for d in ds]).reshape(-1, 4),
                               np.array([a["bbox"] for a in anns]).reshape(-1, 4))
        ignored = np.array([bool(a.get("ignore") or a.get("iscrowd")) for a in anns])
        used = set()
        for i, d in enumerate(ds):
            candidates = [j for j, a in enumerate(anns) if j not in used and not ignored[j]
                          and overlaps[i, j] >= (0.3 if a["area"] < 1024 else 0.5)]
            match = max(candidates, key=lambda j: overlaps[i, j]) if candidates else None
            if match is not None:
                used.add(match)
                events.append({"d": d, "kind": 1, "ann": anns[match]})
            elif any(overlaps[i, j] >= 0.5 for j in range(len(anns)) if ignored[j]):
                events.append({"d": d, "kind": -1, "ann": None})
            else:
                events.append({"d": d, "kind": 0, "ann": None})
    return images, gt, events


def fit_threshold(coco, predictions, minimum_precision=0.90):
    if coco.get("info", {}).get("split") != "valid":
        raise ValueError("Threshold fitting is restricted to valid")
    if not 0 < minimum_precision <= 1:
        raise ValueError("Invalid precision target")
    _, _, events = prepare(coco, predictions)
    es = sorted((e for e in events if e["kind"] >= 0), key=lambda e: -e["d"]["score"])
    tp = fp = 0
    best = None
    for i, e in enumerate(es):
        tp += e["kind"] == 1
        fp += e["kind"] == 0
        score = e["d"]["score"]
        if i + 1 < len(es) and es[i + 1]["d"]["score"] == score:
            continue
        precision = tp / (tp + fp)
        if precision >= minimum_precision:
            candidate = (tp, precision, score)
            if best is None or candidate > best:
                best = candidate
    return {"tau": best[2] if best else 1.0000001, "feasible": best is not None,
            "minimum_precision": minimum_precision, "selection": "max recall; ties max precision then threshold"}


def track_recall(coco, hit_ids):
    """Every sliding, fully observed 2s window (ceil(2*fps) consecutive frames).

    Gaps break visibility; short tracks and incomplete tails do not enter denominator.
    IDs are scoped by dataset and source video. Correlated windows are not CP trials.
    """
    images = {im["id"]: im for im in coco["images"]}
    tracks = collections.defaultdict(dict)
    for a in coco["annotations"]:
        im = images[a["image_id"]]
        if a.get("ignore") or a.get("iscrowd") or a.get("track_id", -1) < 0 or not im.get("fps"):
            continue
        key = (im["dataset"], im["source_video"], a["track_id"], im["fps"])
        tracks[key][im["frame_index"]] = a["id"] in hit_ids
    windows = hits = eligible_tracks = 0
    for key, frames in tracks.items():
        length = math.ceil(2 * key[-1])
        eligible = False
        for start in sorted(frames):
            if all(f in frames for f in range(start, start + length)):
                windows += 1
                hits += any(frames[f] for f in range(start, start + length))
                eligible = True
        eligible_tracks += eligible
    return {"windows": windows, "detected_windows": hits, "eligible_tracks": eligible_tracks,
            "recall": hits / windows if windows else None, "window_seconds": 2, "sliding": True}


def ap50_strata(coco, predictions, image_ids):
    """COCO AP50 (101 recall points, maxDets=100), including ignore regions."""
    subset = {**coco, "images": [i for i in coco["images"] if i["id"] in image_ids],
              "annotations": [a for a in coco["annotations"] if a["image_id"] in image_ids]}
    ds = [d for d in predictions if d["image_id"] in image_ids]
    areas = {"all": [0, 1e10], "tiny": [0, 256 - 1e-8], "small": [256, 1024 - 1e-8],
             "medium": [1024, 9216 - 1e-8], "large": [9216, 1e10], "medium_large": [1024, 1e10]}
    out = {k: None for k in areas}
    if not subset["images"]:
        return out
    if not ds:
        for k, (lo, hi) in areas.items():
            if any(not a.get("ignore") and not a.get("iscrowd") and lo <= a["area"] <= hi for a in subset["annotations"]):
                out[k] = 0.0
        return out
    COCO, COCOeval = _coco_api()
    with contextlib.redirect_stdout(io.StringIO()):
        gt = COCO()
        gt.dataset = subset
        gt.createIndex()
        ev = COCOeval(gt, gt.loadRes(ds), "bbox")
        ev.params.iouThrs = np.array([0.5])
        ev.params.areaRng = list(areas.values())
        ev.params.areaRngLbl = list(areas)
        ev.evaluate()
        ev.accumulate()
    for j, k in enumerate(areas):
        p = ev.eval["precision"][0, :, :, j, -1]
        p = p[p >= 0]
        out[k] = float(p.mean()) if p.size else None
    return out


def evaluate(coco, predictions, tau):
    images, gt, events = prepare(coco, predictions)
    positives = [a for a in coco["annotations"] if not a.get("ignore") and not a.get("iscrowd")]
    selected = [e for e in events if e["d"]["score"] >= tau and e["kind"] >= 0]
    hit_ids = {e["ann"]["id"] for e in selected if e["kind"] == 1}
    empty_ids = {iid for iid in images if not any(not a.get("ignore") and not a.get("iscrowd") for a in gt[iid])}
    ap = {"all": ap50_strata(coco, predictions, set(images)),
          "day": ap50_strata(coco, predictions, {iid for iid in images if not images[iid].get("low_light", False)}),
          "low_light": ap50_strata(coco, predictions, {iid for iid in images if images[iid].get("low_light", False)})}
    strata = {}
    names = ["all", "day", "low_light", *BUCKETS, *("day_" + b for b in BUCKETS), "day_medium_large"]
    for name in names:
        def image_ok(iid):
            low = images[iid].get("low_light", False)
            return low if name == "low_light" else not low if name.startswith("day") else True

        def area_ok(area):
            b = bucket(area)
            return b in {"medium", "large"} if name == "day_medium_large" else b == name.removeprefix("day_") if name.removeprefix("day_") in BUCKETS else True

        anns = [a for a in positives if image_ok(a["image_id"]) and area_ok(a["area"])]
        hits = sum(a["id"] in hit_ids for a in anns)
        # FP bucket is its predicted area; TP bucket is its matched GT area.
        fp = sum(e["kind"] == 0 and image_ok(e["d"]["image_id"]) and area_ok(e["d"]["bbox"][2] * e["d"]["bbox"][3]) for e in selected)
        empty = {iid for iid in empty_ids if image_ok(iid)}
        empty_fp = sum(e["kind"] == 0 and e["d"]["image_id"] in empty and area_ok(e["d"]["bbox"][2] * e["d"]["bbox"][3]) for e in selected)
        sources = {(images[a["image_id"]]["dataset"], "Arriving_In_Norfolk" if images[a["image_id"]]["source_video"].startswith("Arriving_In_Norfolk") else images[a["image_id"]]["source_video"]) for a in anns}
        ap_value = ap["day"][name.removeprefix("day_")] if name.startswith("day_") else ap[name]["all"] if name in ap else ap["all"][name]
        strata[name] = {"vessels": len(anns), "detected": hits, "recall": hits / len(anns) if anns else None,
                        "recall_cp95_lower": cp_lower(hits, len(anns)), "precision": hits / (hits + fp) if hits + fp else None,
                        "false_positives": fp, "empty_frames": len(empty), "fp_per_empty_frame": empty_fp / len(empty) if empty else None,
                        "sources": len(sources), "AP50": ap_value}
    return {"tau": tau, "strata": strata, "AP50": ap["all"]["all"],
            "R4": track_recall(coco, hit_ids)}


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--coco", type=Path, required=True)
    ap.add_argument("--predictions", type=Path, required=True)
    ap.add_argument("--thresholds", type=Path, required=True)
    ap.add_argument("--fit-valid", action="store_true")
    ap.add_argument("--output", type=Path, required=True)
    args = ap.parse_args()
    raw = args.coco.read_bytes()
    coco = json.loads(raw)
    pred = json.loads(args.predictions.read_text())
    if args.fit_valid:
        fitted = {**fit_threshold(coco, pred["detections"]), "valid_coco_sha256": hashlib.sha256(raw).hexdigest(),
                  "configuration": pred["metadata"]}
        with args.thresholds.open("x") as stream:
            json.dump(fitted, stream, indent=2)
    else:
        fitted = json.loads(args.thresholds.read_text())
        if fitted["configuration"] != pred["metadata"]:
            raise ValueError("Frozen threshold configuration differs from predictions")
    result = evaluate(coco, pred["detections"], fitted["tau"])
    args.output.write_text(json.dumps(result, indent=2, allow_nan=False))
    print(json.dumps(result, allow_nan=False))


if __name__ == "__main__":
    main()
