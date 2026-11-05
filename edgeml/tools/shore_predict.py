#!/usr/bin/env python3
"""Frozen DEV inference or selection-authorized single locked pass."""
import argparse
import hashlib
import json
import sys
import time
from pathlib import Path

import numpy as np

from shore_eval import evaluate, fit_threshold
from shore_train import verify_split

COCO_WEIGHTS_SHA256 = "d81979a9213a2109345158ce9232668df4c1ae52e9b8db3f2ec0a8cbad959b33"


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def resolve_checkpoint(weights):
    path = weights if weights is not None else Path("rf-detr-small.pth")
    sha = digest(path)
    if weights is None and sha != COCO_WEIGHTS_SHA256:
        raise ValueError("Reference COCO checkpoint hash changed")
    return path, sha, sha == COCO_WEIGHTS_SHA256


def authorize(selection, role, threshold_sha256, weights_sha256):
    chosen = selection[role]
    if chosen["threshold_sha256"] != threshold_sha256 or chosen["weights_sha256"] != weights_sha256:
        raise ValueError("Locked model/threshold differs from pre-test selection")


def candidate_key(metrics, latency):
    s = metrics["strata"]
    bounds = [s[k]["recall_cp95_lower"] for k in ("day_small", "day_medium_large")]
    if any(v is None for v in bounds):
        raise ValueError("Missing gated valid stratum")
    return min(bounds), sum(bounds), -latency["p95_ms"]


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--data", type=Path, required=True)
    ap.add_argument("--manifests", type=Path, required=True)
    ap.add_argument("--weights", type=Path)
    ap.add_argument("--resolution", type=int, default=512)
    ap.add_argument("--output", type=Path, required=True)
    ap.add_argument("--threshold", type=Path)
    ap.add_argument("--fit-valid", action="store_true")
    ap.add_argument("--selection", type=Path)
    ap.add_argument("--role", choices=["selected", "reference"])
    args = ap.parse_args()
    coco_path = args.data / "_annotations.coco.json"
    coco = json.loads(coco_path.read_text())
    split = coco["info"]["split"]
    weights_path, weights_sha, is_reference = resolve_checkpoint(args.weights)
    if args.fit_valid and split != "valid":
        ap.error("Threshold fitting is valid-only")
    if split != "valid":
        if not split.startswith("test_") or args.fit_valid or not args.selection or not args.role or not args.threshold:
            ap.error("Locked pass requires frozen selection, role and threshold")
        authorize(json.loads(args.selection.read_text()), args.role, digest(args.threshold), weights_sha)
    elif not args.fit_valid and not args.threshold:
        ap.error("Need a threshold or valid-only fitting")
    verify_split(args.data, args.manifests, split)
    args.output.mkdir(parents=True, exist_ok=False)
    (args.output / "STARTED.json").write_text(json.dumps({"split": split, "weights_sha256": weights_sha,
        "threshold_sha256": digest(args.threshold) if args.threshold else None,
        "selection_sha256": digest(args.selection) if args.selection else None, "coco_sha256": digest(coco_path)}, indent=2))
    import cv2
    import torch
    from PIL import Image

    sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
    from edgeml.detectors.base import BaseDetector, xyxy_to_xywh
    from edgeml.detectors.bakeoff import RFDetector
    from edgeml.detectors.labels import agnostic
    from edgeml.types import Detection

    torch.set_num_threads(4)
    if not is_reference:
        from rfdetr import RFDETRSmall

        class FineDetector(BaseDetector):
            name = "shore-rfdetr-small"

            def __init__(self):
                super().__init__(score_thr=.001)
                self.model = RFDETRSmall(device="cuda", pretrain_weights=str(weights_path), resolution=args.resolution)
                if self.model.model.model.class_embed.bias.shape[0] != 1:
                    raise ValueError("Expected the trained one-logit vessel head")

            def _infer(self, bgr):
                ds = self.model.predict(Image.fromarray(bgr[:, :, ::-1]), threshold=.001)
                if any(int(c) != 0 for c in ds.class_id):
                    raise ValueError("Unexpected trained class ID")
                return [Detection(box=xyxy_to_xywh(b), score=float(s), label="vessel", label_id=0)
                        for b, s in zip(ds.xyxy, ds.confidence)]

            def _sync(self):
                torch.cuda.synchronize()

        detector = FineDetector()
        configuration = {"model": "RFDETRSmall", "weights_sha256": weights_sha, "resolution": args.resolution,
                         "mode": "native" if args.resolution == 512 else "high_res", "candidate_score_floor": .001,
                         "training_class_id": 0, "evaluation_class_id": 1, "max_det": 300}
    else:
        if args.resolution != 512:
            ap.error("Reference must be the frozen native 512 baseline")
        detector = RFDetector("RFDETRSmall", "rfdetr-small", device="cuda", score_thr=.001,
                              pretrain_weights=str(weights_path))
        configuration = {"model": "RFDETRSmall", "rfdetr": "1.3.0", "weights": "COCO pretrained", "resolution": 512,
                         "mode": "native", "candidate_score_floor": .001, "tile": None, "overlap": None,
                         "include_full": True, "merge_nms_iou": None, "max_det": 300}
    frozen = json.loads(args.threshold.read_text()) if args.threshold else None
    if frozen and frozen["configuration"] != configuration:
        raise ValueError("Inference configuration differs from frozen threshold")
    first = np.zeros((1080, 1920, 3), dtype=np.uint8)
    for _ in range(5):
        detector.detect(first)
    predictions, timings = [], []
    with (args.output / "frames.jsonl").open("x") as journal:
        for i, im in enumerate(coco["images"]):
            image = cv2.imread(str(args.data / im["file_name"]))
            if image is None:
                raise ValueError("Image decode failed")
            torch.cuda.synchronize()
            start = time.perf_counter()
            ds = detector.detect(image)
            torch.cuda.synchronize()
            elapsed = (time.perf_counter() - start) * 1000
            if image.shape[:2] == (1080, 1920):
                timings.append(elapsed)
            rows = [{"image_id": im["id"], "category_id": 1, "bbox": list(map(float, d.box)), "score": d.score}
                    for d in ds if agnostic(d.label) == "vessel"]
            predictions.extend(rows)
            journal.write(json.dumps({"image_id": im["id"], "ms": elapsed, "is_1080p": image.shape[:2] == (1080, 1920), "detections": rows}) + "\n")
            journal.flush()
            if (i + 1) % 200 == 0:
                print(f"{split}: {i + 1}/{len(coco['images'])}", flush=True)
    (args.output / "predictions.json").write_text(json.dumps({"metadata": configuration, "detections": predictions}))
    if args.fit_valid:
        frozen = {**fit_threshold(coco, predictions), "valid_coco_sha256": digest(coco_path), "configuration": configuration}
    (args.output / "threshold.json").write_text(json.dumps(frozen, indent=2))
    result = evaluate(coco, predictions, frozen["tau"])
    (args.output / "metrics.json").write_text(json.dumps(result, indent=2, allow_nan=False))
    latency = {"n_1080p": len(timings), "p50_ms": float(np.percentile(timings, 50)) if timings else None,
               "p95_ms": float(np.percentile(timings, 95)) if timings else None, "gpu": torch.cuda.get_device_name(0),
               "scope": "decode excluded; synchronized preprocessing+inference; five warmups excluded"}
    (args.output / "latency.json").write_text(json.dumps(latency, indent=2))
    (args.output / "COMPLETE.json").write_text(json.dumps({"images": len(coco["images"]), "split": split,
        "threshold_sha256": digest(args.output / "threshold.json"), "weights_sha256": weights_sha}, indent=2))
    print(json.dumps({"split": split, "tau": frozen["tau"], "all": result["strata"]["all"], "latency": latency}), flush=True)


if __name__ == "__main__":
    main()
