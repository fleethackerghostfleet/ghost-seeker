"""Contact sheets for the detector benchmark: failure modes on labelled dev sets and qualitative clips.

  failures: python edgeml/tools/contact_sheets.py failures --run ~/runs/<run_id> --model yolo11n --set smd_vis_onboard_sample__dev
  clips:    python edgeml/tools/contact_sheets.py clips --models yolo11n,dfine-n --clips Sailboat_on_the_horizon,... --out ~/runs/<run_id>/clips

GT is drawn green, detections >= tau in red (vessel) / orange (other agnostic class). Sheets are JPEGs under <run>/sheets/.
"""
from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path

import cv2
import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from bench_detectors import load_dets  # noqa: E402
from edgeml.data.catalog import clip_frames, clip_split, eval_set  # noqa: E402
from edgeml.detectors import build  # noqa: E402
from edgeml.detectors.labels import agnostic  # noqa: E402
from edgeml.eval.detection import match_at_threshold  # noqa: E402

CAT = os.path.expanduser("~/data/catalog")
ROOT = os.path.expanduser("~/data")


def draw(img, gt_boxes, dets, tau, focus=None, pad=0):
    out = img.copy()
    for b in gt_boxes:
        x, y, w, h = map(int, b)
        cv2.rectangle(out, (x, y), (x + w, y + h), (0, 255, 0), 2)
    for d in dets:
        if d.score < tau:
            continue
        a = agnostic(d.label)
        col = (0, 0, 255) if a == "vessel" else (0, 165, 255)
        x, y, w, h = map(int, d.box)
        cv2.rectangle(out, (x, y), (x + w, y + h), col, 2)
        cv2.putText(out, f"{d.label[:12]} {d.score:.2f}", (x, max(y - 4, 10)), cv2.FONT_HERSHEY_SIMPLEX, 0.5, col, 1, cv2.LINE_AA)
    if focus is not None:
        x, y, w, h = focus
        cx, cy = x + w / 2, y + h / 2
        s = max(w, h) * 4 + 2 * pad
        s = max(s, 160)
        x0, y0 = int(max(cx - s / 2, 0)), int(max(cy - s / 2, 0))
        x1, y1 = int(min(cx + s / 2, out.shape[1])), int(min(cy + s / 2, out.shape[0]))
        out = out[y0:y1, x0:x1]
    return out


def grid(tiles, cols=3, cell=(480, 300), title=None):
    cells = []
    for t, cap in tiles:
        h, w = t.shape[:2]
        sc = min(cell[0] / w, cell[1] / h)
        t = cv2.resize(t, (int(w * sc), int(h * sc)))
        canvas = np.full((cell[1] + 22, cell[0], 3), 30, np.uint8)
        canvas[:t.shape[0], :t.shape[1]] = t
        cv2.putText(canvas, cap[:70], (4, cell[1] + 16), cv2.FONT_HERSHEY_SIMPLEX, 0.45, (255, 255, 255), 1, cv2.LINE_AA)
        cells.append(canvas)
    while len(cells) % cols:
        cells.append(np.full_like(cells[0], 30))
    rows = [np.hstack(cells[i:i + cols]) for i in range(0, len(cells), cols)]
    sheet = np.vstack(rows)
    if title:
        bar = np.full((28, sheet.shape[1], 3), 60, np.uint8)
        cv2.putText(bar, title, (6, 20), cv2.FONT_HERSHEY_SIMPLEX, 0.6, (255, 255, 255), 1, cv2.LINE_AA)
        sheet = np.vstack([bar, sheet])
    return sheet


def failures(args):
    run = Path(args.run)
    metrics = json.loads((run / "metrics.json").read_text())
    ds, split = args.set.rsplit("__", 1)
    entry = metrics[args.model][args.set]
    tau = entry["tau"]
    images = eval_set(ds, split, CAT, ROOT, stride=args.stride)
    dets = load_dets(run / args.model / args.set / "dets.jsonl")
    m = match_at_threshold(images, dets, tau)
    per = m["per_image"]
    by_id = {im.image_id: im for im in images}
    out = run / "sheets"
    out.mkdir(exist_ok=True)
    # worst false-positive frames (sea-only first), worst misses (smallest GT first)
    fp_ids = sorted(per, key=lambda k: (-per[k][1], per[k][0]))[:args.k]
    fn = []
    for k, (tp, fp, nfn) in per.items():
        if nfn:
            im = by_id[k]
            vessel = [b for b, l in zip(im.boxes, im.labels) if agnostic(l) == "vessel"]
            fn.append((min(b[2] * b[3] for b in vessel), k))
    fn_ids = [k for _, k in sorted(fn)[:args.k]]
    for kind, ids in (("fp", fp_ids), ("fn", fn_ids)):
        tiles = []
        for k in ids:
            im = by_id[k]
            img = cv2.imread(im.local_path)
            if img is None:
                continue
            gt = [b for b, l in zip(im.boxes, im.labels) if agnostic(l) == "vessel"]
            focus = None
            if kind == "fn" and gt:
                focus = min(gt, key=lambda b: b[2] * b[3])
            tiles.append((draw(img, gt, dets.get(k, []), tau, focus=focus),
                          f"{k.split('/')[-1]} tp={per[k][0]} fp={per[k][1]} fn={per[k][2]}"))
        if tiles:
            sheet = grid(tiles, cols=args.cols, title=f"{args.model} {args.set} tau={tau} worst-{kind} (GT green, det red)")
            p = out / f"{args.model}__{args.set}__{kind}.jpg"
            cv2.imwrite(str(p), sheet, [cv2.IMWRITE_JPEG_QUALITY, 80])
            print(p)


def clips(args):
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    summary = {}
    dets_models = {m: build(m, imgsz=args.imgsz, score_thr=0.05) for m in args.models.split(",")}
    taus = json.loads(Path(args.taus).read_text()) if args.taus else {}
    for clip in args.clips.split(","):
        frames = [f for f in clip_frames(clip, CAT, ROOT) if os.path.exists(f.local_path)]
        frames = frames[::args.stride][:args.max_frames]
        if not frames:
            print("no local frames for", clip)
            continue
        for mname, det in dets_models.items():
            tau = taus.get(mname, args.tau)
            tiles, n_det, n_frames_with = [], 0, 0
            recs = []
            for f in frames:
                img = cv2.imread(f.local_path)
                ds = det.detect(img)
                keep = [d for d in ds if d.score >= tau and agnostic(d.label) is not None]
                n_det += len(keep)
                n_frames_with += bool(keep)
                recs.append({"image_id": f.image_id, "n": len(keep), "dets": [[d.label, round(d.score, 3), [round(v, 1) for v in d.box]] for d in keep]})
                tiles.append((draw(img, [], ds, tau), f"{f.source_ref} n={len(keep)}"))
            sheet = grid(tiles[:args.max_tiles], cols=args.cols, title=f"{clip} [{clip_split(clip)}] {mname} tau={tau} ({n_frames_with}/{len(frames)} frames with dets)")
            p = out / f"{clip}__{mname}.jpg"
            cv2.imwrite(str(p), sheet, [cv2.IMWRITE_JPEG_QUALITY, 75])
            (out / f"{clip}__{mname}.jsonl").write_text("\n".join(json.dumps(r) for r in recs) + "\n")
            summary.setdefault(clip, {})[mname] = {"split": clip_split(clip), "frames": len(frames), "frames_with_dets": n_frames_with,
                                                   "dets_per_frame": round(n_det / len(frames), 3), "tau": tau}
            print(p, summary[clip][mname])
    (out / "summary.json").write_text(json.dumps(summary, indent=1))


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    a = sub.add_parser("failures")
    a.add_argument("--run", required=True)
    a.add_argument("--model", required=True)
    a.add_argument("--set", required=True)
    a.add_argument("--stride", type=int, default=1, help="must match the bench run's stride for this set")
    a.add_argument("--k", type=int, default=9)
    a.add_argument("--cols", type=int, default=3)
    a.set_defaults(fn=failures)
    c = sub.add_parser("clips")
    c.add_argument("--models", required=True)
    c.add_argument("--clips", required=True)
    c.add_argument("--out", required=True)
    c.add_argument("--imgsz", type=int, default=640)
    c.add_argument("--tau", type=float, default=0.3)
    c.add_argument("--taus", default=None, help="json {model: tau} from the dev run")
    c.add_argument("--stride", type=int, default=1)
    c.add_argument("--max-frames", type=int, default=60)
    c.add_argument("--max-tiles", type=int, default=12)
    c.add_argument("--cols", type=int, default=3)
    c.set_defaults(fn=clips)
    args = ap.parse_args()
    args.fn(args)


if __name__ == "__main__":
    main()
