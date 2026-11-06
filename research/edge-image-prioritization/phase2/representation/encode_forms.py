#!/usr/bin/env python3
"""Encode exact-byte image representations using pixels and cached predictions only.

The cache is image-only evidence from the unchanged phase-one NanoDet provider.
This module neither opens annotations nor uses them to propose or size crops.
"""
from __future__ import annotations

import argparse
import copy
import hashlib
import json
import math
from pathlib import Path
import platform
import time

import cv2
import numpy as np

CONFIG = {
    "version": "representation-v1",
    "full_forms": [
        ["full_1280_q80", "full", 1280, 80],
        ["full_1280_q50", "full", 1280, 50],
        ["overview_640_q80", "overview", 640, 80],
        ["overview_320_q50", "overview", 320, 50],
    ],
    "crop_confidence_min": 0.30,
    "crop_max_count": 2,
    "crop_margin_fraction_each_side": 0.25,
    "crop_max_side": 960,
    "crop_jpeg_quality": 80,
    "upscale": False,
    "all_detection_classes": True,
    "cached_nanodet_upstream_probability_threshold": 0.35,
    "crop_source": "cached_prediction_only",
}


def sha(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def padded_roi(box, width: int, height: int, margin: float = 0.25) -> list[int]:
    """Outward-round a predicted xywh box plus context; never leave original pixels."""
    if len(box) != 4 or not all(isinstance(v, (float, int)) and not isinstance(v, bool)
                                and math.isfinite(v) for v in box):
        raise ValueError("Detection box must contain four finite numbers")
    x, y, w, h = map(float, box)
    if width <= 0 or height <= 0 or margin < 0 or not math.isfinite(margin):
        raise ValueError("Invalid image dimensions or crop margin")
    # Require the same original-image contract as the cached provider.
    if x < 0 or y < 0 or w <= 0 or h <= 0 or x + w > width + 1e-9 or y + h > height + 1e-9:
        raise ValueError("Detection box lies outside original image")
    x0 = max(0, math.floor(x - margin * w))
    y0 = max(0, math.floor(y - margin * h))
    x1 = min(width, math.ceil(x + w + margin * w))
    y1 = min(height, math.ceil(y + h + margin * h))
    return [x0, y0, x1 - x0, y1 - y0]


def encode_pixels(bgr: np.ndarray, roi: list[int], max_side: int, quality: int):
    """Return actual JPEG bytes and dimensions, without manufacturing pixel detail."""
    x, y, w, h = roi
    pixels = bgr[y:y+h, x:x+w]
    if pixels.shape[:2] != (h, w) or min(w, h) <= 0:
        raise ValueError("ROI must be a nonempty rectangle inside the image")
    scale = min(1.0, max_side / max(w, h))
    dst_w, dst_h = max(1, round(w * scale)), max(1, round(h * scale))
    if scale < 1:
        pixels = cv2.resize(pixels, (dst_w, dst_h), interpolation=cv2.INTER_AREA)
    ok, encoded = cv2.imencode(".jpg", pixels, [cv2.IMWRITE_JPEG_QUALITY, quality])
    if not ok:
        raise RuntimeError("JPEG encoder failed")
    return encoded.tobytes(), dst_w, dst_h


def crop_candidates(record: dict) -> list[tuple[int, dict, list[int]]]:
    result = []
    for index, detection in enumerate(record.get("detections") or []):
        confidence = detection.get("confidence")
        if not isinstance(confidence, (int, float)) or isinstance(confidence, bool) or not math.isfinite(confidence) or not 0 <= confidence <= 1:
            raise ValueError("Invalid cached detection confidence")
        if confidence < CONFIG["crop_confidence_min"]:
            continue
        roi = padded_roi(detection["bbox_xywh"], record["width"], record["height"],
                         CONFIG["crop_margin_fraction_each_side"])
        result.append((index, detection, roi))
    result.sort(key=lambda item: (-item[1]["confidence"], item[0]))
    return result[:CONFIG["crop_max_count"]]


def encode_record(record: dict, bgr: np.ndarray, out: Path) -> dict:
    h, w = bgr.shape[:2]
    if [w, h] != [record["width"], record["height"]]:
        raise ValueError(f"Cached dimensions disagree with image: {record['id']}")
    result = copy.deepcopy(record)
    forms = []
    configs = [(fid, kind, side, quality, [0, 0, w, h], None)
               for fid, kind, side, quality in CONFIG["full_forms"]]
    for rank, (index, detection, roi) in enumerate(crop_candidates(record)):
        configs.append((f"crop_{rank}_q80", "crop", CONFIG["crop_max_side"],
                        CONFIG["crop_jpeg_quality"], roi,
                        {**copy.deepcopy(detection), "original_detection_index": index}))
    token = sha(record["id"].encode())[:24]
    for form_id, kind, side, quality, roi, detection in configs:
        started = time.perf_counter()
        raw, dst_w, dst_h = encode_pixels(bgr, roi, side, quality)
        encode_ms = (time.perf_counter() - started) * 1000
        relative = Path("payloads") / f"{token}-{form_id}.jpg"
        destination = out / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(raw)
        forms.append({
            "form_id": form_id,
            "kind": kind,
            "bytes": len(raw),
            "sha256": sha(raw),
            "roi": roi,
            "payload_width": dst_w,
            "payload_height": dst_h,
            "payload_path": relative.as_posix(),
            "jpeg_quality": quality,
            "max_side": side,
            "detection": detection,
            "encode_ms": encode_ms,
        })
    result["forms"] = forms
    return result


def run(records_path: Path, data_root: Path, out: Path, threads: int = 0) -> dict:
    if out.exists():
        raise ValueError("Output must be a new directory")
    records_raw = records_path.read_bytes()
    records = json.loads(records_raw)
    if not isinstance(records, list) or not records:
        raise ValueError("Expected a nonempty records array")
    ids = [r["id"] for r in records]
    if len(set(ids)) != len(ids):
        raise ValueError("Duplicate record IDs")
    out.mkdir(parents=True)
    cv2.setNumThreads(threads)
    cv2.ocl.setUseOpenCL(False)
    result, provenance = [], []
    started = time.perf_counter()
    root = data_root.resolve()
    for i, record in enumerate(records):
        relative = Path(record["source"]) / "img1" / f"{record['frame_index']:06d}.jpg"
        image_path = (root / relative).resolve()
        if not image_path.is_relative_to(root):
            raise ValueError("Input record path leaves data root")
        raw = image_path.read_bytes()
        tick = time.perf_counter()
        bgr = cv2.imdecode(np.frombuffer(raw, dtype=np.uint8), cv2.IMREAD_COLOR)
        if bgr is None:
            raise ValueError(f"Cannot decode {image_path}")
        decode_ms = (time.perf_counter() - tick) * 1000
        encoded = encode_record(record, bgr, out)
        encoded["representation_input_sha256"] = sha(raw)
        encoded["representation_decode_ms"] = decode_ms
        result.append(encoded)
        provenance.append({"id": record["id"], "path": relative.as_posix(), "sha256": sha(raw), "bytes": len(raw)})
        if (i+1) % 50 == 0:
            print(f"Encoded {i+1}/{len(records)} images", flush=True)
    all_forms = [f for r in result for f in r["forms"]]
    manifest = {
        "config": CONFIG,
        "records_sha256": sha(records_raw),
        "encoder_sha256": sha(Path(__file__).read_bytes()),
        "input_provenance": provenance,
        "records": len(result), "forms": len(all_forms),
        "crop_forms": sum(f["kind"] == "crop" for f in all_forms),
        "frames_with_crops": sum(any(f["kind"] == "crop" for f in r["forms"]) for r in result),
        "encoded_bytes": sum(f["bytes"] for f in all_forms),
        "encoding_ms": sum(f["encode_ms"] for f in all_forms),
        "decode_ms": sum(r["representation_decode_ms"] for r in result),
        "wall_seconds": time.perf_counter() - started,
        "python": platform.python_version(), "platform": platform.platform(),
        "opencv": cv2.__version__, "numpy": np.__version__,
        "requested_opencv_threads": threads, "reported_opencv_threads": cv2.getNumThreads(),
        "annotation_access": "none; image paths derived from cached source and frame_index",
        "timing_scope": "local representation encoding only; no detector inference or transport",
    }
    (out / "records-with-forms.json").write_text(json.dumps(result, indent=2, allow_nan=False) + "\n")
    (out / "manifest.json").write_text(json.dumps(manifest, indent=2, allow_nan=False) + "\n")
    print(json.dumps({k: manifest[k] for k in ["records", "forms", "crop_forms", "frames_with_crops", "encoded_bytes", "wall_seconds"]}), flush=True)
    return manifest


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--records", type=Path, required=True)
    p.add_argument("--data-root", type=Path, required=True)
    p.add_argument("--out", type=Path, required=True)
    p.add_argument("--threads", type=int, default=0,
                   help="0 disables OpenCV parallel regions (portable across macOS GCD)")
    a = p.parse_args()
    if a.threads < 0:
        p.error("--threads must be nonnegative")
    run(a.records, a.data_root, a.out, a.threads)


if __name__ == "__main__":
    main()
