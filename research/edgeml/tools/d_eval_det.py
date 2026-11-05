"""Workstream D: class-agnostic vessel detection accuracy for exported detectors (FP32 vs INT8 drop).

Evaluates every ``<model>-<size>/<precision>/model.xml`` under --export on an image set with catalog GT
(``ghost_fleet.annotations`` JSONL, pixel [x,y,w,h]). Metrics: vessel AP50 (all-point interpolation), recall at
score>=0.25 overall and by GT box-area bucket (tiny <16^2, small <32^2, medium <96^2, large), mean score of matched
dets. Predictions with any ``vessel_labels`` label count as vessel; GT labels listed in --gt-labels count as vessel.

Usage:
  python tools/d_eval_det.py --export ~/models/export --images ~/data/smd/all --ann ~/data/catalog/ann/smd_vis_onboard_sample.jsonl \
      --videos MVI_0790 MVI_0799 --set smd_dev --out results/det_acc.jsonl
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

import numpy as np


def iou_xywh(a, b):
    ax1, ay1, aw, ah = a
    bx1, by1, bw, bh = b
    ix = max(0, min(ax1 + aw, bx1 + bw) - max(ax1, bx1))
    iy = max(0, min(ay1 + ah, by1 + bh) - max(ay1, by1))
    inter = ix * iy
    return inter / (aw * ah + bw * bh - inter + 1e-9)


def area_bucket(box):
    a = box[2] * box[3]
    return "tiny" if a < 16 ** 2 else "small" if a < 32 ** 2 else "medium" if a < 96 ** 2 else "large"


def ap50(all_dets, gts, thr=0.5):
    """all_dets: list of (image, score, box); gts: {image: [box]}"""
    n_gt = sum(len(v) for v in gts.values())
    if n_gt == 0:
        return float("nan")
    all_dets = sorted(all_dets, key=lambda d: -d[1])
    used = {k: np.zeros(len(v), bool) for k, v in gts.items()}
    tp = np.zeros(len(all_dets))
    for i, (img, s, box) in enumerate(all_dets):
        g = gts.get(img, [])
        best, bj = 0, -1
        for j, gb in enumerate(g):
            io = iou_xywh(box, gb)
            if io > best:
                best, bj = io, j
        if best >= thr and not used[img][bj]:
            used[img][bj] = True
            tp[i] = 1
    ctp = np.cumsum(tp)
    cfp = np.cumsum(1 - tp)
    rec = ctp / n_gt
    prec = ctp / np.maximum(ctp + cfp, 1e-9)
    mrec = np.concatenate([[0], rec, [1]])
    mpre = np.concatenate([[0], prec, [0]])
    for i in range(len(mpre) - 2, -1, -1):
        mpre[i] = max(mpre[i], mpre[i + 1])
    idx = np.where(mrec[1:] != mrec[:-1])[0]
    return float(np.sum((mrec[idx + 1] - mrec[idx]) * mpre[idx + 1]))


def evaluate(det, images: list[Path], gts: dict, score_thr=0.25):
    all_dets, matched_scores = [], []
    rec_hits, rec_tot = {}, {}
    n_fp = 0
    for p in images:
        import cv2

        im = cv2.imread(str(p))
        ds = det.detect(im)
        key = p.stem
        g = gts.get(key, [])
        for d in ds:
            all_dets.append((key, d.score, d.box))
        strong = [d for d in ds if d.score >= score_thr]
        used = set()
        for gb in g:
            b = area_bucket(gb)
            rec_tot[b] = rec_tot.get(b, 0) + 1
            best, bi = 0, -1
            for i, d in enumerate(strong):
                if i in used:
                    continue
                io = iou_xywh(d.box, gb)
                if io > best:
                    best, bi = io, i
            if best >= 0.5:
                used.add(bi)
                rec_hits[b] = rec_hits.get(b, 0) + 1
                matched_scores.append(strong[bi].score)
        n_fp += len(strong) - len(used)
    tot = sum(rec_tot.values())
    hits = sum(rec_hits.values())
    return {
        "ap50": ap50(all_dets, gts),
        "recall@0.25": hits / max(tot, 1),
        "recall_by_bucket": {b: {"recall": rec_hits.get(b, 0) / rec_tot[b], "n": rec_tot[b]} for b in rec_tot},
        "fp_per_image@0.25": n_fp / max(len(images), 1),
        "mean_matched_score": float(np.mean(matched_scores)) if matched_scores else 0.0,
        "n_images": len(images), "n_gt": tot,
    }


def load_gt(ann_path: str, images_dir: Path, videos: list[str] | None, gt_labels: set[str], ids_file: str | None):
    gts: dict[str, list] = {}
    keep_ids = set(Path(ids_file).read_text().split()) if ids_file else None
    for line in Path(ann_path).read_text().splitlines():
        d = json.loads(line)
        if not d["box"]:
            continue
        key = d["image_id"].split("/")[-1]
        if keep_ids is not None and d["image_id"] not in keep_ids:
            continue
        if videos and not any(key.startswith(v) for v in videos):
            continue
        if gt_labels and d["label"] not in gt_labels:
            continue
        gts.setdefault(key, []).append(d["box"])
    images = [p for p in sorted(images_dir.glob("*.jpg")) if (videos is None or any(p.stem.startswith(v) for v in videos))
              and (keep_ids is None or p.stem in {k for k in gts} or True)]
    if keep_ids is not None:
        stems = {i.split("/")[-1] for i in keep_ids}
        images = [p for p in images if p.stem in stems]
    return gts, images


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--export", required=True)
    ap.add_argument("--images", required=True)
    ap.add_argument("--ann", required=True)
    ap.add_argument("--videos", nargs="*")
    ap.add_argument("--ids", help="file with image_ids to keep (dev subset)")
    ap.add_argument("--gt-labels", nargs="*", default=[])
    ap.add_argument("--set", required=True)
    ap.add_argument("--models", nargs="*", help="model-size dirs to evaluate (default all)")
    ap.add_argument("--precisions", nargs="*", default=["fp32", "int8"])
    ap.add_argument("--engine", default="openvino")
    ap.add_argument("--threads", type=int, default=4)
    ap.add_argument("--max-images", type=int, default=0)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()
    sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
    sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "edgeml" / "src"))
    from edgeml.runtime.threads import pin_threads

    pin_threads(args.threads)
    from edgeml.runtime.detectors import load_detector

    gts, images = load_gt(args.ann, Path(args.images), args.videos, set(args.gt_labels), args.ids)
    if args.max_images:
        images = images[: args.max_images]
    print(f"{args.set}: {len(images)} images, {sum(len(v) for v in gts.values())} GT boxes", file=sys.stderr)
    dirs = sorted(d for d in Path(args.export).iterdir() if d.is_dir() and (not args.models or d.name in args.models))
    Path(args.out).parent.mkdir(parents=True, exist_ok=True)
    with open(args.out, "a") as f:
        for d in dirs:
            for prec in args.precisions:
                mp = d / prec / "model.xml"
                if not mp.exists():
                    continue
                det = load_detector(mp, engine=args.engine, threads=args.threads, score_thr=0.05)
                det.keep_labels = None
                vessel = {"boat", "ship", "vessel", "cargo ship", "tanker", "container ship", "fishing boat", "sailboat", "speedboat",
                          "warship", "submarine", "buoy"}
                orig = det.detect

                def detect_vessel(im, _orig=orig, _vessel=frozenset(vessel)):
                    return [x for x in _orig(im) if x.label in _vessel]

                det.detect = detect_vessel
                r = evaluate(det, images, gts)
                r.update({"set": args.set, "model": d.name, "precision": prec, "engine": args.engine})
                print(json.dumps(r), file=sys.stderr)
                f.write(json.dumps(r) + "\n")


if __name__ == "__main__":
    main()
