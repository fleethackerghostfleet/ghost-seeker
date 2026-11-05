#!/usr/bin/env python
"""DINOv2 feature-only CPU smoke benchmark; NOT a zero-shot text classifier.

Uses D's labelled dev crops and preprocessing from the pretrained image processor.
No fitted head/bank is constructed and no locked-test image is read.
"""
import argparse
import json
import time
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "edgeml" / "src"))
from edgeml.runtime.threads import pin_threads


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--ann", required=True)
    ap.add_argument("--ids", required=True)
    ap.add_argument("--images", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()
    pin_threads(4)
    import numpy as np
    import torch
    from PIL import Image
    from transformers import AutoImageProcessor, AutoModel
    from d_eval_zeroshot import load_crops

    pin_threads(4)
    crops = load_crops(args.ann, args.images, args.ids, max_per_class=10, context=1.5)
    if not crops:
        raise ValueError("No dev crops loaded; check image/annotation/ID paths")
    results = {}
    for model_id in ("facebook/dinov2-small", "facebook/dinov2-base"):
        processor = AutoImageProcessor.from_pretrained(model_id)
        model = AutoModel.from_pretrained(model_id).eval()
        latency = []
        with torch.inference_mode():
            for crop, *_ in crops:
                t0 = time.perf_counter()
                inputs = processor(images=Image.fromarray(crop[:, :, ::-1]), return_tensors="pt")
                feature = model(**inputs).last_hidden_state[:, 0]
                assert torch.isfinite(feature).all()
                latency.append((time.perf_counter() - t0) * 1000)
        results[model_id] = {"params_m": sum(p.numel() for p in model.parameters()) / 1e6,
            "dim": feature.shape[-1], "n": len(crops), "ms_p50": float(np.median(latency[5:])),
            "ms_p95": float(np.percentile(latency[5:], 95)), "device": "cpu", "threads": 4,
            "split": "dev", "accuracy": None,
            "note": "Image-only encoder; no maritime/text head. Zero-shot subtype accuracy is undefined."}
    Path(args.out).write_text(json.dumps(results, indent=2))


if __name__ == "__main__":
    main()
