#!/usr/bin/env python3
import argparse
import io
import json
import shutil
import tempfile
from collections import defaultdict
from datetime import datetime, timezone
from pathlib import Path, PurePosixPath

import yaml
from datasets import Image as HFImage
from datasets import load_dataset
from huggingface_hub import HfApi, snapshot_download
from PIL import Image, ImageDraw

from common import (
    convert_to_jpeg,
    ensure_raw_matches_or_upload,
    gcs_uri,
    image_size,
    load_yolo_boxes,
    sha256_file,
    upload_directory,
    write_catalog,
)


REPO_ROOT = Path(__file__).resolve().parents[2]
REGISTRY_PATH = REPO_ROOT / "datasets" / "registry.yaml"
IMAGE_SUFFIXES = {".jpg", ".jpeg", ".png", ".bmp", ".tif", ".tiff", ".webp"}
SPLITS = ("train", "validation", "valid", "val", "test")


def _timestamp():
    return datetime.now(timezone.utc).isoformat()


def _json(value):
    return json.dumps(value, separators=(",", ":"), ensure_ascii=False, default=str)


def _entry(dataset_id):
    registry = yaml.safe_load(REGISTRY_PATH.read_text(encoding="utf-8"))
    for entry in registry:
        if entry["dataset_id"] == dataset_id:
            return entry
    raise KeyError(f"Unknown dataset_id in registry: {dataset_id}")


def _split_for_path(path):
    parts = [part.lower() for part in path.parts]
    for split in SPLITS:
        if split in parts:
            return split
    filename = path.name.lower()
    for split in SPLITS:
        if filename.startswith(f"{split}-") or filename.startswith(f"{split}_"):
            return split
    return "all"


def _image_files(root):
    return [
        path
        for path in root.rglob("*")
        if path.is_file() and path.suffix.lower() in IMAGE_SUFFIXES
    ]


def _resolve_image(root, annotation_path, file_name, files):
    relative = PurePosixPath(str(file_name))
    candidates = (
        root / Path(*relative.parts),
        annotation_path.parent / Path(*relative.parts),
        annotation_path.parent.parent / Path(*relative.parts),
        root / "images" / Path(*relative.parts),
    )
    for candidate in candidates:
        if candidate.is_file():
            return candidate.resolve()
    suffix = relative.as_posix()
    matches = [
        path
        for path in files
        if path.name == relative.name or path.as_posix().endswith(suffix)
    ]
    if len(matches) == 1:
        return matches[0].resolve()
    raise FileNotFoundError(
        f"Could not uniquely resolve COCO image {file_name!r} from {annotation_path}"
    )


def _json_coco_files(root):
    result = []
    for path in root.rglob("*.json"):
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            continue
        if all(key in data for key in ("images", "annotations", "categories")):
            result.append((path, data))
    return result


def _coco_annotations(root, ready_root, dataset_id):
    files = _image_files(root)
    images = {}
    annotations = defaultdict(list)
    classes = []
    for annotation_path, data in _json_coco_files(root):
        categories = {item["id"]: item["name"] for item in data["categories"]}
        for name in categories.values():
            if name not in classes:
                classes.append(name)
        image_keys = {}
        for item in data["images"]:
            source = _resolve_image(root, annotation_path, item["file_name"], files)
            split = _split_for_path(PurePosixPath(item["file_name"]))
            if split == "all":
                split = _split_for_path(source.relative_to(root))
            key = (split, source)
            image_keys[item["id"]] = key
            images.setdefault(
                key,
                {
                    "path": source,
                    "split": split,
                    "source_ref": item["file_name"],
                    "stem": Path(item["file_name"]).stem,
                },
            )
        for item in data["annotations"]:
            key = image_keys.get(item["image_id"])
            if key is None:
                continue
            attrs = {
                field: value
                for field, value in item.items()
                if field not in ("image_id", "bbox", "category_id")
            }
            annotations[key].append(
                {
                    "label": categories[item["category_id"]],
                    "box": item["bbox"],
                    "attrs": _json(attrs),
                }
            )
    return (
        _write_records(images, annotations, classes, ready_root, dataset_id),
        "coco",
    )


def _yolo_config(root):
    for path in sorted(root.rglob("*.yaml")) + sorted(root.rglob("*.yml")):
        try:
            data = yaml.safe_load(path.read_text(encoding="utf-8"))
        except (OSError, yaml.YAMLError):
            continue
        if isinstance(data, dict) and "names" in data and any(
            split in data for split in ("train", "val", "test")
        ):
            return path, data
    return None, None


def _class_names(names):
    if isinstance(names, dict):
        return [str(names[key]) for key in sorted(names, key=lambda key: int(key))]
    return [str(name) for name in names]


def _yolo_image_paths(config_path, config, root):
    config_root = Path(config.get("path", "."))
    if not config_root.is_absolute():
        config_root = (config_path.parent / config_root).resolve()
    result = []
    for split_key in ("train", "val", "test"):
        value = config.get(split_key)
        if not value:
            continue
        paths = value if isinstance(value, list) else [value]
        for value_path in paths:
            candidate = Path(value_path)
            if not candidate.is_absolute():
                options = (config_root / candidate, config_path.parent / candidate, root / candidate)
                candidate = next((option for option in options if option.exists()), options[0])
            if candidate.is_dir():
                found = sorted(
                    path
                    for path in candidate.rglob("*")
                    if path.is_file() and path.suffix.lower() in IMAGE_SUFFIXES
                )
            elif candidate.is_file() and candidate.suffix.lower() in IMAGE_SUFFIXES:
                found = [candidate]
            elif candidate.is_file():
                found = [
                    Path(line.strip())
                    for line in candidate.read_text(encoding="utf-8").splitlines()
                    if line.strip()
                ]
                found = [
                    path if path.is_absolute() else (candidate.parent / path).resolve()
                    for path in found
                ]
            else:
                found = []
            result.extend((split_key, path.resolve()) for path in found if path.is_file())
    return result


def _yolo_label_path(image_path, root):
    parts = list(image_path.parts)
    if "images" in parts:
        parts[parts.index("images")] = "labels"
        candidate = Path(*parts).with_suffix(".txt")
        if candidate.is_file():
            return candidate
    candidates = (
        image_path.parent.parent / "labels" / image_path.parent.name / f"{image_path.stem}.txt",
        image_path.parent.parent / "labels" / f"{image_path.stem}.txt",
        root / "labels" / f"{image_path.stem}.txt",
    )
    return next((path for path in candidates if path.is_file()), candidates[0])


def _yolo_annotations(root, ready_root, dataset_id):
    config_path, config = _yolo_config(root)
    if config is None:
        return None
    classes = _class_names(config["names"])
    images = {}
    annotations = defaultdict(list)
    for split, source in _yolo_image_paths(config_path, config, root):
        key = (split, source)
        images[key] = {
            "path": source,
            "split": split,
            "source_ref": source.relative_to(root).as_posix(),
            "stem": source.stem,
        }
        with Image.open(source) as image:
            width, height = image.size
        for item in load_yolo_boxes(
            _yolo_label_path(source, root), classes, width, height
        ):
            annotations[key].append(
                {
                    "label": item["label"],
                    "box": item["box"],
                    "attrs": item["attrs"],
                }
            )
    return (
        _write_records(images, annotations, classes, ready_root, dataset_id),
        "yolo",
    )


def _parquet_objects(objects):
    if not objects:
        return []
    if isinstance(objects, list):
        rows = objects
    elif isinstance(objects, dict):
        boxes = objects.get("bbox", objects.get("boxes", [])) or []
        labels = objects.get("category", objects.get("label", objects.get("name", [])))
        if not isinstance(labels, (list, tuple)):
            labels = [labels] * len(boxes)
        rows = []
        for index, box in enumerate(boxes):
            item = {
                key: values[index]
                for key, values in objects.items()
                if key not in ("bbox", "boxes", "category", "label", "name")
                and isinstance(values, (list, tuple))
                and index < len(values)
            }
            rows.append(
                {
                    **item,
                    "bbox": box,
                    "category": labels[index] if index < len(labels) else None,
                }
            )
    else:
        return []
    result = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        box = row.get("bbox", row.get("bbox_xywh", row.get("box")))
        label = row.get(
            "category", row.get("label", row.get("name", row.get("category_name")))
        )
        if box is None or label is None:
            continue
        attrs = {
            key: value
            for key, value in row.items()
            if key not in ("bbox", "bbox_xywh", "box", "category", "label", "name")
        }
        result.append({"label": str(label), "box": list(box), "attrs": _json(attrs)})
    return result


def _parquet_files(root):
    result = defaultdict(list)
    for path in sorted(root.rglob("*.parquet")):
        split = _split_for_path(path.relative_to(root))
        if split == "all":
            continue
        result[split].append(path)
    return result


def _materialize_hf_image(value, destination):
    if isinstance(value, dict):
        image_bytes = value.get("bytes")
        if image_bytes is not None:
            return convert_to_jpeg(image_bytes, destination)
        image_path = value.get("path")
        if image_path:
            return convert_to_jpeg(image_path, destination)
    if isinstance(value, (bytes, bytearray, memoryview)):
        return convert_to_jpeg(value, destination)
    if isinstance(value, Image.Image):
        buffer = io.BytesIO()
        value.save(buffer, format="JPEG")
        return convert_to_jpeg(buffer.getvalue(), destination)
    raise ValueError(f"Unsupported Hugging Face image value: {type(value).__name__}")


def _parquet_annotations(root, ready_root, cache_dir, dataset_id):
    files_by_split = _parquet_files(root)
    if not files_by_split:
        return None
    images = {}
    annotations = defaultdict(list)
    classes = []
    for split, paths in files_by_split.items():
        dataset = load_dataset(
            "parquet",
            data_files={split: [str(path) for path in paths]},
            split=split,
            cache_dir=str(cache_dir),
        )
        if "image" not in dataset.column_names:
            raise ValueError(f"HF parquet split {split} has no image column")
        try:
            dataset = dataset.cast_column("image", HFImage(decode=False))
        except (TypeError, ValueError):
            pass
        for index, row in enumerate(dataset):
            source_id = row.get("image_id", index)
            stem = Path(str(source_id)).stem or str(index)
            destination = ready_root / "images" / split / f"{stem}.jpg"
            _materialize_hf_image(row["image"], destination)
            key = (split, destination)
            images[key] = {
                "path": destination,
                "split": split,
                "source_ref": f"{split}/{source_id}",
                "stem": stem,
            }
            for annotation in _parquet_objects(row.get("objects")):
                if annotation["label"] not in classes:
                    classes.append(annotation["label"])
                annotations[key].append(annotation)
    return (
        _write_records(
            images, annotations, classes, ready_root, dataset_id, already_ready=True
        ),
        "hf-parquet image+objects",
    )


def _write_records(
    images, annotations, classes, ready_root, dataset_id, already_ready=False
):
    image_rows = []
    annotation_rows = []
    path_by_image_id = {}
    seen_image_ids = set()
    for key, image in images.items():
        split = image["split"]
        source_path = image["path"]
        if already_ready:
            output_path = source_path
        else:
            output_path = ready_root / "images" / split / f"{image['stem']}.jpg"
            convert_to_jpeg(source_path, output_path)
        width, height = image_size(output_path)
        image_id = f"{dataset_id}/{split}/{image['stem']}"
        if image_id in seen_image_ids:
            raise ValueError(f"Duplicate image ID generated: {image_id}")
        seen_image_ids.add(image_id)
        object_rows = annotations.get(key, [])
        image_rows.append(
            {
                "image_id": image_id,
                "dataset_id": dataset_id,
                "gcs_uri": gcs_uri(
                    f"datasets/{dataset_id}/images/{split}/{output_path.name}"
                ),
                "split": split,
                "width": width,
                "height": height,
                "bytes": output_path.stat().st_size,
                "sha256": sha256_file(output_path),
                "source_ref": image["source_ref"],
                "timestamp_s": None,
                "has_annotations": bool(object_rows),
            }
        )
        path_by_image_id[image_id] = output_path
        for annotation in object_rows:
            box = annotation["box"]
            if len(box) != 4:
                raise ValueError(f"Expected four-value box for {image_id}: {box}")
            annotation_rows.append(
                {
                    "image_id": image_id,
                    "dataset_id": dataset_id,
                    "label": annotation["label"],
                    "label_norm": None,
                    "box": [float(value) for value in box],
                    "conf": 1.0,
                    "source": "gt",
                    "attrs": annotation["attrs"],
                }
            )
            if annotation["label"] not in classes:
                classes.append(annotation["label"])
    return {
        "images": image_rows,
        "annotations": annotation_rows,
        "classes": classes,
        "path_by_image_id": path_by_image_id,
    }


def _save_overlay(dataset_id, annotation_rows, path_by_image_id):
    if not annotation_rows:
        return None
    selected = next(
        row for row in annotation_rows if len(row.get("box") or []) == 4
    )
    image_id = selected["image_id"]
    source = path_by_image_id[image_id]
    with Image.open(source) as image:
        image = image.convert("RGB")
        draw = ImageDraw.Draw(image)
        for row in annotation_rows:
            if row["image_id"] != image_id or len(row.get("box") or []) != 4:
                continue
            x, y, width, height = row["box"]
            draw.rectangle((x, y, x + width, y + height), outline="red", width=3)
            draw.text((x, max(0, y - 12)), str(row["label"]), fill="red")
        verify_dir = Path.home() / "verify"
        verify_dir.mkdir(parents=True, exist_ok=True)
        output = verify_dir / f"{dataset_id}_{source.parent.name}_{source.stem}.png"
        image.save(output)
    return output


def ingest(dataset_id):
    entry = _entry(dataset_id)
    repository = entry.get("hf_repo")
    if not repository:
        raise ValueError(f"Registry entry {dataset_id} has no hf_repo")
    api = HfApi()
    revision = api.dataset_info(repository).sha
    raw_prefix = gcs_uri(f"raw/{dataset_id}/")

    with tempfile.TemporaryDirectory(
        prefix=f"{dataset_id}-", dir=Path.home() / "data"
    ) as temporary_directory:
        temporary_directory = Path(temporary_directory)
        snapshot = Path(
            snapshot_download(
                repo_id=repository,
                repo_type="dataset",
                revision=revision,
                local_dir=temporary_directory / "snapshot",
                max_workers=8,
            )
        )
        metadata_dir = snapshot / ".cache"
        if metadata_dir.exists():
            shutil.rmtree(metadata_dir)
        ensure_raw_matches_or_upload(snapshot, raw_prefix)

        ready_root = temporary_directory / "ready"
        cache_dir = temporary_directory / "hf-cache"
        records = None
        annotation_format = None
        coco_files = _json_coco_files(snapshot)
        if coco_files:
            records, annotation_format = _coco_annotations(
                snapshot, ready_root, dataset_id
            )
        else:
            yolo = _yolo_annotations(snapshot, ready_root, dataset_id)
            if yolo is not None:
                records, annotation_format = yolo
            else:
                parquet = _parquet_annotations(
                    snapshot, ready_root, cache_dir, dataset_id
                )
                if parquet is not None:
                    records, annotation_format = parquet
        if records is None:
            raise ValueError(
                "No supported COCO, YOLO, or HF parquet image/annotation data found"
            )
        if not records["images"]:
            raise ValueError("Dataset snapshot contains no images")

        split_counts = defaultdict(int)
        for row in records["images"]:
            split_counts[row["split"]] += 1
        upload_directory(ready_root / "images", gcs_uri(f"datasets/{dataset_id}/images/"))
        image_rows = records["images"]
        annotation_rows = records["annotations"]
        dataset_row = {
            "dataset_id": dataset_id,
            "name": entry.get("name") or dataset_id,
            "source_type": "huggingface",
            "source_url": entry.get("source_url")
            or f"https://huggingface.co/datasets/{repository}",
            "source_revision": revision,
            "license": entry.get("license") or "unknown",
            "viewpoint": entry.get("viewpoint"),
            "modality": entry.get("modality"),
            "synthetic": bool(entry.get("synthetic", False)),
            "annotation_format": annotation_format,
            "native_classes": records["classes"],
            "num_images": len(image_rows),
            "num_annotations": len(annotation_rows),
            "bytes": sum(row["bytes"] for row in image_rows),
            "raw_prefix": raw_prefix,
            "images_prefix": gcs_uri(f"datasets/{dataset_id}/images/"),
            "ingested_at": _timestamp(),
            "ingest_script": "research/datasets/ingest/hf_dataset.py",
            "notes": _json(
                {
                    "splits": dict(split_counts),
                    "annotation_format": annotation_format,
                }
            ),
        }
        write_catalog(dataset_row, image_rows, annotation_rows)
        overlay = _save_overlay(
            dataset_id, annotation_rows, records["path_by_image_id"]
        )
        print(
            f"Ingested {dataset_id}: revision={revision}, images={len(image_rows)}, "
            f"annotations={len(annotation_rows)}, splits={dict(split_counts)}"
        )
        if overlay:
            print(f"Catalog overlay: {overlay}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("dataset_id")
    args = parser.parse_args()
    (Path.home() / "data").mkdir(parents=True, exist_ok=True)
    ingest(args.dataset_id)


if __name__ == "__main__":
    main()
