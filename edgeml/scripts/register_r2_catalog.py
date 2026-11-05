#!/usr/bin/env python3
"""Prepare or register the round-2 set using the dataset catalog schemas."""

from __future__ import annotations

import argparse
import hashlib
import json
import random
import subprocess
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

BUCKET = "qwiklabs-gcp-02-34a870014566-ghost-seeker"
DATASET_ID = "edgeml_ghost_fleet_r2_1fps"
DATASET_FIELDS = (
    "dataset_id", "name", "source_type", "source_url", "source_revision", "license",
    "viewpoint", "modality", "synthetic", "annotation_format", "native_classes",
    "num_images", "num_annotations", "bytes", "raw_prefix", "images_prefix",
    "ingested_at", "ingest_script", "notes",
)
IMAGE_FIELDS = (
    "image_id", "dataset_id", "gcs_uri", "split", "width", "height", "bytes",
    "sha256", "source_ref", "timestamp_s", "has_annotations",
)
ANNOTATION_FIELDS = (
    "image_id", "dataset_id", "label", "label_norm", "box", "conf", "source", "attrs",
)


def normalized(row: dict[str, Any], fields: tuple[str, ...]) -> dict[str, Any]:
    return {field: row.get(field) for field in fields}


def write_jsonl(path: Path, rows: list[dict[str, Any]], fields: tuple[str, ...]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as stream:
        for row in rows:
            stream.write(
                json.dumps(
                    normalized(row, fields),
                    ensure_ascii=False,
                    separators=(",", ":"),
                )
                + "\n"
            )


def jsonl(path: Path) -> list[dict[str, Any]]:
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def gcloud(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["gcloud", *args], check=check, capture_output=True, text=True)


def catalog_rows(root: Path) -> tuple[dict[str, Any], list[dict[str, Any]], list[dict[str, Any]]]:
    sequences = sorted(path for path in root.iterdir() if (path / "seqinfo.ini").exists())
    if not sequences:
        raise ValueError(f"No MOT sequences found under {root}")
    index = {row["seq_id"]: row for row in jsonl(root / "index.jsonl")}
    image_rows, annotation_rows = [], []
    total_bytes = 0
    source_names = set()
    class_names = set()
    licenses = set()
    for seq_dir in sequences:
        info = {}
        for line in (seq_dir / "seqinfo.ini").read_text(encoding="utf-8").splitlines():
            if "=" in line and not line.lstrip().startswith("["):
                key, value = line.split("=", 1)
                info[key.strip()] = value.strip()
        source_name = info["source_dataset"]
        source_names.add(source_name)
        if info.get("license"):
            licenses.add(info["license"])
        labels = json.loads((seq_dir / "gt" / "labels.json").read_text(encoding="utf-8"))
        classes = labels["classes"]
        class_names.update(classes.values())
        attrs_by_frame: dict[int, list[dict[str, Any]]] = {}
        for row in jsonl(seq_dir / "gt" / "attrs.jsonl"):
            attrs_by_frame.setdefault(int(row["frame"]), []).append(row)
        map_rows = json.loads((seq_dir / "frame_map.json").read_text(encoding="utf-8"))
        gt_by_frame: dict[int, list[list[str]]] = {}
        gt_path = seq_dir / "gt" / "gt.txt"
        for line in gt_path.read_text(encoding="utf-8").splitlines():
            fields = line.split(",")
            gt_by_frame.setdefault(int(fields[0]), []).append(fields)
        count = int(info["seqLength"])
        for frame in range(1, count + 1):
            image_path = seq_dir / "img1" / f"{frame:06d}.jpg"
            image_bytes = image_path.stat().st_size
            total_bytes += image_bytes
            source_file = map_rows[str(frame)]["source_frame"]
            source_second = int(source_file.removeprefix("sec_").removesuffix(".jpg"))
            image_id = f"{DATASET_ID}/dev/{seq_dir.name}_{frame:06d}"
            image_rows.append(
                {
                    "image_id": image_id,
                    "dataset_id": DATASET_ID,
                    "gcs_uri": (
                        f"gs://{BUCKET}/edgeml/eval/{root.name}/{seq_dir.name}/"
                        f"img1/{frame:06d}.jpg"
                    ),
                    "split": "dev",
                    "width": int(info["imWidth"]),
                    "height": int(info["imHeight"]),
                    "bytes": image_bytes,
                    "sha256": sha256_file(image_path),
                    "source_ref": f"{source_name}@{source_second}",
                    "timestamp_s": float(source_second),
                    "has_annotations": bool(gt_by_frame.get(frame)),
                }
            )
            attr_by_id = {int(row["id"]): row for row in attrs_by_frame.get(frame, [])}
            for gt in gt_by_frame.get(frame, []):
                _, track_id, x, y, width, height, _conf, class_id, _visibility = gt
                label = classes[str(int(float(class_id)))]
                class_id_int = int(float(class_id))
                attr = attr_by_id.get(int(track_id), {})
                box_label_source = attr.get(
                    "label_source", labels.get("label_source", "teacher")
                )
                annotation_rows.append(
                    {
                        "image_id": image_id,
                        "dataset_id": DATASET_ID,
                        "label": label,
                        "label_norm": None,
                        "box": [float(x), float(y), float(width), float(height)],
                        "conf": float(attr.get("confidence", 1.0)),
                        "source": (
                            "model:gemini-2.5-flash+human"
                            if box_label_source == "teacher+human"
                            else "model:gemini-2.5-flash"
                        ),
                        "attrs": json.dumps(
                            {
                                "track_id": int(track_id),
                                "visibility": float(_visibility),
                                "visibility_label": attr.get("visibility"),
                                "lights": attr.get("lights", []),
                                "nav_lights_visible": attr.get("nav_lights_visible"),
                                "approaching": attr.get("approaching"),
                                "wake": attr.get("wake"),
                                "label_source": box_label_source,
                                "class_id": class_id_int,
                            },
                            ensure_ascii=False,
                            separators=(",", ":"),
                        ),
                    }
                )
    dataset_row = {
        "dataset_id": DATASET_ID,
        "name": "Ghost Fleet annotation round 2 (1 fps)",
        "source_type": "youtube",
        "source_url": None,
        "source_revision": None,
        "license": "mixed; see per-sequence seqinfo.ini" if len(licenses) > 1 else next(iter(licenses), "unknown"),
        "viewpoint": "mixed",
        "modality": "rgb",
        "synthetic": True,
        "annotation_format": "mot",
        "native_classes": sorted(class_names),
        "num_images": len(image_rows),
        "num_annotations": len(annotation_rows),
        "bytes": total_bytes,
        "raw_prefix": None,
        "images_prefix": f"gs://{BUCKET}/edgeml/eval/{root.name}/",
        "ingested_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "ingest_script": "edgeml/scripts/register_r2_catalog.py",
        "notes": "Multiple video-frame sources: " + ", ".join(sorted(source_names)),
    }
    return dataset_row, image_rows, annotation_rows


def read_remote_datasets() -> list[dict[str, Any]]:
    uri = f"gs://{BUCKET}/catalog/datasets.jsonl"
    result = gcloud("storage", "cat", uri, check=False)
    if result.returncode:
        error = result.stderr.lower()
        if any(marker in error for marker in ("matched no objects", "no urls matched", "404", "not found")):
            return []
        raise RuntimeError(f"Cannot read {uri}: {result.stderr.strip()}")
    return [json.loads(line) for line in result.stdout.splitlines() if line.strip()]


def upsert_simulation(rows: list[dict[str, Any]], row: dict[str, Any]) -> list[dict[str, Any]]:
    output = []
    replaced = False
    for existing in rows:
        if existing.get("dataset_id") == row["dataset_id"]:
            if not replaced:
                output.append(normalized(row, DATASET_FIELDS))
                replaced = True
        else:
            output.append(normalized(existing, DATASET_FIELDS))
    if not replaced:
        output.append(normalized(row, DATASET_FIELDS))
    return output


def remote_generation(uri: str) -> int:
    result = gcloud(
        "storage", "objects", "describe", uri, "--format=value(generation)", check=False
    )
    if result.returncode:
        if any(marker in result.stderr.lower() for marker in ("404", "not found", "matched no objects")):
            return 0
        raise RuntimeError(f"Cannot read generation for {uri}: {result.stderr.strip()}")
    return int(result.stdout.strip())


def upload_and_upsert(
    output_dir: Path,
    dataset_row: dict[str, Any],
    image_path: Path,
    annotation_path: Path,
) -> None:
    gcloud(
        "storage", "cp", str(image_path),
        f"gs://{BUCKET}/catalog/images/{DATASET_ID}.jsonl",
    )
    gcloud(
        "storage", "cp", str(annotation_path),
        f"gs://{BUCKET}/catalog/annotations/{DATASET_ID}.jsonl",
    )
    uri = f"gs://{BUCKET}/catalog/datasets.jsonl"
    for attempt in range(5):
        generation = remote_generation(uri)
        current = read_remote_datasets()
        updated = upsert_simulation(current, dataset_row)
        with tempfile.TemporaryDirectory(prefix="ghost-seeker-catalog-") as directory:
            path = Path(directory) / "datasets.jsonl"
            write_jsonl(path, updated, DATASET_FIELDS)
            result = gcloud(
                "storage", "cp", f"--if-generation-match={generation}", str(path), uri, check=False
            )
        if result.returncode == 0:
            return
        if "412" not in result.stderr and "precondition" not in result.stderr.lower():
            raise RuntimeError(f"Cannot update {uri}: {result.stderr.strip()}")
        if attempt < 4:
            time.sleep(random.uniform(0.1, 0.4))
    raise RuntimeError("Dataset catalog changed during all 5 write attempts")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path("~/eval/ghost_fleet_r2_1fps").expanduser())
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    root = args.root.expanduser()
    dataset_row, image_rows, annotation_rows = catalog_rows(root)
    export_dir = root / "catalog"
    image_path = export_dir / "images" / f"{DATASET_ID}.jsonl"
    annotation_path = export_dir / "annotations" / f"{DATASET_ID}.jsonl"
    write_jsonl(image_path, image_rows, IMAGE_FIELDS)
    write_jsonl(annotation_path, annotation_rows, ANNOTATION_FIELDS)
    before = read_remote_datasets()
    after = upsert_simulation(before, dataset_row)
    mode = "dry-run" if args.dry_run else "write"
    print(
        f"{mode} dataset_id={DATASET_ID} images={len(image_rows)} "
        f"annotations={len(annotation_rows)} dataset_rows={len(before)}->{len(after)} "
        f"existing_dataset_rows={sum(row.get('dataset_id') == DATASET_ID for row in before)}"
    )
    print(f"images: {image_path}")
    print(f"annotations: {annotation_path}")
    print(f"dataset row: {json.dumps(normalized(dataset_row, DATASET_FIELDS), ensure_ascii=False)}")
    if args.dry_run:
        print("no GCS catalog files were written")
        return
    upload_and_upsert(export_dir, dataset_row, image_path, annotation_path)
    print("catalog files uploaded and dataset row upserted")


if __name__ == "__main__":
    main()
