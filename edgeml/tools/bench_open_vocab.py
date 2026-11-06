#!/usr/bin/env python3
"""Measure real EdgemlEngine.analyze latency for 0/1/5 cached prompts on v3 frames."""
import argparse
import json
from pathlib import Path
import time

import cv2
import numpy as np

from edgeml.sidecar.service import EdgemlEngine


def main(args):
    cv2.setNumThreads(1)
    manifest = [json.loads(x) for x in (args.evaluation / "manifest.jsonl").read_text().splitlines()]
    rows = [x for x in manifest if x["eval_split"] == "test"][:args.frames]
    images = [cv2.imread(str(args.evaluation / "images" / (x["sha256"] + ".jpg"))) for x in rows]
    queries = ["buoy", "jetski", "life saving appliances", "oil slick", "orange life jackets"]
    report = {"frames_per_condition": len(images) * args.repeats, "threads": 4,
              "scope": "EdgemlEngine.analyze including detector, tracker, aggregator, optional crop/grid matcher; excludes JPEG/RPC",
              "image_ids": [x["image_id"] for x in rows], "conditions": {}}
    for count in (0, 1, 5):
        # Reset live tiling cursors, tracker state, runtime sessions and text cache equally.
        engine = EdgemlEngine(args.models / "yolo11n-640/model.onnx", args.models / "mobileclip-s1", threads=4)
        assert engine.capabilities()["openVocabulary"]
        labels = ["open-vocab:" + q for q in queries[:count]]
        started = time.perf_counter()
        engine.analyze("warmup", images[0], None, labels, False)
        cold = (time.perf_counter() - started) * 1000
        times = []
        for repeat in range(args.repeats):
            for i, image in enumerate(images):
                started = time.perf_counter()
                engine.analyze(f"frame-{repeat}-{i}", image, None, labels, False)
                times.append((time.perf_counter() - started) * 1000)
        report["conditions"][str(count)] = {"cold_first_ms": cold, "mean_ms": float(np.mean(times)),
                                              "median_ms": float(np.median(times)), "p95_ms": float(np.percentile(times, 95)),
                                              "text_cache_entries": len(engine._cached_text.cache)}
        print(count, report["conditions"][str(count)], flush=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evaluation", type=Path, required=True)
    parser.add_argument("--models", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--frames", type=int, default=12)
    parser.add_argument("--repeats", type=int, default=2)
    main(parser.parse_args())
