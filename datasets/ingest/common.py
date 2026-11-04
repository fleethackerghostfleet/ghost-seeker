import hashlib
import json
import os
import random
import shutil
import subprocess
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path

from PIL import Image


BUCKET = "qwiklabs-gcp-02-34a870014566-ghost-seeker"
GCLOUD = os.environ.get("GCLOUD", "/home/ubuntu/google-cloud-sdk/bin/gcloud")

DATASET_FIELDS = (
    "dataset_id",
    "name",
    "source_type",
    "source_url",
    "source_revision",
    "license",
    "viewpoint",
    "modality",
    "synthetic",
    "annotation_format",
    "native_classes",
    "num_images",
    "num_annotations",
    "bytes",
    "raw_prefix",
    "images_prefix",
    "ingested_at",
    "ingest_script",
    "notes",
)
IMAGE_FIELDS = (
    "image_id",
    "dataset_id",
    "gcs_uri",
    "split",
    "width",
    "height",
    "bytes",
    "sha256",
    "source_ref",
    "timestamp_s",
    "has_annotations",
)
ANNOTATION_FIELDS = (
    "image_id",
    "dataset_id",
    "label",
    "label_norm",
    "box",
    "conf",
    "source",
    "attrs",
)


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def image_size(path):
    with Image.open(path) as image:
        return image.size


def convert_to_jpeg(source, destination):
    source = Path(source)
    destination = Path(destination)
    destination.parent.mkdir(parents=True, exist_ok=True)
    with Image.open(source) as image:
        if image.format == "JPEG" and source.suffix.lower() in (".jpg", ".jpeg"):
            shutil.copyfile(source, destination)
        else:
            image.convert("RGB").save(destination, format="JPEG", quality=95)
    return destination


def gcs_uri(path):
    return f"gs://{BUCKET}/{path.lstrip('/')}"


def run_gcloud(*args):
    subprocess.run([GCLOUD, *args], check=True)


def upload_directory(local_dir, destination_uri):
    run_gcloud("storage", "rsync", "--recursive", str(local_dir), destination_uri)


def _normalized_row(row, fields):
    return {field: row.get(field) for field in fields}


def _write_jsonl(path, rows, fields):
    with open(path, "w", encoding="utf-8") as stream:
        for row in rows:
            stream.write(
                json.dumps(
                    _normalized_row(row, fields),
                    ensure_ascii=False,
                    separators=(",", ":"),
                )
            )
            stream.write("\n")


def _read_dataset_catalog():
    uri = gcs_uri("catalog/datasets.jsonl")
    result = subprocess.run(
        [GCLOUD, "storage", "cat", uri],
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode:
        if "matched no objects" in result.stderr or "No URLs matched" in result.stderr:
            return []
        raise RuntimeError(f"Could not read {uri}: {result.stderr.strip()}")
    return [json.loads(line) for line in result.stdout.splitlines() if line.strip()]


def _dataset_catalog_generation(uri):
    result = subprocess.run(
        [
            GCLOUD,
            "storage",
            "objects",
            "describe",
            uri,
            "--format=value(generation)",
        ],
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode:
        error = result.stderr.lower()
        if any(
            marker in error
            for marker in (
                "404",
                "not found",
                "does not exist",
                "no urls matched",
                "matched no objects",
            )
        ):
            return 0
        raise RuntimeError(
            f"Could not read generation for {uri}: {result.stderr.strip()}"
        )
    generation = result.stdout.strip()
    if not generation:
        raise RuntimeError(f"No generation returned for {uri}")
    return int(generation)


def _update_dataset_catalog(transform):
    uri = gcs_uri("catalog/datasets.jsonl")
    for attempt in range(5):
        generation = _dataset_catalog_generation(uri)
        rows = _read_dataset_catalog()
        output, result_value = transform(rows)
        if output == rows:
            return result_value
        with tempfile.TemporaryDirectory(prefix="ghost-seeker-catalog-") as tmp:
            path = Path(tmp) / "datasets.jsonl"
            _write_jsonl(path, output, DATASET_FIELDS)
            result = subprocess.run(
                [
                    GCLOUD,
                    "storage",
                    "cp",
                    f"--if-generation-match={generation}",
                    str(path),
                    uri,
                ],
                check=False,
                capture_output=True,
                text=True,
            )
        if result.returncode == 0:
            return result_value
        error = result.stderr.lower()
        if "412" in error or "precondition" in error:
            if attempt < 4:
                time.sleep(random.uniform(0.1, 0.4))
                continue
            raise RuntimeError(
                f"Dataset catalog changed during all 5 write attempts: {result.stderr.strip()}"
            )
        raise RuntimeError(f"Could not update {uri}: {result.stderr.strip()}")
    raise RuntimeError(f"Could not update {uri} after 5 attempts")


def upsert_dataset_row(row):
    dataset_id = row["dataset_id"]

    def transform(rows):
        output = []
        replaced = False
        for existing in rows:
            if existing.get("dataset_id") == dataset_id:
                if not replaced:
                    output.append(_normalized_row(row, DATASET_FIELDS))
                    replaced = True
            else:
                output.append(_normalized_row(existing, DATASET_FIELDS))
        if not replaced:
            output.append(_normalized_row(row, DATASET_FIELDS))
        return output, None

    _update_dataset_catalog(transform)


def delete_dataset_rows_by_prefix(prefix):
    def transform(rows):
        retained = [
            row
            for row in rows
            if not str(row.get("dataset_id", "")).startswith(prefix)
        ]
        return retained, len(rows) - len(retained)

    return _update_dataset_catalog(transform)


def write_catalog(dataset_row, image_rows, annotation_rows):
    dataset_id = dataset_row["dataset_id"]
    with tempfile.TemporaryDirectory(prefix="ghost-seeker-catalog-") as tmp:
        images_path = Path(tmp) / "images.jsonl"
        annotations_path = Path(tmp) / "annotations.jsonl"
        _write_jsonl(images_path, image_rows, IMAGE_FIELDS)
        _write_jsonl(annotations_path, annotation_rows, ANNOTATION_FIELDS)
        run_gcloud(
            "storage",
            "cp",
            str(images_path),
            gcs_uri(f"catalog/images/{dataset_id}.jsonl"),
        )
        run_gcloud(
            "storage",
            "cp",
            str(annotations_path),
            gcs_uri(f"catalog/annotations/{dataset_id}.jsonl"),
        )
        upsert_dataset_row(dataset_row)


def utc_timestamp():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
