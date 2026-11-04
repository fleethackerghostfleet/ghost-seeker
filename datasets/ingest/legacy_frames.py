import re
import subprocess
from pathlib import Path

from common import (
    BUCKET,
    gcs_uri,
    image_size,
    sha256_file,
    utc_timestamp,
    write_catalog,
)


DATASET_ROOT = Path.home() / "data" / "frames"
SOURCE_URL = "https://github.com/fleethackerghostfleet/ghost-seeker"
DATASETS = (
    ("30_Days_at_Sea_Timelapse", "30 Days at Sea Timelapse", 644),
    (
        "Argentina_to_Alaska_Cruise_Bridge_Timelapse",
        "Argentina to Alaska Cruise Bridge Timelapse",
        139,
    ),
    ("Cargo_Ship_Storm_first_hour", "Cargo Ship Storm first hour", 3600),
    ("Cargo_Ship_Uncut_View", "Cargo Ship Uncut View", 9000),
    ("Dolphins_Pungo_Ferry_Bridge", "Dolphins Pungo Ferry Bridge", 5),
)


def dataset_id_from_name(name):
    return "yt_" + re.sub(r"[^a-z0-9]+", "_", name.lower()).strip("_")


def main():
    source_revision = subprocess.check_output(
        ["git", "rev-parse", "HEAD"],
        cwd=Path(__file__).resolve().parents[2],
        text=True,
    ).strip()
    for name, display_name, expected_count in DATASETS:
        dataset_id = dataset_id_from_name(name)
        local_dir = DATASET_ROOT / f"{name}_frames"
        files = sorted(local_dir.glob("*.jpg"))
        if len(files) != expected_count:
            raise RuntimeError(
                f"{local_dir} has {len(files)} JPEGs; expected {expected_count}"
            )

        images_prefix = gcs_uri(f"frames/{name}_frames/")
        image_rows = []
        total_bytes = 0
        for image_path in files:
            match = re.fullmatch(r"sec_(\d+)\.jpg", image_path.name)
            if match is None:
                raise ValueError(f"Unexpected frame name: {image_path.name}")
            second = int(match.group(1))
            width, height = image_size(image_path)
            size_bytes = image_path.stat().st_size
            total_bytes += size_bytes
            image_rows.append(
                {
                    "image_id": f"{dataset_id}/all/{image_path.stem}",
                    "dataset_id": dataset_id,
                    "gcs_uri": f"{images_prefix}{image_path.name}",
                    "split": "all",
                    "width": width,
                    "height": height,
                    "bytes": size_bytes,
                    "sha256": sha256_file(image_path),
                    "source_ref": image_path.name,
                    "timestamp_s": float(second - 1),
                    "has_annotations": False,
                }
            )

        write_catalog(
            {
                "dataset_id": dataset_id,
                "name": display_name,
                "source_type": "repo",
                "source_url": SOURCE_URL,
                "source_revision": source_revision,
                "license": "unknown",
                "viewpoint": "surface",
                "modality": "rgb",
                "synthetic": False,
                "annotation_format": "none",
                "native_classes": [],
                "num_images": len(image_rows),
                "num_annotations": 0,
                "bytes": total_bytes,
                "raw_prefix": gcs_uri(f"zips/{name}_frames.zip"),
                "images_prefix": images_prefix,
                "ingested_at": utc_timestamp(),
                "ingest_script": "datasets/ingest/legacy_frames.py",
                "notes": None,
            },
            image_rows,
            [],
        )
        print(f"{dataset_id}: {len(image_rows)} images, 0 annotations")


if __name__ == "__main__":
    main()
