import hashlib
import json
import os
import shutil
import subprocess
import tempfile
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


def upsert_dataset_row(row):
    dataset_id = row["dataset_id"]
    rows = _read_dataset_catalog()
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

    with tempfile.TemporaryDirectory(prefix="ghost-seeker-catalog-") as tmp:
        path = Path(tmp) / "datasets.jsonl"
        _write_jsonl(path, output, DATASET_FIELDS)
        run_gcloud("storage", "cp", str(path), gcs_uri("catalog/datasets.jsonl"))


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
