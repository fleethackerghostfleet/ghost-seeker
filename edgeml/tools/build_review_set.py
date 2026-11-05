#!/usr/bin/env python3
"""Build the dev-only clean-v2 Operator Review image set."""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import shutil
import subprocess
import time
from collections import defaultdict
from pathlib import Path, PurePosixPath

import cv2
import numpy as np
import yaml

from edgeml.embed.shiptype import TYPE_TO_CATALOG
from edgeml.runtime.detectors import MARITIME_KEEP, load_detector

REPO = Path(__file__).resolve().parents[2]
DEFAULT_SELECTION = REPO / "datasets/review/clean_v2_dev_review.jsonl"
DEFAULT_SPLITS = REPO / "edgeml/configs/splits.yaml"
DEFAULT_OUTPUT = REPO / "data/review_set"
DEFAULT_DETECTOR = Path("~/models/D/yolo11n-640/model.onnx")
DEFAULT_EMBED = Path("~/models/D/mobileclip-s1")
EDGE_SCORE_THRESHOLD = 0.35
UI_WIDTH, UI_HEIGHT = 480, 300

DETECTOR_CLASS_MAP = {
    "boat": "vessel",
    "airplane": "aircraft",
    "bird": "bird",
    "person": "person",
    "surfboard": "unknown",
    "kite": "unknown",
}


def _normalized_video(value: object) -> str:
    value = Path(str(value or "")).stem
    value = re.sub(r"^video_", "", value, flags=re.IGNORECASE)
    value = re.sub(r"(?:_seg.*|_f\d+|_frame_?\d+)$", "", value, flags=re.IGNORECASE)
    return re.sub(r"[^a-z0-9]", "", value.lower())


def _split_keys(values: object) -> set[str]:
    if not isinstance(values, dict):
        return set()
    return {
        _normalized_video(value)
        for records in values.values()
        if isinstance(records, list)
        for value in records
    }


def selection_guard(rows: list[dict], splits: dict) -> None:
    locked = {
        "test": _split_keys(splits.get("test")),
        "quarantine": _split_keys(splits.get("quarantine")),
    }
    aliases = {
        _normalized_video(source): _normalized_video(target)
        for source, target in (splits.get("source_video_aliases") or {}).items()
    }
    violations = []
    for row in rows:
        image_id = str(row.get("image_id") or "")
        dataset_id = str(row.get("dataset_id") or "")
        if row.get("eval_split") != "dev":
            violations.append(f"{image_id}: eval_split is not 'dev'")
        if dataset_id.lower() == "singapore_maritime":
            violations.append(f"{image_id}: singapore_maritime is excluded")

        names = [str(row.get("source_video") or "")]
        if dataset_id.lower().startswith("video_"):
            names.append(dataset_id)
        names.extend(image_id.split("/"))
        for name in names:
            key = _normalized_video(name)
            key = aliases.get(key, key)
            for split in ("test", "quarantine"):
                if key and key in locked[split]:
                    violations.append(f"{image_id}: source {name!r} is in {split}")
    if violations:
        raise ValueError("selection contains locked or non-dev rows:\n" + "\n".join(violations))


def map_detector_class(label: str, ship_type: str | None = None) -> str:
    if label == "boat":
        return TYPE_TO_CATALOG.get(ship_type or "", "vessel")
    return DETECTOR_CLASS_MAP.get(label, "unknown")


def read_selection(path: Path) -> list[dict]:
    rows = [json.loads(line) for line in path.read_text().splitlines() if line.strip()]
    if not rows:
        raise ValueError(f"selection is empty: {path}")
    return rows


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def _safe_part(value: str) -> str:
    part = re.sub(r"[^A-Za-z0-9_.-]+", "_", value).strip("._")
    return part or "image"


def _relative_file(row: dict) -> PurePosixPath:
    uri = str(row["gcs_uri"])
    suffix = Path(uri.split("?", 1)[0]).suffix.lower() or ".jpg"
    image_id = _safe_part(str(row["image_id"]))
    dataset_id = _safe_part(str(row["dataset_id"]))
    return PurePosixPath(dataset_id, image_id + suffix)


def download_images(rows: list[dict], image_dir: Path, gsutil: str) -> None:
    groups: dict[str, list[dict]] = defaultdict(list)
    for row in rows:
        groups[str(row["dataset_id"])].append(row)

    stage_root = image_dir.parent / ".review-download"
    if stage_root.exists():
        shutil.rmtree(stage_root)
    stage_root.mkdir(parents=True)
    try:
        for dataset_id, group in sorted(groups.items()):
            staging = stage_root / _safe_part(dataset_id)
            staging.mkdir()
            missing = []
            for row in group:
                target = image_dir / _relative_file(row)
                if target.is_file() and _sha256(target) == row["sha256"]:
                    continue
                missing.append(row)
            if missing:
                subprocess.run(
                    [gsutil, "-m", "cp", "-n", *[row["gcs_uri"] for row in missing], str(staging)],
                    check=True,
                )
            for row in missing:
                source = staging / Path(str(row["gcs_uri"]).split("?", 1)[0]).name
                if not source.is_file():
                    raise FileNotFoundError(f"gsutil did not create {source}")
                if _sha256(source) != row["sha256"]:
                    raise ValueError(f"SHA-256 mismatch for {row['image_id']}")
                target = image_dir / _relative_file(row)
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source, target)
                if _sha256(target) != row["sha256"]:
                    raise ValueError(f"SHA-256 mismatch after copy for {row['image_id']}")
    finally:
        shutil.rmtree(stage_root, ignore_errors=True)


def _ship_classifier(embed_dir: Path, threads: int):
    from edgeml.embed.clip_embedder import ClipEmbedder
    from edgeml.embed.shiptype import ShipTypeClassifier

    embedder = ClipEmbedder("mobileclip-s1", backend="ort", onnx_dir=embed_dir, threads=threads)
    try:
        return ShipTypeClassifier(embedder)
    except RuntimeError as error:
        if "prompts missing from cache" not in str(error):
            raise
    embedder = ClipEmbedder("mobileclip-s1", backend="torch", threads=threads)
    return ShipTypeClassifier(embedder)


def build_manifest(
    rows: list[dict],
    image_dir: Path,
    detector_path: Path,
    embed_dir: Path | None,
    threads: int,
    ship_typing_limit: float,
) -> tuple[list[dict], dict]:
    detector_started = time.monotonic()
    detector = load_detector(
        detector_path.expanduser(), engine="ort", threads=threads, score_thr=0.05
    )
    records = []
    crops = []
    crop_refs = []
    class_counts: dict[str, int] = defaultdict(int)
    person_images = set()

    for row_index, row in enumerate(rows):
        image_path = image_dir / _relative_file(row)
        image = cv2.imdecode(np.fromfile(image_path, dtype=np.uint8), cv2.IMREAD_COLOR)
        if image is None:
            raise ValueError(f"cannot decode {image_path}")
        height, width = image.shape[:2]
        expected = (int(row["width"]), int(row["height"]))
        if (width, height) != expected:
            raise ValueError(f"{row['image_id']}: decoded size {(width, height)} != selection {expected}")
        detections = []
        for detection in detector.detect(image):
            if detection.label not in MARITIME_KEEP or detection.score < EDGE_SCORE_THRESHOLD:
                continue
            x, y, box_width, box_height = (float(v) for v in detection.box)
            entry = {
                "cls": map_detector_class(detection.label),
                "box": {
                    "x": x * UI_WIDTH / width,
                    "y": y * UI_HEIGHT / height,
                    "w": box_width * UI_WIDTH / width,
                    "h": box_height * UI_HEIGHT / height,
                },
                "conf": float(detection.score),
                "_label": detection.label,
            }
            if detection.label == "boat":
                entry["ship"] = {"type": "unknown", "label": "Unclassified vessel", "aspect": "unknown"}
                from edgeml.embed.shiptype import crop_box

                crops.append(crop_box(image, detection.box))
                crop_refs.append((row_index, len(detections)))
            if entry["cls"] == "person":
                person_images.add(row_index)
            class_counts[entry["cls"]] += 1
            detections.append(entry)
        records.append({
            "image_id": row["image_id"],
            "dataset_id": row["dataset_id"],
            "source_video": row["source_video"],
            "sha256": row["sha256"],
            "file": _relative_file(row).as_posix(),
            "width": width,
            "height": height,
            "dets": detections,
        })

    detector_runtime = time.monotonic() - detector_started
    ship_status = "disabled"
    typed_boats = 0
    ship_started = time.monotonic() if embed_dir and crops else None
    if embed_dir and crops:
        try:
            classifier = _ship_classifier(embed_dir.expanduser(), threads)
        except (ImportError, RuntimeError, FileNotFoundError) as error:
            classifier = None
            ship_status = f"skipped: {error}"
        if classifier is not None:
            started = time.monotonic()
            predictions = []
            batch_size = 32
            for start in range(0, len(crops), batch_size):
                predictions.extend(classifier.classify(crops[start:start + batch_size]))
                elapsed = time.monotonic() - started
                if len(predictions) == batch_size and elapsed / len(predictions) * len(crops) > ship_typing_limit:
                    predictions = []
                    ship_status = f"skipped: estimated CPU runtime exceeds {ship_typing_limit:.0f}s"
                    break
            if predictions or not crops:
                ship_status = "completed"
                typed_boats = len(predictions)
                for (row_index, detection_index), prediction in zip(crop_refs, predictions):
                    detection = records[row_index]["dets"][detection_index]
                    detection["cls"] = map_detector_class("boat", prediction["type"])
                    detection["ship"] = prediction
                    class_counts["vessel"] -= 1
                    class_counts[detection["cls"]] += 1
    ship_runtime = time.monotonic() - ship_started if ship_started is not None else 0.0
    for record in records:
        for detection in record["dets"]:
            detection.pop("_label", None)

    stats = {
        "detections_by_class": dict(sorted(class_counts.items())),
        "images_with_person_detection": len(person_images),
        "ship_typing": ship_status,
        "typed_boat_detections": typed_boats,
        "detector_runtime_seconds": round(detector_runtime, 2),
        "ship_typing_runtime_seconds": round(ship_runtime, 2),
    }
    return records, stats


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--selection", type=Path, default=DEFAULT_SELECTION)
    parser.add_argument("--splits", type=Path, default=DEFAULT_SPLITS)
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--images", type=Path)
    parser.add_argument("--manifest", type=Path)
    parser.add_argument("--detector", type=Path, default=DEFAULT_DETECTOR)
    parser.add_argument("--embed", type=Path, default=DEFAULT_EMBED)
    parser.add_argument("--threads", type=int, default=4)
    parser.add_argument("--skip-ship-typing", action="store_true")
    parser.add_argument("--ship-typing-limit-seconds", type=float, default=3600)
    args = parser.parse_args(argv)

    rows = read_selection(args.selection)
    selection_guard(rows, yaml.safe_load(args.splits.read_text()))
    image_dir = args.images or args.output / "images"
    manifest_path = args.manifest or args.output / "manifest.json"
    gsutil = shutil.which("gsutil") or str(Path.home() / "google-cloud-sdk/bin/gsutil")
    download_images(rows, image_dir, gsutil)
    manifest, stats = build_manifest(
        rows,
        image_dir,
        args.detector,
        None if args.skip_ship_typing else args.embed,
        args.threads,
        args.ship_typing_limit_seconds,
    )
    manifest_path.parent.mkdir(parents=True, exist_ok=True)
    manifest_path.write_text(json.dumps(manifest, indent=2, allow_nan=False) + "\n")
    print(json.dumps({"images": len(manifest), "manifest": str(manifest_path), **stats}, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
