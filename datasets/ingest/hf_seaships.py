import json
import random
import shutil
import subprocess
import tempfile
from pathlib import Path

import yaml
from huggingface_hub import snapshot_download
from PIL import Image, ImageDraw

from common import (
    GCLOUD,
    ensure_raw_matches_or_upload,
    gcs_uri,
    convert_to_jpeg,
    image_size,
    sha256_file,
    upload_directory,
    utc_timestamp,
    write_catalog,
)


DATASET_ID = "seaships7000"
REPOSITORY_ID = "dronefreak/SeaShips"
REPOSITORY_URL = f"https://huggingface.co/datasets/{REPOSITORY_ID}"
REVISION = "bf234011497547de80e02bd75475d8d49ace0416"
EXPECTED_IMAGES = {"train": 1750, "valid": 1750, "test": 3500}


def load_classes(data_yaml):
    config = yaml.safe_load(data_yaml.read_text(encoding="utf-8"))
    names = config["names"]
    if isinstance(names, dict):
        return [names[index] for index in sorted(names, key=lambda value: int(value))]
    return names


def load_yolo_boxes(label_path, classes, width, height):
    annotations = []
    if not label_path.exists():
        return annotations
    for line_number, line in enumerate(
        label_path.read_text(encoding="utf-8").splitlines(), start=1
    ):
        values = line.split()
        if len(values) != 5:
            raise ValueError(f"Invalid YOLO row at {label_path}:{line_number}")
        class_id = int(values[0])
        if class_id < 0 or class_id >= len(classes):
            raise ValueError(f"Unknown class id {class_id} in {label_path}")
        center_x, center_y, box_width, box_height = map(float, values[1:])
        annotations.append(
            {
                "label": classes[class_id],
                "label_norm": None,
                "box": [
                    (center_x - box_width / 2) * width,
                    (center_y - box_height / 2) * height,
                    box_width * width,
                    box_height * height,
                ],
                "conf": 1.0,
                "source": "gt",
                "attrs": json.dumps(
                    {"class_id": class_id}, separators=(",", ":"), ensure_ascii=False
                ),
            }
        )
    return annotations


def save_overlays(samples, output_dir):
    output_dir.mkdir(parents=True, exist_ok=True)
    for image_path, boxes, split in samples:
        with Image.open(image_path) as source:
            image = source.convert("RGB")
        draw = ImageDraw.Draw(image)
        for annotation in boxes:
            x, y, width, height = annotation["box"]
            x1, y1, x2, y2 = x, y, x + width, y + height
            draw.rectangle((x1, y1, x2, y2), outline=(255, 35, 35), width=4)
            label = annotation["label"]
            text_box = draw.textbbox((0, 0), label)
            text_width = text_box[2] - text_box[0]
            text_height = text_box[3] - text_box[1]
            top = max(0, y1 - text_height - 8)
            draw.rectangle(
                (x1, top, x1 + text_width + 8, top + text_height + 8),
                fill=(255, 35, 35),
            )
            draw.text((x1 + 4, top + 4), label, fill=(255, 255, 255))
        output = output_dir / f"seaships_{split}_{image_path.stem}.png"
        image.save(output, format="PNG")
        print(f"Overlay: {output}")


def main():
    bucket_raw_prefix = gcs_uri(f"raw/{DATASET_ID}/")
    (Path.home() / "data").mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(
        prefix="seaships7000-", dir=Path.home() / "data"
    ) as temporary_directory:
        temporary_directory = Path(temporary_directory)
        snapshot_dir = temporary_directory / "snapshot"
        ready_dir = temporary_directory / "ready"
        downloaded_snapshot = Path(
            snapshot_download(
                repo_id=REPOSITORY_ID,
                repo_type="dataset",
                revision=REVISION,
                local_dir=snapshot_dir,
                max_workers=8,
            )
        )
        metadata_dir = downloaded_snapshot / ".cache"
        if metadata_dir.exists():
            shutil.rmtree(metadata_dir)

        ensure_raw_matches_or_upload(downloaded_snapshot, bucket_raw_prefix)

        classes = load_classes(downloaded_snapshot / "data" / "data.yaml")
        image_rows = []
        annotation_rows = []
        image_annotation_pairs = []
        total_bytes = 0
        split_counts = {}

        for split in ("train", "valid", "test"):
            upstream_images = sorted(
                (downloaded_snapshot / "data" / "images" / split).glob("*")
            )
            upstream_images = [
                path
                for path in upstream_images
                if path.is_file() and path.suffix.lower() in (".jpg", ".jpeg", ".png")
            ]
            split_counts[split] = len(upstream_images)
            if len(upstream_images) != EXPECTED_IMAGES[split]:
                raise RuntimeError(
                    f"Upstream {split} has {len(upstream_images)} images; "
                    f"expected {EXPECTED_IMAGES[split]}"
                )

            output_dir = ready_dir / "images" / split
            for upstream_image in upstream_images:
                output_path = convert_to_jpeg(
                    upstream_image, output_dir / f"{upstream_image.stem}.jpg"
                )
                width, height = image_size(output_path)
                size_bytes = output_path.stat().st_size
                total_bytes += size_bytes
                image_id = f"{DATASET_ID}/{split}/{output_path.stem}"
                file_annotations = load_yolo_boxes(
                    downloaded_snapshot
                    / "data"
                    / "labels"
                    / split
                    / f"{upstream_image.stem}.txt",
                    classes,
                    width,
                    height,
                )
                image_rows.append(
                    {
                        "image_id": image_id,
                        "dataset_id": DATASET_ID,
                        "gcs_uri": gcs_uri(
                            f"datasets/{DATASET_ID}/images/{split}/{output_path.name}"
                        ),
                        "split": split,
                        "width": width,
                        "height": height,
                        "bytes": size_bytes,
                        "sha256": sha256_file(output_path),
                        "source_ref": upstream_image.name,
                        "timestamp_s": None,
                        "has_annotations": bool(file_annotations),
                    }
                )
                for annotation in file_annotations:
                    annotation_rows.append(
                        {
                            "image_id": image_id,
                            "dataset_id": DATASET_ID,
                            **annotation,
                        }
                    )
                if file_annotations:
                    image_annotation_pairs.append(
                        (output_path, file_annotations, split)
                    )

        upload_directory(ready_dir, gcs_uri(f"datasets/{DATASET_ID}"))
        images_prefix = gcs_uri(f"datasets/{DATASET_ID}/images/")
        write_catalog(
            {
                "dataset_id": DATASET_ID,
                "name": "SeaShips 7000",
                "source_type": "huggingface",
                "source_url": REPOSITORY_URL,
                "source_revision": REVISION,
                "license": "unknown",
                "viewpoint": "surface",
                "modality": "rgb",
                "synthetic": False,
                "annotation_format": "yolo",
                "native_classes": classes,
                "num_images": len(image_rows),
                "num_annotations": len(annotation_rows),
                "bytes": total_bytes,
                "raw_prefix": bucket_raw_prefix,
                "images_prefix": images_prefix,
                "ingested_at": utc_timestamp(),
                "ingest_script": "datasets/ingest/hf_seaships.py",
                "notes": "YOLO-format boxes from the upstream train/valid/test split.",
            },
            image_rows,
            annotation_rows,
        )
        print(
            f"{DATASET_ID}: {len(image_rows)} images, "
            f"{len(annotation_rows)} annotations; splits={split_counts}"
        )
        save_overlays(
            random.SystemRandom().sample(image_annotation_pairs, 3),
            Path.home() / "verify",
        )


def list_remote_prefix(uri):
    result = subprocess.run(
        [GCLOUD, "storage", "ls", uri],
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode:
        if "matched no objects" in result.stderr or "No URLs matched" in result.stderr:
            return []
        raise RuntimeError(f"Could not inspect {uri}: {result.stderr.strip()}")
    return result.stdout.splitlines()


if __name__ == "__main__":
    main()
