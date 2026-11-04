#!/usr/bin/env python3
"""Run the OWLv2 CPU cross-check and compare its boxes with Gemini."""

from __future__ import annotations

import argparse
import configparser
import json
import time
from pathlib import Path
from typing import Any

import cv2
import numpy as np
from PIL import Image

MODEL = "google/owlv2-base-patch16-ensemble"
GEMINI_LABELS = [
    "cargo",
    "tanker",
    "container",
    "fishing",
    "sailboat",
    "small_boat",
    "warship",
    "submarine",
    "ferry_passenger",
    "tug",
    "buoy",
    "aircraft",
    "person",
    "marine_animal",
    "unknown",
]


def box_iou(a: list[float], b: list[float]) -> float:
    ax, ay, aw, ah = a
    bx, by, bw, bh = b
    intersection = max(0.0, min(ax + aw, bx + bw) - max(ax, bx)) * max(
        0.0, min(ay + ah, by + bh) - max(ay, by)
    )
    union = aw * ah + bw * bh - intersection
    return intersection / union if union > 0 else 0.0


def load_model():
    import torch
    from transformers import Owlv2ForObjectDetection, Owlv2Processor

    torch.set_num_threads(8)
    processor = Owlv2Processor.from_pretrained(MODEL)
    model = Owlv2ForObjectDetection.from_pretrained(MODEL).to("cpu").eval()
    return processor, model, torch


def detect(
    image_path: Path,
    processor: Any,
    model: Any,
    torch: Any,
    prompts: list[str],
) -> list[dict[str, Any]]:
    with Image.open(image_path) as image:
        image = image.convert("RGB")
        width, height = image.size
        inputs = processor(text=prompts, images=image, return_tensors="pt")
    with torch.inference_mode():
        outputs = model(**inputs)
    results = processor.post_process_object_detection(
        outputs, target_sizes=[(height, width)], threshold=0.2
    )[0]
    detections = []
    for box, score, label_index in zip(
        results["boxes"].cpu().numpy(),
        results["scores"].cpu().numpy(),
        results["labels"].cpu().numpy(),
    ):
        x1, y1, x2, y2 = (float(value) for value in box)
        x1, y1 = max(0.0, min(width, x1)), max(0.0, min(height, y1))
        x2, y2 = max(0.0, min(width, x2)), max(0.0, min(height, y2))
        if x2 <= x1 or y2 <= y1:
            continue
        detections.append(
            {
                "label": prompts[int(label_index)],
                "score": float(score),
                "box": [x1, y1, x2 - x1, y2 - y1],
            }
        )
    return detections


def read_gemini(seq_dir: Path) -> dict[int, list[list[float]]]:
    path = seq_dir / "teacher" / "dets.jsonl"
    rows = {}
    if not path.exists():
        return rows
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        row = json.loads(line)
        rows[int(row["frame"])] = [detection["box"] for detection in row.get("detections", [])]
    return rows


def compare(
    gemini_boxes: dict[int, list[list[float]]],
    owlv2_by_frame: dict[int, list[dict[str, Any]]],
) -> dict[str, Any]:
    gemini_total = gemini_matched = owl_total = owl_unmatched = 0
    for frame, detections in owlv2_by_frame.items():
        gemini = gemini_boxes.get(frame, [])
        gemini_total += len(gemini)
        for box in gemini:
            if any(box_iou(box, detection["box"]) >= 0.3 for detection in detections):
                gemini_matched += 1
        owl_review = [detection for detection in detections if detection["score"] >= 0.3]
        owl_total += len(owl_review)
        for detection in owl_review:
            if not any(box_iou(detection["box"], box) >= 0.3 for box in gemini):
                owl_unmatched += 1
    return {
        "frames_compared": len(owlv2_by_frame),
        "gemini_boxes": gemini_total,
        "gemini_box_agreement_fraction": gemini_matched / gemini_total if gemini_total else None,
        "owlv2_boxes_score_ge_0_3": owl_total,
        "owlv2_boxes_without_gemini_match_fraction": owl_unmatched / owl_total if owl_total else None,
    }


def sequence_frame_count(seq_dir: Path) -> int:
    parser = configparser.ConfigParser()
    parser.read(seq_dir / "seqinfo.ini")
    return int(parser["Sequence"]["seqLength"])


def run(root: Path) -> dict[str, Any]:
    sequences = sorted(path for path in root.iterdir() if (path / "seqinfo.ini").exists())
    total_frames = sum(sequence_frame_count(path) for path in sequences)
    processor, model, torch = load_model()
    prompts = list(dict.fromkeys([name.replace("_", " ") for name in GEMINI_LABELS] + ["ship", "boat", "vessel"]))
    first_seq = sequences[0]
    warmup_count = min(5, sequence_frame_count(first_seq))
    timings = []
    warmup = {}
    for frame in range(1, warmup_count + 1):
        start = time.monotonic()
        warmup[frame] = detect(
            first_seq / "img1" / f"{frame:06d}.jpg", processor, model, torch, prompts
        )
        timings.append(time.monotonic() - start)
    average = sum(timings) / len(timings)
    estimate_seconds = average * total_frames
    stride = 2 if estimate_seconds > 90 * 60 else 1
    print(
        f"OWLv2 CPU estimate: {estimate_seconds / 60:.1f} min for {total_frames} frames; "
        f"using stride={stride}"
    )
    summaries = {}
    for seq_dir in sequences:
        frame_count = sequence_frame_count(seq_dir)
        teacher_dir = seq_dir / "teacher"
        teacher_dir.mkdir(parents=True, exist_ok=True)
        frames = range(1, frame_count + 1, stride)
        rows = []
        by_frame = {}
        for frame in frames:
            if seq_dir == first_seq and frame in warmup:
                detections = warmup[frame]
            else:
                detections = detect(
                    seq_dir / "img1" / f"{frame:06d}.jpg",
                    processor,
                    model,
                    torch,
                    prompts,
                )
            by_frame[frame] = detections
            rows.append(
                json.dumps(
                    {"frame": frame, "stride": stride, "detections": detections},
                    separators=(",", ":"),
                )
            )
        (teacher_dir / "owlv2_dets.jsonl").write_text("\n".join(rows) + "\n", encoding="utf-8")
        metadata = {
            "model": MODEL,
            "device": "cpu",
            "stride": stride,
            "score_threshold": 0.2,
            "prompts": prompts,
            "full_run_eta_minutes": estimate_seconds / 60,
        }
        (teacher_dir / "owlv2_meta.json").write_text(json.dumps(metadata, indent=2) + "\n")
        result = compare(read_gemini(seq_dir), by_frame)
        summaries[seq_dir.name] = {**metadata, **result}
        print(
            f"{seq_dir.name}: agreement={result['gemini_box_agreement_fraction']} "
            f"owlv2_unmatched={result['owlv2_boxes_without_gemini_match_fraction']} "
            f"frames={result['frames_compared']}"
        )
    summary = {"model": MODEL, "device": "cpu", "stride": stride, "sequences": summaries}
    (root / "owlv2_summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    return summary


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path("~/eval/ghost_fleet_r2_1fps").expanduser())
    args = parser.parse_args()
    run(args.root.expanduser())


if __name__ == "__main__":
    main()
