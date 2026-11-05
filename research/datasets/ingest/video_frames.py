import json
import re
import subprocess
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import urlparse

from common import (
    GCLOUD,
    delete_dataset_rows_by_prefix,
    gcs_uri,
    image_size,
    run_gcloud,
    sha256_file,
    utc_timestamp,
    write_catalog,
)


DATA_ROOT = Path.home() / "data"
SOURCE_URL = "https://github.com/fleethackerghostfleet/ghost-seeker"
INGEST_SCRIPT = "research/datasets/ingest/video_frames.py"


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


def discover_frame_sets():
    root_uri = gcs_uri("frames/")
    frame_sets = {}
    for uri in list_gcs(root_uri):
        relative = uri.removeprefix(root_uri).strip("/")
        if "/" in relative or not relative.endswith("_frames"):
            continue
        name = relative[: -len("_frames")]
        frame_sets[name] = gcs_uri(f"frames/{name}_frames/")
    return sorted(frame_sets.items())


def load_source_metadata(name, source_objects):
    source_uri = gcs_uri(f"sources/{name}.json")
    if source_uri not in source_objects:
        return None
    result = subprocess.run(
        [GCLOUD, "storage", "cat", source_uri],
        check=True,
        capture_output=True,
        text=True,
    )
    metadata = json.loads(result.stdout)
    if not isinstance(metadata, dict):
        raise ValueError(f"Expected a JSON object in {source_uri}")
    return metadata


def timestamp_utc(value):
    value = str(value).strip().replace("Z", "+00:00")
    value = re.sub(r"([+-]\d{2})(\d{2})$", r"\1:\2", value)
    timestamp = datetime.fromisoformat(value)
    if timestamp.tzinfo is None:
        timestamp = timestamp.replace(tzinfo=timezone.utc)
    return timestamp.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def object_upload_time(uri):
    result = subprocess.run(
        [GCLOUD, "storage", "objects", "describe", uri, "--format=json"],
        check=True,
        capture_output=True,
        text=True,
    )
    metadata = json.loads(result.stdout)
    created = metadata.get("creation_time") or metadata.get("timeCreated")
    if not created:
        raise RuntimeError(f"Object metadata has no creation time: {uri}")
    return timestamp_utc(created)


def source_type_from_url(source_url):
    host = (urlparse(source_url or "").hostname or "").lower()
    if host == "youtu.be" or host == "youtube.com" or host.endswith(".youtube.com"):
        return "youtube"
    if host == "facebook.com" or host.endswith(".facebook.com"):
        return "facebook"
    if host == "motionarray.com" or host.endswith(".motionarray.com"):
        return "motionarray"
    return "web"


def dataset_id_from_name(name):
    return "video_" + re.sub(r"[^a-z0-9]+", "_", name.lower()).strip("_")


def metadata_notes(metadata):
    if metadata is None:
        return None
    fields = ("uploader", "fps", "native_fps", "duration_s")
    return json.dumps(
        {field: metadata.get(field) for field in fields},
        ensure_ascii=False,
        separators=(",", ":"),
    )


def catalog_frame_set(name, frames_uri, metadata, source_revision):
    dataset_id = dataset_id_from_name(name)
    with tempfile.TemporaryDirectory(prefix="video-frames-", dir=DATA_ROOT) as tmp:
        local_dir = Path(tmp)
        run_gcloud("storage", "cp", "-r", frames_uri, str(local_dir))
        files = sorted(path for path in local_dir.rglob("sec_*.jpg") if path.is_file())
        if not files:
            raise RuntimeError(f"No sec_*.jpg frames found under {frames_uri}")

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
                    "gcs_uri": f"{frames_uri}{image_path.name}",
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

        has_metadata = metadata is not None
        source_url = metadata.get("source_url") if has_metadata else SOURCE_URL
        ingested_at = (
            timestamp_utc(metadata["ingested"])
            if has_metadata and metadata.get("ingested")
            else object_upload_time(f"{frames_uri}{files[0].name}")
        )
        row = {
            "dataset_id": dataset_id,
            "name": (
                metadata.get("title") or name.replace("_", " ")
                if has_metadata
                else name.replace("_", " ")
            ),
            "source_type": (
                source_type_from_url(source_url) if has_metadata else "repo"
            ),
            "source_url": source_url,
            "source_revision": (
                metadata.get("youtube_id") if has_metadata else source_revision
            ),
            "license": (
                metadata.get("license") or "unknown" if has_metadata else "unknown"
            ),
            "viewpoint": "surface",
            "modality": "rgb",
            "synthetic": False,
            "annotation_format": "none",
            "native_classes": [],
            "num_images": len(image_rows),
            "num_annotations": 0,
            "bytes": total_bytes,
            "raw_prefix": gcs_uri(f"zips/{name}_frames.zip"),
            "images_prefix": frames_uri,
            "ingested_at": ingested_at,
            "ingest_script": INGEST_SCRIPT,
            "notes": metadata_notes(metadata),
        }
        write_catalog(row, image_rows, [])
        print(f"{dataset_id}: {len(image_rows)} images, 0 annotations")


def delete_legacy_youtube_catalog():
    removed_rows = delete_dataset_rows_by_prefix("yt_")
    removed_files = 0
    for table in ("images", "annotations"):
        objects = list_gcs(gcs_uri(f"catalog/{table}/yt_*.jsonl"))
        if objects:
            run_gcloud("storage", "rm", *objects)
            removed_files += len(objects)
    print(f"Removed {removed_rows} yt_ dataset rows and {removed_files} catalog files")


def main():
    DATA_ROOT.mkdir(parents=True, exist_ok=True)
    repository_root = Path(__file__).resolve().parents[2]
    source_revision = subprocess.check_output(
        ["git", "rev-parse", "HEAD"],
        cwd=repository_root,
        text=True,
    ).strip()
    frame_sets = discover_frame_sets()
    if not frame_sets:
        raise RuntimeError(f"No frame sets discovered under {gcs_uri('frames/')}")

    source_objects = set(list_gcs(gcs_uri("sources/")))
    print(f"Discovered {len(frame_sets)} frame sets")
    for name, frames_uri in frame_sets:
        metadata = load_source_metadata(name, source_objects)
        catalog_frame_set(name, frames_uri, metadata, source_revision)
    delete_legacy_youtube_catalog()


if __name__ == "__main__":
    main()
