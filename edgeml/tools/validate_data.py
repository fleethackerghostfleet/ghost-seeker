#!/usr/bin/env python3
"""Read-only data audit; emit clean catalogs/selection manifests into a NEW directory.

See reports/DATA_CLEANUP.md for commands, thresholds and coverage. Images in the
input repository/mirror are never changed. Near duplicates are review candidates,
not grounds for deleting temporally adjacent frames or changing locked test sets.
"""
from __future__ import annotations

import argparse
import configparser
import hashlib
import json
import math
import re
import subprocess
import zipfile
import xml.etree.ElementTree as ET
from collections import Counter, defaultdict
from pathlib import Path

import cv2
import numpy as np
import yaml

from edgeml.detectors.labels import agnostic

IMAGE_SUFFIXES = {".jpg", ".jpeg", ".png", ".bmp", ".webp"}


def read_jsonl(path):
    with Path(path).open() as stream:
        for line in stream:
            if line.strip():
                yield json.loads(line)


def write_jsonl(path, rows):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("x") as stream:
        for row in rows:
            stream.write(json.dumps(row, allow_nan=False, sort_keys=True) + "\n")


def source_video(value):
    value = Path(value).stem
    value = re.sub(r"^video_", "", value)
    value = re.sub(r"(?:_seg.*|_f\d+|_frame_?\d+)$", "", value, flags=re.I)
    return re.sub(r"[^a-z0-9]", "", value.lower())


class SplitPolicy:
    def __init__(self, config):
        self.config = config
        self.test = set()
        self.dev = set()
        self.quarantine = {source_video(v) for values in config.get("quarantine", {}).values() for v in values}
        for split in ("test", "dev"):
            for values in config.get(split, {}).values():
                if isinstance(values, list):
                    getattr(self, split).update(source_video(v) for v in values)
        self.aliases = {source_video(k): source_video(v) for k, v in config.get("source_video_aliases", {}).items()}

    def expected(self, dataset, source, upstream=None):
        video = source_video(source)
        video = self.aliases.get(video, video)
        if video in self.quarantine:
            return "quarantine"
        if dataset == "seaships7000":
            return "test" if upstream == "test" else "dev" if upstream in {"train", "valid", "validation"} else "unassigned"
        if video in self.test:
            return "test"
        if video in self.dev or dataset.startswith("video_"):
            return "dev"
        return "unassigned"


def clean_box(box, width, height):
    """Return clipped xywh, reason; fully off-image/non-finite boxes quarantine."""
    if not isinstance(box, (list, tuple)) or len(box) != 4:
        return None, "invalid_box"
    if not all(isinstance(v, (int, float)) and math.isfinite(v) for v in box):
        return None, "nonfinite_box"
    x, y, w, h = box
    if w <= 0 or h <= 0:
        return None, "zero_or_negative_area"
    x1, y1, x2, y2 = max(0, x), max(0, y), min(width, x + w), min(height, y + h)
    if x2 <= x1 or y2 <= y1:
        return None, "outside_image"
    result = [x1, y1, x2 - x1, y2 - y1]
    return result, "clip_bounds" if x < 0 or y < 0 or x + w > width or y + h > height else None


def inspect_image(data):
    array = cv2.imdecode(np.frombuffer(data, dtype=np.uint8), cv2.IMREAD_COLOR)
    if array is None:
        return {"readable": False, "sha256": hashlib.sha256(data).hexdigest()}
    gray = cv2.cvtColor(array, cv2.COLOR_BGR2GRAY)
    small = cv2.resize(gray, (32, 32), interpolation=cv2.INTER_AREA).astype(np.float32)
    coeff = cv2.dct(small)[:8, :8].flatten()
    # Ignore the DC coefficient: illumination is not a shape feature.
    bits = coeff[1:] > np.median(coeff[1:])
    phash = sum(int(bit) << i for i, bit in enumerate(bits))
    mean, std = float(gray.mean()), float(gray.std())
    return {"readable": True, "width": array.shape[1], "height": array.shape[0],
            "sha256": hashlib.sha256(data).hexdigest(), "phash": f"{phash:016x}",
            "mean": round(mean, 4), "std": round(std, 4),
            "black": float(np.percentile(small, 99)) <= 3, "blank": std < 1.0}


class HashIndex:
    """Pigeonhole Hamming index: radius r needs r+1 disjoint hash slices."""
    def __init__(self, radius=4):
        if not 0 <= radius <= 8:
            raise ValueError("near-duplicate radius must be between 0 and 8")
        self.radius = radius
        self.slices = [(i * 63 // (radius + 1), (i + 1) * 63 // (radius + 1)) for i in range(radius + 1)]
        self.buckets = defaultdict(set)

    def keys(self, value):
        for i, (lo, hi) in enumerate(self.slices):
            yield i, (value >> lo) & ((1 << (hi - lo)) - 1)

    def add(self, value):
        for key in self.keys(value):
            self.buckets[key].add(value)

    def matches(self, value):
        candidates = set()
        for key in self.keys(value):
            candidates.update(self.buckets[key])
        return sorted((v for v in candidates if (v ^ value).bit_count() <= self.radius),
                      key=lambda v: ((v ^ value).bit_count(), v))


class Audit:
    def __init__(self, repo, mirror, output, inventory=None, radius=4):
        self.repo, self.mirror, self.output = repo, mirror, output
        self.policy = SplitPolicy(yaml.safe_load((repo / "edgeml/configs/splits.yaml").read_text()))
        self.issues = []
        self.changes = []
        self.frames = []
        self.inventory = []
        self.metrics = {}
        self.radius = radius
        self.inventory_path = inventory
        self.images = {}
        self.original_images = {}
        self.datasets = {}
        self.repaired = False
        self.decoded = {}

    def issue(self, reason, record, **details):
        self.issues.append({"reason": reason, "record": str(record), **details})

    def change(self, kind, key, before, after, reason):
        self.changes.append({"kind": kind, "key": str(key), "before": before, "after": after, "reason": reason})

    def image(self, ref, data, dataset, source, split="unassigned"):
        digest = hashlib.sha256(data).hexdigest()
        if digest not in self.decoded:
            self.decoded[digest] = inspect_image(data)
        result = {"ref": ref, "dataset_id": dataset, "source_video": source_video(source),
                  "eval_split": split, "bytes": len(data), **self.decoded[digest]}
        self.frames.append(result)
        if split == "quarantine":
            self.issue("source_quarantined", ref)
        for key in ("black", "blank"):
            if result.get(key):
                self.issue(key + "_frame", ref)
        if not result["readable"]:
            self.issue("unreadable_image", ref)
        return result

    def repository(self):
        for path in sorted((self.repo / "frames").glob("*.zip")):
            count = 0
            unpacked = 0
            try:
                with zipfile.ZipFile(path) as archive:
                    for item in archive.infolist():
                        if item.is_dir():
                            continue
                        unpacked += item.file_size
                        try:
                            data = archive.read(item)  # checks each entry's CRC
                        except (zipfile.BadZipFile, OSError) as exc:
                            self.issue("corrupt_zip_member", f"{path.name}:{item.filename}", error=str(exc))
                            continue
                        if Path(item.filename).suffix.lower() in IMAGE_SUFFIXES:
                            source = Path(item.filename).stem if "MVI_" in item.filename else path.stem.removesuffix("_frames")
                            self.image(f"{path.relative_to(self.repo)}:{item.filename}", data,
                                       "archive:" + path.stem, source,
                                       self.policy.expected("archive", source))
                            count += 1
            except zipfile.BadZipFile as exc:
                self.issue("corrupt_archive", path, error=str(exc))
            self.inventory.append({"dataset": "archive:" + path.name, "bytes": path.stat().st_size,
                                   "frames": count, "unpacked_bytes": unpacked, "license": "see source manifest; unknown is not permission"})
        imagery = json.loads((self.repo / "internal/catalog/imagery.json").read_text())
        synthetic_boxes = 0
        synthetic_bytes = 0
        for row in imagery:
            path = self.repo / "web/static" / row["file"]
            if not path.exists():
                self.issue("missing_image", row["file"])
                continue
            result = self.image(row["file"], path.read_bytes(), "synthetic", row.get("src", ""))
            synthetic_bytes += result["bytes"]
            if not row.get("src"):
                self.issue("missing_source", row["file"])
            if not row.get("license"):
                self.issue("missing_synthetic_license", row["file"])
            for i, det in enumerate(row.get("dets", [])):
                synthetic_boxes += 1
                if result["readable"]:
                    box = [det["b"].get(k) for k in ("x", "y", "w", "h")]
                    _, reason = clean_box(box, result["width"], result["height"])
                    if reason:
                        self.issue(reason, f"{row['file']}#{i}")
                if not agnostic(det.get("cls")):
                    self.issue("unmapped_label", row["file"], label=det.get("cls"))
        self.inventory.append({"dataset": "synthetic", "bytes": synthetic_bytes, "frames": len(imagery),
                               "labels": synthetic_boxes, "license": "synthetic; per-image licence/source absent"})
        referenced = {r["file"] for r in imagery}
        for path in sorted((self.repo / "web/static/imagery").glob("*")):
            if path.suffix.lower() in IMAGE_SUFFIXES and "imagery/" + path.name not in referenced:
                self.image("imagery/" + path.name, path.read_bytes(), "synthetic_unreferenced", path.stem)
                self.issue("unreferenced_synthetic_image", path.relative_to(self.repo))
        self.qa_and_scenarios()

    def qa_and_scenarios(self):
        n = 0
        qa = self.repo / "edgeml/eval_qa"
        yaml_files = sorted(qa.rglob("*.yaml"))
        for path in yaml_files:
            row = yaml.safe_load(path.read_text())
            if not isinstance(row, dict):
                self.issue("invalid_qa_yaml", path.relative_to(self.repo))
        self.metrics["qa_yaml_files"] = len(yaml_files)
        for path in sorted((self.repo / "edgeml/eval_qa").rglob("*.jsonl")):
            for row in read_jsonl(path):
                n += 1
                key = f"{path.relative_to(self.repo)}:{row.get('seq_id')}"
                for field in ("license", "source_dataset"):
                    if not row.get(field):
                        self.issue("missing_" + field, key)
                expected = self.policy.expected(row.get("set", ""), row.get("seq_id", ""))
                if expected != row.get("split"):
                    self.issue("qa_split_mismatch", key, actual=row.get("split"), expected=expected)
        self.metrics["qa_index_sequences"] = n
        for path in sorted((self.repo / "scenarios").rglob("*.json")):
            row = json.loads(path.read_text())
            for i, record in enumerate(row.get("sets", [])):
                lowered = {k.lower(): v for k, v in record.items()}
                key = f"{path.relative_to(self.repo)}:sets[{i}]"
                if any(k != k.lower() for k in record):
                    self.issue("scenario_mixed_case_keys", key)
                if not lowered.get("source"):
                    self.issue("missing_source", key)
                if not lowered.get("license"):
                    self.issue("missing_license", key)
                if lowered.get("label") and not agnostic(lowered["label"]):
                    self.issue("unmapped_label", key, label=lowered["label"])
                ref = Path(lowered.get("path", ""))
                if not (path.parent / ref).exists() and not (self.repo / ref).exists():
                    self.issue("scenario_input_not_local", key, path=str(ref))

    def object_inventory(self):
        if not self.inventory_path:
            return
        totals = defaultdict(lambda: {"objects": 0, "bytes": 0})
        empty = 0
        for row in read_jsonl(self.inventory_path):
            if row["size"] == 0:
                empty += 1
            for prefix in {"/".join(row["name"].split("/")[:depth]) for depth in (1, 2, 3)}:
                totals[prefix]["objects"] += 1
                totals[prefix]["bytes"] += row["size"]
        self.metrics["gcs_prefixes"] = dict(sorted(totals.items()))
        self.metrics["zero_byte_objects"] = empty
        self.metrics["object_inventory_sha256"] = hashlib.sha256(self.inventory_path.read_bytes()).hexdigest()

    def extra_images(self, roots):
        for root in roots:
            dataset = "raw:" + root.name
            dimensions = {}
            start = len(self.frames)
            for path in sorted(root.rglob("*")):
                if path.suffix.lower() in IMAGE_SUFFIXES:
                    ref = str(path.relative_to(root.parent.parent))
                    result = self.image(ref, path.read_bytes(), dataset, path.parent.name)
                    dimensions[path.name] = result
            labels = 0
            for path in sorted(root.rglob("*.xml")):
                try:
                    tree = ET.parse(path).getroot()
                    filename = tree.findtext("filename") or path.stem + ".jpg"
                    if filename != path.stem + ".jpg":
                        self.issue("xml_image_name_mismatch", f"{dataset}/{path.relative_to(root)}", declared=filename,
                                   note="Do not infer a GT join from mismatched names")
                    frame = dimensions.get(path.stem + ".jpg")
                    if frame is None:
                        self.issue("xml_missing_image", path.relative_to(root))
                        continue
                    for i, obj in enumerate(tree.findall("object")):
                        labels += 1
                        label = obj.findtext("name")
                        if not agnostic(label):
                            self.issue("unmapped_label", f"{path.relative_to(root)}#{i}", label=label)
                        b = obj.find("bndbox")
                        xmin, ymin, xmax, ymax = [float(b.findtext(k)) for k in ("xmin", "ymin", "xmax", "ymax")]
                        _, reason = clean_box([xmin, ymin, xmax - xmin, ymax - ymin], frame["width"], frame["height"])
                        if reason:
                            self.issue("xml_" + reason, f"{path.relative_to(root)}#{i}")
                except (ET.ParseError, ValueError, TypeError, AttributeError, KeyError) as exc:
                    self.issue("invalid_xml_annotation", path.relative_to(root), error=str(exc))
            rows = self.frames[start:]
            self.inventory.append({"dataset": dataset, "frames": len(rows), "bytes": sum(r["bytes"] for r in rows),
                                   "xml_labels": labels, "license": "not inferred; retain upstream README/source provenance"})
            self.issue("raw_label_review_required", dataset, note="XML checked where present; CSV/YOLO metadata retained unchanged, not normalized")

    def load_catalog(self):
        if not self.mirror:
            return
        for row in read_jsonl(self.mirror / "catalog/datasets.jsonl"):
            self.datasets[row["dataset_id"]] = row
            for field in ("license", "source_url"):
                if not row.get(field) or row[field].lower().startswith("unknown"):
                    self.issue("unknown_" + field, row["dataset_id"])
        for path in sorted((self.mirror / "catalog/images").glob("*.jsonl")):
            for row in read_jsonl(path):
                key = row["dataset_id"], row["image_id"]
                if key in self.images:
                    self.issue("duplicate_image_id", row["image_id"])
                self.images[key] = row
                self.original_images[key] = row

    def repair_smd(self):
        """Rebuild the collided catalog from immutable parquet image.path + row GT."""
        import pyarrow.parquet as pq

        dataset = "singapore_maritime"
        original = [r for r in self.images.values() if r["dataset_id"] == dataset]
        self.images = {k: v for k, v in self.images.items() if k[0] != dataset}
        paths = set()
        annotations = []
        source_splits = defaultdict(set)
        n = 0
        for path in sorted((self.mirror / "raw/singapore_maritime/data").glob("*.parquet")):
            split = path.name.split("-")[0]
            for batch in pq.ParquetFile(path).iter_batches(batch_size=32):
                for row in batch.to_pylist():
                    name = row["image"]["path"]
                    if not name or Path(name).name != name or name in paths:
                        raise ValueError(f"unsafe or duplicate source image.path: {name!r}")
                    paths.add(name)
                    data = row["image"]["bytes"]
                    dest = self.output / "datasets/singapore_maritime/images" / name
                    dest.parent.mkdir(parents=True, exist_ok=True)
                    with dest.open("xb") as stream:
                        stream.write(data)
                    image_id = f"{dataset}/{name}"
                    image = {"image_id": image_id, "dataset_id": dataset,
                             "gcs_uri": "CLEAN_PREFIX/datasets/singapore_maritime/images/" + name,
                             "split": split, "width": row["width"], "height": row["height"],
                             "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest(),
                             "source_ref": name, "timestamp_s": None,
                             "has_annotations": bool(row["objects"]["bbox"])}
                    self.images[(dataset, image_id)] = image
                    old_id = f"{dataset}/{split}/{row['image_id']}"
                    self.change("smd_image", image_id,
                                {"old_image_id": old_id, "parquet": str(path.relative_to(self.mirror)), "row": n},
                                image, "rekey_image_path_restore_overwritten_frame")
                    source_splits[source_video(name)].add(split)
                    objects = row["objects"]
                    if len({len(objects[k]) for k in ("bbox", "category", "id")}) != 1:
                        raise ValueError(f"annotation arrays differ: {name}")
                    for i, box in enumerate(objects["bbox"]):
                        annotation = {"image_id": image_id, "dataset_id": dataset,
                                      "label": objects["category"][i], "label_norm": None,
                                      "box": box, "conf": 1.0, "source": "gt",
                                      "attrs": json.dumps({"source_annotation_id": objects["id"][i],
                                                           "source_image_id": row["image_id"],
                                                           "source_parquet": str(path.relative_to(self.mirror))})}
                        annotations.append(annotation)
                        self.change("smd_annotation", f"{image_id}#{i}",
                                    {"old_image_id": old_id, "source_annotation_id": objects["id"][i]},
                                    annotation, "rejoin_gt_to_source_image_path")
                    n += 1
        if not n:
            raise ValueError("No SMD source parquet files available")
        for image in original:
            self.change("smd_collided_image", image["image_id"], image, None, "superseded_by_source_path_rebuild")
        self.metrics["smd_rebuild"] = {"source_rows": n, "source_annotations": len(annotations),
                                       "old_images": len(original), "source_video_splits": {k: sorted(v) for k, v in source_splits.items()}}
        for video, splits in source_splits.items():
            if len(splits) > 1:
                self.issue("source_video_cross_upstream_splits", video, splits=sorted(splits), action="quarantine_for_evaluation")
        self.smd_annotations = annotations
        self.repaired = True

    def cloud_frames(self):
        if not self.mirror:
            return
        catalog_by_uri = {r["gcs_uri"].split("/", 3)[-1]: r for r in {**self.original_images, **self.images}.values() if r["gcs_uri"].startswith("gs://")}
        self.by_ref = {}
        for prefix in ("datasets", "frames", "edgeml/eval"):
            for path in sorted((self.mirror / prefix).rglob("*")):
                if path.suffix.lower() not in IMAGE_SUFFIXES:
                    continue
                if prefix == "edgeml/eval" and path.parent.name != "img1":
                    continue
                ref = str(path.relative_to(self.mirror))
                cat = catalog_by_uri.get(ref)
                if cat:
                    dataset, source = cat["dataset_id"], cat.get("source_ref") or cat["dataset_id"]
                    if dataset.startswith("video_"):
                        source = dataset
                    split = self.policy.expected(dataset, source, cat.get("split"))
                elif prefix == "edgeml/eval":
                    parts = path.relative_to(self.mirror / prefix).parts
                    dataset, source = parts[:2]
                    split = self.policy.expected(dataset, source)
                else:
                    dataset = "uncatalogued:" + path.relative_to(self.mirror / prefix).parts[0]
                    source = path.relative_to(self.mirror / prefix).parts[0].removesuffix("_frames")
                    split = self.policy.expected(dataset, source)
                result = self.image(ref, path.read_bytes(), dataset, source, split)
                self.by_ref[ref] = result
        if self.repaired:
            for path in sorted((self.output / "datasets").rglob("*.jpg")):
                ref = "CLEAN_PREFIX/" + str(path.relative_to(self.output))
                result = self.image(ref, path.read_bytes(), "singapore_maritime_repaired", path.name, "quarantine")
                self.by_ref[ref] = result
        self.mot()

    def mot(self):
        sequences = []
        for path in sorted((self.mirror / "edgeml/eval").glob("*/*")):
            if not path.is_dir():
                continue
            key = str(path.relative_to(self.mirror))
            ini = path / "seqinfo.ini"
            if not ini.exists():
                self.issue("missing_seqinfo", key)
                continue
            config = configparser.ConfigParser()
            config.read(ini)
            seq = dict(config["Sequence"])
            images = [p for p in (path / seq.get("imdir", "img1")).glob("*") if p.suffix.lower() in IMAGE_SUFFIXES]
            n = len(images)
            frame_numbers = {int(p.stem) for p in images if p.stem.isdigit()}
            if int(seq.get("seqlength", 0)) != n:
                self.issue("sequence_length_mismatch", key, declared=seq.get("seqlength"), actual=n)
            for field in ("framerate", "imwidth", "imheight"):
                if not seq.get(field):
                    self.issue("missing_seqinfo_" + field, key)
            for field in ("source", "license", "source_url"):
                if not seq.get(field):
                    self.issue("missing_sequence_" + field, key)
            labels_path = path / "gt/labels.json"
            labels = json.loads(labels_path.read_text()) if labels_path.exists() else {}
            label_map = labels.get("classes", labels)
            if not labels:
                self.issue("missing_label_map", key)
            if isinstance(label_map, dict):
                for cls, label in label_map.items():
                    if isinstance(label, str) and not agnostic(label):
                        self.issue("unmapped_mot_label", key + "#" + cls, label=label)
            gt = path / "gt/gt.txt"
            boxes = 0
            clean_lines = []
            if gt.exists():
                for i, line in enumerate(gt.read_text().splitlines(), 1):
                    if not line.strip():
                        continue
                    parts = line.split(",")
                    try:
                        values = list(map(float, parts))
                        if len(values) < 8 or not all(math.isfinite(v) for v in values):
                            raise ValueError("invalid MOT fields")
                        frame = int(values[0])
                        if frame < 1 or values[0] != frame or frame not in frame_numbers:
                            self.issue("mot_frame_out_of_range", f"{key}:{i}")
                            self.change("mot_annotation", f"{key}:{i}", line, None, "quarantine_missing_frame")
                            continue
                        box = values[2:6]
                        clipped, reason = clean_box(box, int(seq["imwidth"]), int(seq["imheight"]))
                        if reason:
                            self.issue("mot_" + reason, f"{key}:{i}")
                            updated = list(parts)
                            if clipped is not None:
                                updated[2:6] = [str(v) for v in clipped]
                            self.change("mot_annotation", f"{key}:{i}", line,
                                        ",".join(updated) if clipped is not None else None, reason)
                            if clipped is None:
                                continue
                            line = ",".join(updated)
                        cls = str(int(values[7]))
                        if isinstance(label_map, dict) and cls not in label_map:
                            self.issue("mot_unknown_class_id", f"{key}:{i}", class_id=cls)
                        boxes += 1
                        clean_lines.append(line)
                    except (ValueError, IndexError) as exc:
                        self.issue("invalid_mot_row", f"{key}:{i}", error=str(exc))
                        self.change("mot_annotation", f"{key}:{i}", line, None, "quarantine_invalid_mot_row")
            else:
                self.issue("missing_gt", key)
            dest = self.output / "mot" / path.parent.name / path.name
            (dest / "gt").mkdir(parents=True, exist_ok=True)
            (dest / "gt/gt.txt").write_text("\n".join(clean_lines) + "\n")
            if labels_path.exists():
                (dest / "gt/labels.json").write_text(labels_path.read_text())
            updated_config = configparser.ConfigParser()
            updated_config.read(ini)
            for field in ("source", "source_url"):
                value = seq.get("source_dataset") if field == "source" else "https://github.com/gy65896/FVessel" if "github.com/gy65896/FVessel" in seq.get("license", "") else None
                if not seq.get(field) and value:
                    updated_config["Sequence"][field] = value
                    self.change("seqinfo", f"{key}:{field}", None, value, "recover_from_existing_sequence_provenance")
            with (dest / "seqinfo.ini").open("x") as stream:
                updated_config.write(stream)
            (dest / "sequence.json").write_text(json.dumps({
                "original_image_prefix": key + "/img1/", "frame_numbers_preserved": True,
                "eval_split": self.policy.expected(path.parent.name, path.name),
                "image_storage": "original bucket; apply selection.jsonl exclusions without renumbering"}, indent=2))
            sequences.append({"sequence": key, "frames": n, "boxes": boxes})
        self.metrics["mot_sequences"] = sequences

    def duplicates(self):
        exact = defaultdict(list)
        phashes = defaultdict(list)
        for frame in self.frames:
            exact[frame["sha256"]].append(frame)
            if frame.get("readable") and not frame.get("blank") and not frame.get("black"):
                phashes[int(frame["phash"], 16)].append(frame)
        for rows in exact.values():
            if len(rows) < 2:
                continue
            groups = defaultdict(list)
            for row in rows:
                groups[row["dataset_id"]].append(row)
            for group in groups.values():
                group.sort(key=lambda r: (r["eval_split"] != "test", r["ref"]))
                for row in group[1:]:
                    reason = "exact_duplicate_locked_test" if row["eval_split"] == "test" else "exact_duplicate_within_dataset"
                    self.issue(reason, row["ref"], canonical=group[0]["ref"])
            if len(groups) > 1:
                self.issue("exact_copy_across_artifacts", rows[0]["ref"], copies=len(rows), datasets=sorted(groups))
            splits = {r["eval_split"] for r in rows}
            if {"dev", "test"} <= splits:
                self.issue("exact_content_cross_split", rows[0]["ref"], members=[r["ref"] for r in rows])
                for row in rows:
                    if row["eval_split"] == "dev":
                        self.issue("dev_exact_content_leak", row["ref"], action="quarantine")
        index = HashIndex(self.radius)
        # Count candidate frames, not every quadratic pair in static video runs.
        for value, rows in sorted(phashes.items()):
            matches = index.matches(value)
            candidate = phashes[matches[0]][0] if matches else rows[0]
            for row in rows:
                if row["sha256"] != candidate["sha256"]:
                    self.issue("near_duplicate_candidate", row["ref"], candidate=candidate["ref"],
                               distance=(value ^ int(candidate["phash"], 16)).bit_count())
            for match in [value, *matches]:
                split_rows = [*rows, *phashes[match]]
                if {"dev", "test"} <= {r["eval_split"] for r in split_rows}:
                    self.issue("near_content_cross_split_candidate", rows[0]["ref"], candidate=phashes[match][0]["ref"],
                               distance=(value ^ match).bit_count())
                    break
            index.add(value)

    def clean_catalog(self):
        if not self.mirror:
            return
        clean_images = {}
        duplicate_refs = {r["record"] for r in self.issues if r["reason"] in {"exact_duplicate_within_dataset", "dev_exact_content_leak"}}
        clean_annotations = defaultdict(list)
        for key, row in sorted(self.images.items()):
            result = dict(row)
            ref = row["gcs_uri"] if row["gcs_uri"].startswith("CLEAN_PREFIX/") else row["gcs_uri"].split("/", 3)[-1]
            scanned = self.by_ref.get(ref)
            if not scanned:
                self.issue("catalog_image_not_scanned", row["image_id"])
                continue
            if not scanned["readable"] or scanned.get("black") or scanned.get("blank") or ref in duplicate_refs:
                self.change("image", key, row, None, "quarantine_unreadable_blank_or_exact_duplicate")
                continue
            for field in ("width", "height", "bytes", "sha256"):
                if result.get(field) != scanned[field]:
                    self.issue("catalog_" + field + "_mismatch", row["image_id"])
                    result[field] = scanned[field]
            result["source_video"] = scanned["source_video"]
            result["eval_split"] = scanned["eval_split"]
            if row["dataset_id"] == "singapore_maritime":
                result["eval_split"] = "quarantine"  # upstream random frames mix the same videos
            if not row.get("source_ref"):
                self.issue("missing_source_ref", row["image_id"])
                result["eval_split"] = "quarantine"
            clean_images[key] = result
            if result != row:
                self.change("image", key, row, result, "attach_source_level_split_and_verified_image_metadata")
        files = sorted((self.mirror / "catalog/annotations").glob("*.jsonl"))
        rows = (row for path in files for row in read_jsonl(path))
        if self.repaired:
            rows = (row for row in rows if row["dataset_id"] != "singapore_maritime")
        from itertools import chain
        rows = chain(rows, self.smd_annotations if self.repaired else [])
        seen = set()
        for i, row in enumerate(rows):
            key = row["dataset_id"], row["image_id"]
            image = clean_images.get(key)
            if not image:
                self.issue("orphan_or_quarantined_annotation", row["image_id"])
                self.change("annotation", f"{key}#{i}", row, None, "quarantine_missing_image")
                continue
            if row.get("label") == "horizon":
                try:
                    horizon = json.loads(row["attrs"]) if isinstance(row["attrs"], str) else row["attrs"]
                    if not all(isinstance(horizon[k], (int, float)) and math.isfinite(horizon[k]) for k in ("X", "Y", "Nx", "Ny")):
                        raise ValueError("non-finite horizon")
                    clean_annotations[key[0]].append(row)
                except (ValueError, KeyError, TypeError):
                    self.issue("invalid_horizon", row["image_id"])
                    self.change("annotation", f"{key}#{i}", row, None, "quarantine_invalid_horizon")
                continue
            result = dict(row)
            box, reason = clean_box(row.get("box"), image["width"], image["height"])
            if reason:
                self.issue(reason, f"{row['image_id']}#{i}")
            if box is None:
                self.change("annotation", f"{key}#{i}", row, None, reason)
                continue
            label = agnostic(row.get("label"))
            if label is None:
                self.issue("unmapped_label", row["image_id"], label=row.get("label"))
                self.change("annotation", f"{key}#{i}", row, None, "quarantine_unmapped_label")
                continue
            result.update(box=box, label_norm=label)
            identity = (*key, label, *box)
            if identity in seen:
                self.issue("duplicate_annotation", f"{row['image_id']}#{i}")
                self.change("annotation", f"{key}#{i}", row, None, "deduplicate_identical_box_and_class")
                continue
            seen.add(identity)
            clean_annotations[key[0]].append(result)
            if row.get("label_norm") != label:
                self.issue("label_norm_mismatch", f"{row['image_id']}#{i}", before=row.get("label_norm"), expected=label)
            if result != row:
                self.change("annotation", f"{key}#{i}", row, result, reason or "normalize_native_class")
        counts = Counter(row["image_id"] for rows in clean_annotations.values() for row in rows)
        by_dataset = defaultdict(list)
        for row in clean_images.values():
            has = counts[row["image_id"]] > 0
            if row["has_annotations"] != has:
                old = dict(row)
                row["has_annotations"] = has
                self.change("image", row["image_id"], old, row, "recompute_has_annotations")
            by_dataset[row["dataset_id"]].append(row)
        clean_datasets = []
        for dataset, rows in sorted(by_dataset.items()):
            write_jsonl(self.output / f"catalog/images/{dataset}.jsonl", rows)
            write_jsonl(self.output / f"catalog/annotations/{dataset}.jsonl", clean_annotations[dataset])
            before = self.datasets[dataset]
            after = dict(before)
            after.update(num_images=len(rows), num_annotations=len(clean_annotations[dataset]), bytes=sum(r["bytes"] for r in rows))
            if self.repaired and dataset == "singapore_maritime":
                after["images_prefix"] = "CLEAN_PREFIX/datasets/singapore_maritime/images/"
                after["notes"] = "Rekeyed from source image.path; source_video and eval_split attached. Quarantined for evaluation: upstream videos cross splits."
                after["ingest_script"] = "edgeml/tools/validate_data.py --repair-smd"
            if after != before:
                self.change("dataset", dataset, before, after, "recompute_clean_catalog_counts_and_provenance")
            clean_datasets.append(after)
            self.inventory.append({"dataset": dataset, "bytes": after["bytes"], "frames": after["num_images"],
                                   "labels": after["num_annotations"], "license": after["license"]})
        write_jsonl(self.output / "catalog/datasets.jsonl", clean_datasets)

    def finish(self):
        reasons = defaultdict(set)
        for issue in self.issues:
            reasons[issue["record"]].add(issue["reason"])
        selection = []
        for row in self.frames:
            flags = sorted(reasons.get(row["ref"], set()))
            status = "quarantine" if any(f in flags for f in ("unreadable_image", "black_frame", "blank_frame", "exact_duplicate_within_dataset", "dev_exact_content_leak", "source_quarantined")) else "review" if "near_duplicate_candidate" in flags else "keep"
            selection.append({"ref": row["ref"], "sha256": row["sha256"], "status": status, "reasons": flags})
            if status != "keep":
                self.change("selection", row["ref"], {"status": "original"}, selection[-1], ";".join(flags))
        self.metrics.update(issue_counts=dict(sorted(Counter(r["reason"] for r in self.issues).items())),
                            inventory=self.inventory, scanned_images=len(self.frames),
                            scanned_bytes=sum(r["bytes"] for r in self.frames),
                            changes=len(self.changes), selection_counts=dict(Counter(r["status"] for r in selection)),
                            thresholds={"phash_bits": 63, "hamming_radius": self.radius, "blank_std_below": 1,
                                        "black_99th_percentile_at_most": 3},
                            coverage={"repo": str(self.repo), "mirror": str(self.mirror) if self.mirror else None,
                                      "raw_video_and_archives_in_gcs": "inventory only; local six archives decoded",
                                      "near_duplicates": "candidate frames, not exhaustive pair count; review only"})
        self.metrics["repo_commit"] = subprocess.check_output(["git", "-C", str(self.repo), "rev-parse", "HEAD"], text=True).strip()
        write_jsonl(self.output / "issues.jsonl", self.issues)
        write_jsonl(self.output / "changes.jsonl", self.changes)
        write_jsonl(self.output / "frames.jsonl", self.frames)
        write_jsonl(self.output / "selection.jsonl", selection)
        (self.output / "audit.json").write_text(json.dumps(self.metrics, indent=2, sort_keys=True))
        (self.output / "input_splits.yaml").write_text((self.repo / "edgeml/configs/splits.yaml").read_text())
        print(json.dumps({k: self.metrics[k] for k in ("scanned_images", "issue_counts", "selection_counts", "changes")}, indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--mirror", type=Path, help="Local read-only mirror with catalog/, datasets/, frames/, edgeml/eval/")
    parser.add_argument("--object-inventory", type=Path, help="JSONL name/size/generation listing from inventory_gcs.py")
    parser.add_argument("--output", type=Path, required=True, help="Must not exist; no input is modified")
    parser.add_argument("--repair-smd", action="store_true", help="Rebuild from mirror/raw/singapore_maritime/data/*.parquet (requires pyarrow)")
    parser.add_argument("--near-radius", type=int, default=4, choices=range(9))
    parser.add_argument("--image-root", type=Path, action="append", default=[], help="Additional raw image tree; also checks Pascal VOC XML boxes")
    parser.add_argument("--extra-only", action="store_true", help="Audit only --image-root trees, not repo/catalog")
    args = parser.parse_args()
    if args.repair_smd and not args.mirror:
        parser.error("--repair-smd requires --mirror")
    if args.extra_only and (not args.image_root or args.mirror):
        parser.error("--extra-only needs --image-root and no --mirror")
    if any(not root.is_dir() for root in args.image_root):
        parser.error("every --image-root must be an existing directory")
    args.output.mkdir(parents=True, exist_ok=False)
    cv2.setNumThreads(1)
    audit = Audit(args.repo.resolve(), args.mirror.resolve() if args.mirror else None,
                  args.output.resolve(), args.object_inventory, args.near_radius)
    audit.object_inventory()
    if not args.extra_only:
        audit.repository()
    audit.extra_images(args.image_root)
    if args.mirror:
        audit.load_catalog()
        if args.repair_smd:
            audit.repair_smd()
        audit.cloud_frames()
    audit.duplicates()
    if args.mirror:
        audit.clean_catalog()
    audit.finish()


if __name__ == "__main__":
    main()
