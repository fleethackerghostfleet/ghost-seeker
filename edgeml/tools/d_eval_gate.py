"""Workstream D: frame-gate cost and savings on 1-fps clips, and GT safety on the SMD sample.

For every clip dir: run FrameGate on all frames, report pass rate, reason counts, cost ms p50/p95.
For SMD (--smd-images + --smd-ann): frames are per video in time order; report how many frames with >=1 GT box were
dropped by each reason (should be 0 for blank/blur; no_change drops are acceptable only if the previous *kept*
frame contained the same objects, which we also check by IoU>=0.5 of every GT box against the last kept frame's GT).

  python tools/d_eval_gate.py --clips ~/data/clips/* --smd-images ~/data/smd/all --smd-ann ~/data/catalog/ann/smd_vis_onboard_sample.jsonl \
      --videos MVI_0790 MVI_0799 --out results/gate.json
"""
from __future__ import annotations

import argparse
import collections
import json
import sys
from pathlib import Path

import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))


def iou(a, b):
    ix = max(0, min(a[0] + a[2], b[0] + b[2]) - max(a[0], b[0]))
    iy = max(0, min(a[1] + a[3], b[1] + b[3]) - max(a[1], b[1]))
    i = ix * iy
    return i / (a[2] * a[3] + b[2] * b[3] - i + 1e-9)


def run_gate(frames, gate_kw, calibrate: bool):
    import cv2

    from edgeml.runtime.gate import FrameGate

    g = FrameGate(**gate_kw)
    if calibrate:
        g.calibrate([cv2.imread(str(p)) for p in frames[: min(30, len(frames))]])
    out = []
    for i, p in enumerate(frames):
        out.append(g(cv2.imread(str(p)), float(i)))
    return g, out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--clips", nargs="*", default=[])
    ap.add_argument("--smd-images")
    ap.add_argument("--smd-ann")
    ap.add_argument("--videos", nargs="*")
    ap.add_argument("--max-frames", type=int, default=600)
    ap.add_argument("--threads", type=int, default=4)
    ap.add_argument("--calibrate", action="store_true", help="Reducto-style per-clip threshold from the first 30 frames")
    ap.add_argument("--diff-thr", type=float, default=4.0)
    ap.add_argument("--phash-thr", type=int, default=6)
    ap.add_argument("--blur-thr", type=float, default=0.15)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()
    from edgeml.runtime.threads import pin_threads

    pin_threads(args.threads)
    kw = {"diff_thr": args.diff_thr, "phash_thr": args.phash_thr, "blur_thr": args.blur_thr}
    res = {"config": {**kw, "calibrate": args.calibrate}, "clips": [], "smd": []}
    for c in args.clips:
        frames = sorted(Path(c).glob("*.jpg"))[: args.max_frames]
        if not frames:
            continue
        g, dec = run_gate(frames, kw, args.calibrate)
        cost = [d.cost_ms for d in dec]
        row = {"clip": Path(c).name, "n": len(dec), "kept": sum(d.run_detector for d in dec), "reasons": dict(collections.Counter(d.reason for d in dec)),
               "cost_ms_p50": float(np.median(cost)), "cost_ms_p95": float(np.percentile(cost, 95)), "thr": {"diff": g.diff_thr, "phash": g.phash_thr},
               "horizon_y_frac_median": float(np.median([d.signals.get("horizon_y_frac", np.nan) for d in dec]))}
        row["saved_frac"] = 1 - row["kept"] / row["n"]
        print(json.dumps(row), file=sys.stderr)
        res["clips"].append(row)
    if args.smd_images and args.smd_ann:
        gt = collections.defaultdict(list)
        for line in Path(args.smd_ann).read_text().splitlines():
            d = json.loads(line)
            if d["box"]:
                gt[d["image_id"].split("/")[-1]].append(d["box"])
        vids = args.videos or sorted({k.rsplit("_f", 1)[0] for k in gt})
        for v in vids:
            frames = sorted(p for p in Path(args.smd_images).glob(f"{v}_f*.jpg"))
            g, dec = run_gate(frames, kw, args.calibrate)
            dropped_with_gt, unsafe, last_kept_gt = 0, 0, []
            for p, d in zip(frames, dec):
                boxes = gt.get(p.stem, [])
                if d.run_detector:
                    last_kept_gt = boxes
                elif boxes:
                    dropped_with_gt += 1
                    # unsafe if some GT object in the dropped frame has no counterpart in the last kept frame
                    if any(max([iou(b, k) for k in last_kept_gt] or [0]) < 0.5 for b in boxes):
                        unsafe += 1
            row = {"video": v, "n": len(dec), "kept": sum(d.run_detector for d in dec), "reasons": dict(collections.Counter(d.reason for d in dec)),
                   "frames_with_gt": sum(1 for p in frames if gt.get(p.stem)), "dropped_with_gt": dropped_with_gt, "dropped_unsafe": unsafe,
                   "cost_ms_p50": float(np.median([d.cost_ms for d in dec]))}
            print(json.dumps(row), file=sys.stderr)
            res["smd"].append(row)
    Path(args.out).parent.mkdir(parents=True, exist_ok=True)
    Path(args.out).write_text(json.dumps(res, indent=1))


if __name__ == "__main__":
    main()
