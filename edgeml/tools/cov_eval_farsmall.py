"""Recall on the held-out far/small DEV slice written by cov_build_detector_dataset.py (<out>/farsmall_dev.json).

Objects are dev-only (SeaShips valid, SeaDronesSee inferred-dev stride subset, holdout MOT clips: YOLO val, never train)
with area < 32^2 after scaling the frame's long side to 640. A GT box counts as found when a kept detection of the
vessel class overlaps it at IoU >= --iou (0.3: small-box IoU is noisy). Used for model selection only; never on test.

  python edgeml/tools/cov_eval_farsmall.py --slice ~/data/yolo_cov/farsmall_dev.json --catalog ~/data/clean_v3/catalog \
      --weights yolo11n.pt ~/runs/cov/yolo11n-cov-640/weights/best.pt --out ~/runs/cov/farsmall_dev.json
"""
from __future__ import annotations

import argparse
import json
import sys
from collections import defaultdict
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from cov_build_detector_dataset import catalog_rows, local  # noqa: E402


def iou(a, b):
    ax2, ay2, bx2, by2 = a[0] + a[2], a[1] + a[3], b[0] + b[2], b[1] + b[3]
    iw, ih = max(0.0, min(ax2, bx2) - max(a[0], b[0])), max(0.0, min(ay2, by2) - max(a[1], b[1]))
    inter = iw * ih
    return inter / (a[2] * a[3] + b[2] * b[3] - inter + 1e-9)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--slice", type=Path, required=True)
    ap.add_argument("--catalog", type=Path, required=True)
    ap.add_argument("--datasets", type=Path, default=Path("~/data/datasets").expanduser())
    ap.add_argument("--eval-root", type=Path, default=Path("~/data/edgeml_eval").expanduser())
    ap.add_argument("--weights", nargs="+", required=True)
    ap.add_argument("--imgsz", type=int, default=640)
    ap.add_argument("--conf", type=float, default=0.25, help="sidecar score floor")
    ap.add_argument("--iou", type=float, default=0.3)
    ap.add_argument("--classes", type=int, nargs="+", default=[8], help="COCO boat")
    ap.add_argument("--device", default="0")
    ap.add_argument("--out", type=Path, required=True)
    a = ap.parse_args()
    objs = json.loads(a.slice.read_text())
    paths = {}
    for ds in ("seaships7000", "seadronessee"):
        for r, _ in catalog_rows(a.catalog, ds):
            paths[(ds, r["image_id"])] = str(local(a.datasets, r))
    by_img = defaultdict(list)
    for o in objs:
        if o["set"] in ("seaships7000", "seadronessee"):
            p = paths[(o["set"], o["image"])]
        else:
            seq, f = o["image"].split("/")
            p = str(a.eval_root / o["set"] / seq / "img1" / f"{int(f):06d}.jpg")
        by_img[p].append(o)
    from ultralytics import YOLO
    res = {"n_objects": len(objs), "n_images": len(by_img), "conf": a.conf, "iou": a.iou, "imgsz": a.imgsz, "models": {}}
    imgs = sorted(by_img)
    for w in a.weights:
        model = YOLO(w)
        found, per_set, n_det = 0, defaultdict(lambda: [0, 0]), 0
        for i in range(0, len(imgs), 16):
            for p, r in zip(imgs[i:i + 16], model.predict(imgs[i:i + 16], imgsz=a.imgsz, conf=a.conf, classes=a.classes,
                                                          device=a.device, verbose=False)):
                dets = [[x1, y1, x2 - x1, y2 - y1] for x1, y1, x2, y2 in r.boxes.xyxy.tolist()]
                n_det += len(dets)
                for o in by_img[p]:
                    hit = any(iou(o["box"], d) >= a.iou for d in dets)
                    found += hit
                    per_set[o["set"]][0] += hit
                    per_set[o["set"]][1] += 1
        res["models"][w] = {"recall": round(found / max(1, len(objs)), 3), "found": found,
                            "dets_per_image": round(n_det / max(1, len(imgs)), 2),
                            "per_set": {k: {"found": v[0], "n": v[1], "recall": round(v[0] / v[1], 3)} for k, v in per_set.items()}}
        print(w, res["models"][w], flush=True)
    a.out.parent.mkdir(parents=True, exist_ok=True)
    a.out.write_text(json.dumps(res, indent=1))


if __name__ == "__main__":
    main()
