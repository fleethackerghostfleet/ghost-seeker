import argparse
import json
import math
import random
import subprocess
import tempfile
import zipfile
from pathlib import Path

from PIL import Image, ImageDraw

from common import (
    GCLOUD,
    convert_to_jpeg,
    gcs_uri,
    image_size,
    run_gcloud,
    sha256_file,
    upload_directory,
    utc_timestamp,
    write_catalog,
)


DATASET_ID = "smd_vis_onboard_sample"
ARCHIVE_NAME = "SMD_VIS_Onboard_frames.zip"
SOURCE_URL = "https://sites.google.com/site/dilipprasad/home/singapore-maritime-dataset"
DATA_ROOT = Path.home() / "data"
OVERLAY_ROOT = Path.home() / "verify"


def list_gcs(uri):
    result = subprocess.run(
        [GCLOUD, "storage", "ls", uri],
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode:
        if "matched no objects" in result.stderr or "No URLs matched" in result.stderr:
            return []
        raise RuntimeError(f"Could not list {uri}: {result.stderr.strip()}")
    return [line.strip() for line in result.stdout.splitlines() if line.strip()]


def raw_prefix_has_objects():
    raw_prefix = gcs_uri(f"raw/{DATASET_ID}/")
    return bool(list_gcs(raw_prefix))


def safe_extract(archive, destination):
    destination = destination.resolve()
    with zipfile.ZipFile(archive) as bundle:
        for member in bundle.infolist():
            target = (destination / member.filename).resolve()
            if target != destination and destination not in target.parents:
                raise ValueError(f"Unsafe path in archive: {member.filename}")
        bundle.extractall(destination)


def json_attrs(values, fields):
    return json.dumps(
        {field: values.get(field) for field in fields},
        ensure_ascii=False,
        separators=(",", ":"),
    )


def draw_horizon(draw, horizon, size):
    normal_x = float(horizon["Nx"])
    normal_y = float(horizon["Ny"])
    normal_length = math.hypot(normal_x, normal_y)
    if normal_length == 0:
        raise ValueError("Horizon normal must not be zero")
    direction_x = -normal_y / normal_length
    direction_y = normal_x / normal_length
    extent = 2 * math.hypot(*size)
    x = float(horizon["X"])
    y = float(horizon["Y"])
    draw.line(
        (
            x - direction_x * extent,
            y - direction_y * extent,
            x + direction_x * extent,
            y + direction_y * extent,
        ),
        fill=(255, 225, 0),
        width=4,
    )


def save_overlays(samples):
    overlay_candidates = [
        sample for sample in samples if sample["objects"] and sample["horizon"]
    ]
    if len(overlay_candidates) < 2:
        raise RuntimeError("Need two SMD frames with objects and horizon labels")
    OVERLAY_ROOT.mkdir(parents=True, exist_ok=True)
    selected = random.SystemRandom().sample(overlay_candidates, 2)
    output_paths = []
    for sample in selected:
        with Image.open(sample["image_path"]) as source:
            image = source.convert("RGB")
        draw = ImageDraw.Draw(image)
        for obj in sample["objects"]:
            x, y, width, height = obj["bbox_xywh"]
            draw.rectangle(
                (x, y, x + width, y + height),
                outline=(255, 35, 35),
                width=4,
            )
            label = obj["object_type"]
            text_box = draw.textbbox((0, 0), label)
            text_width = text_box[2] - text_box[0]
            text_height = text_box[3] - text_box[1]
            top = max(0, y - text_height - 8)
            draw.rectangle(
                (x, top, x + text_width + 8, top + text_height + 8),
                fill=(255, 35, 35),
            )
            draw.text((x + 4, top + 4), label, fill=(255, 255, 255))
        draw_horizon(draw, sample["horizon"], image.size)
        output_path = OVERLAY_ROOT / (
            f"smd_{sample['video']}_{sample['image_path'].stem}.png"
        )
        image.save(output_path, format="PNG")
        output_paths.append(output_path)
        print(f"Overlay: {output_path}")
    return output_paths


def ingest(archive_path, repository_root):
    archive_path = archive_path.resolve()
    if not archive_path.is_file():
        raise FileNotFoundError(f"SMD archive not found: {archive_path}")
    raw_prefix_exists = raw_prefix_has_objects()
    DATA_ROOT.mkdir(parents=True, exist_ok=True)

    with tempfile.TemporaryDirectory(prefix="smd-vis-onboard-", dir=DATA_ROOT) as tmp:
        temporary_root = Path(tmp)
        extracted_root = temporary_root / "extracted"
        extracted_root.mkdir()
        safe_extract(archive_path, extracted_root)
        dataset_root = extracted_root / "SMD_VIS_Onboard_frames"
        if not dataset_root.is_dir():
            raise RuntimeError("Archive is missing SMD_VIS_Onboard_frames/")

        upstream_images = sorted((dataset_root / "frames").rglob("*.jpg"))
        if len(upstream_images) != 80:
            raise RuntimeError(
                f"Expected 80 SMD frames; found {len(upstream_images)}"
            )

        ready_root = temporary_root / "ready"
        output_dir = ready_root / "images" / "all"
        image_rows = []
        annotation_rows = []
        object_annotation_count = 0
        horizon_count = 0
        negative_count = 0
        native_classes = []
        overlay_samples = []
        seen_image_ids = set()
        total_bytes = 0

        for upstream_image in upstream_images:
            video = upstream_image.parent.name
            label_path = dataset_root / "labels" / video / f"{upstream_image.stem}.json"
            if not label_path.is_file():
                raise FileNotFoundError(f"Missing label sidecar: {label_path}")
            label_data = json.loads(label_path.read_text(encoding="utf-8"))
            objects = label_data.get("objects", [])
            if not isinstance(objects, list):
                raise ValueError(f"Expected an object list in {label_path}")
            horizon = label_data.get("horizon")

            output_path = convert_to_jpeg(
                upstream_image, output_dir / upstream_image.name
            )
            width, height = image_size(output_path)
            if label_data.get("width") not in (None, width) or label_data.get(
                "height"
            ) not in (None, height):
                raise ValueError(f"Label dimensions do not match {upstream_image}")
            size_bytes = output_path.stat().st_size
            total_bytes += size_bytes
            image_id = f"{DATASET_ID}/all/{output_path.stem}"
            if image_id in seen_image_ids:
                raise ValueError(f"Duplicate SMD image name: {output_path.name}")
            seen_image_ids.add(image_id)

            image_rows.append(
                {
                    "image_id": image_id,
                    "dataset_id": DATASET_ID,
                    "gcs_uri": gcs_uri(
                        f"datasets/{DATASET_ID}/images/all/{output_path.name}"
                    ),
                    "split": "all",
                    "width": width,
                    "height": height,
                    "bytes": size_bytes,
                    "sha256": sha256_file(output_path),
                    "source_ref": upstream_image.relative_to(extracted_root).as_posix(),
                    "timestamp_s": label_data["time_s"],
                    "has_annotations": bool(objects),
                }
            )

            for obj in objects:
                label = obj["object_type"]
                if label not in native_classes:
                    native_classes.append(label)
                annotation_rows.append(
                    {
                        "image_id": image_id,
                        "dataset_id": DATASET_ID,
                        "label": label,
                        "label_norm": None,
                        "box": obj["bbox_xywh"],
                        "conf": 1.0,
                        "source": "gt",
                        "attrs": json_attrs(
                            {
                                **obj,
                                "video": label_data["video"],
                                "frame_index": label_data["frame_index"],
                            },
                            (
                                "object_id",
                                "motion_type",
                                "distance_type",
                                "video",
                                "frame_index",
                            ),
                        ),
                    }
                )
                object_annotation_count += 1
            if not objects:
                negative_count += 1

            if horizon is not None:
                annotation_rows.append(
                    {
                        "image_id": image_id,
                        "dataset_id": DATASET_ID,
                        "label": "horizon",
                        "label_norm": None,
                        "box": [],
                        "conf": 1.0,
                        "source": "gt",
                        "attrs": json_attrs(horizon, ("X", "Y", "Nx", "Ny")),
                    }
                )
                horizon_count += 1

            overlay_samples.append(
                {
                    "image_path": output_path,
                    "objects": objects,
                    "horizon": horizon,
                    "video": label_data["video"],
                }
            )

        if negative_count != 14:
            raise RuntimeError(f"Expected 14 labelled negatives; found {negative_count}")

        image_prefix = gcs_uri(f"datasets/{DATASET_ID}/images/all/")
        source_revision = subprocess.check_output(
            ["git", "rev-parse", "HEAD"],
            cwd=repository_root,
            text=True,
        ).strip()
        dataset_row = {
            "dataset_id": DATASET_ID,
            "name": "Singapore Maritime Dataset VIS Onboard sample",
            "source_type": "repo",
            "source_url": SOURCE_URL,
            "source_revision": source_revision,
            "license": "unknown (academic research use)",
            "viewpoint": "surface",
            "modality": "rgb",
            "synthetic": False,
            "annotation_format": "smd-objectgt-json (per-frame sidecar, bbox_xywh px)",
            "native_classes": native_classes,
            "num_images": len(image_rows),
            "num_annotations": object_annotation_count,
            "bytes": total_bytes,
            "raw_prefix": gcs_uri(f"raw/{DATASET_ID}/"),
            "images_prefix": image_prefix,
            "ingested_at": utc_timestamp(),
            "ingest_script": "datasets/ingest/smd_vis_onboard.py",
            "notes": (
                "from PR #3; 4 labelled videos MVI_0790/0797/0799/0801, "
                f"every 30th frame; horizon_count={horizon_count}; "
                f"labelled_negatives={negative_count}"
            ),
        }

        save_overlays(overlay_samples)
        if raw_prefix_exists:
            print(
                f"Raw prefix already exists; skipping raw upload: "
                f"{gcs_uri(f'raw/{DATASET_ID}/')}"
            )
        else:
            run_gcloud(
                "storage",
                "cp",
                str(archive_path),
                gcs_uri(f"raw/{DATASET_ID}/{ARCHIVE_NAME}"),
            )
        upload_directory(ready_root, gcs_uri(f"datasets/{DATASET_ID}"))
        write_catalog(dataset_row, image_rows, annotation_rows)
        print(
            f"{DATASET_ID}: {len(image_rows)} images, "
            f"{object_annotation_count} object annotations, "
            f"{horizon_count} horizon rows, {negative_count} labelled negatives"
        )


def main():
    repository_root = Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--archive",
        type=Path,
        default=repository_root / "frames" / ARCHIVE_NAME,
    )
    arguments = parser.parse_args()
    ingest(arguments.archive, repository_root)


if __name__ == "__main__":
    main()
