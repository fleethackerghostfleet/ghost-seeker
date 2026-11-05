"""Eval-set loader over the shared dataset catalog (GCS catalog/*.jsonl == BigQuery ghost_fleet.*).

Detection-only sets are not copied into edgeml/eval (CONTRACTS §3); we read the catalog and mirror the
images locally with the same object layout as the bucket:  <data_root>/datasets/<ds>/images/<split>/x.jpg
"""
from __future__ import annotations

import json
import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterable, Optional

import yaml

BUCKET = "gs://qwiklabs-gcp-02-34a870014566-ghost-seeker"
SPLITS_YAML = Path(__file__).resolve().parents[3] / "configs" / "splits.yaml"


@dataclass
class EvalImage:
    image_id: str
    dataset_id: str
    gcs_uri: str
    local_path: str
    width: int
    height: int
    split: str  # upstream split name
    source_ref: str
    eval_split: Optional[str] = None  # clean catalogs can explicitly quarantine unsafe upstream splits
    boxes: list[list[float]] = field(default_factory=list)  # pixel [x, y, w, h]
    labels: list[str] = field(default_factory=list)  # native labels
    horizon: Optional[dict] = None  # {X, Y, Nx, Ny} when the catalog has one (SMD)

    @property
    def video(self) -> Optional[str]:
        # SMD onboard frames are named MVI_0790_f00000; clips are frames/<Name>_frames/sec_0001.jpg
        base = os.path.basename(self.source_ref or self.gcs_uri)
        if base.startswith("MVI_"):
            return base.rsplit("_f", 1)[0]
        return None


def _read_jsonl(path: str) -> Iterable[dict]:
    with open(path) as f:
        for line in f:
            line = line.strip()
            if line:
                yield json.loads(line)


def local_path_for(gcs_uri: str, data_root: str) -> str:
    assert gcs_uri.startswith(BUCKET + "/"), gcs_uri
    return os.path.join(data_root, gcs_uri[len(BUCKET) + 1 :])


def load_dataset(dataset_id: str, catalog_dir: str, data_root: str) -> list[EvalImage]:
    """All catalog images of one dataset with their GT boxes attached."""
    imgs: dict[str, EvalImage] = {}
    for r in _read_jsonl(os.path.join(catalog_dir, "images", f"{dataset_id}.jsonl")):
        imgs[r["image_id"]] = EvalImage(
            image_id=r["image_id"],
            dataset_id=dataset_id,
            gcs_uri=r["gcs_uri"],
            local_path=local_path_for(r["gcs_uri"], data_root),
            width=int(r["width"]),
            height=int(r["height"]),
            split=r.get("split") or "all",
            source_ref=r.get("source_ref") or "",
            eval_split=r.get("eval_split"),
        )
    ann_path = os.path.join(catalog_dir, "annotations", f"{dataset_id}.jsonl")
    if os.path.exists(ann_path):
        for a in _read_jsonl(ann_path):
            im = imgs.get(a["image_id"])
            if im is None:
                continue
            if a["label"] == "horizon":
                attrs = a.get("attrs")
                im.horizon = json.loads(attrs) if isinstance(attrs, str) else attrs
                continue
            box = a.get("box") or []
            if len(box) != 4:
                continue
            im.boxes.append([float(v) for v in box])
            im.labels.append(a["label"])
    return sorted(imgs.values(), key=lambda im: im.image_id)


def _load_splits() -> dict:
    with open(SPLITS_YAML) as f:
        return yaml.safe_load(f)


# dataset_id -> (dev upstream splits, test upstream splits). SeaShips/Singapore follow the upstream split
# (the catalog has no source-video ids for them, so the upstream split is the only split we can honour).
UPSTREAM_SPLITS = {
    "seaships7000": ({"train", "valid"}, {"test"}),
    "singapore_maritime": ({"train", "validation"}, {"test"}),
}


def eval_set(dataset_id: str, split: str, catalog_dir: str, data_root: str, stride: int = 1,
             only_with_gt: bool = False) -> list[EvalImage]:
    """split is 'dev' or 'test' per configs/splits.yaml; stride subsamples deterministically (dev only)."""
    assert split in ("dev", "test"), split
    images = load_dataset(dataset_id, catalog_dir, data_root)
    explicit = [im for im in images if im.eval_split is not None]
    if explicit:
        if len(explicit) != len(images):
            raise ValueError("mixed legacy/clean split metadata; refusing an ambiguous evaluation set")
        images = [im for im in images if im.eval_split == split]
    elif dataset_id == "smd_vis_onboard_sample":
        vids = set(_load_splits()[split]["smd_vis_onboard_sample"])
        images = [im for im in images if im.video in vids]
    elif dataset_id in UPSTREAM_SPLITS:
        dev, test = UPSTREAM_SPLITS[dataset_id]
        want = dev if split == "dev" else test
        images = [im for im in images if im.split in want]
    else:
        raise ValueError(f"no split rule for {dataset_id}")
    if only_with_gt:
        images = [im for im in images if im.boxes]
    if stride > 1:
        assert split == "dev", "never subsample test"
        images = images[::stride]
    return images


def clip_frames(clip_name: str, catalog_dir: str, data_root: str) -> list[EvalImage]:
    """Unlabeled 1-fps clip frames (frames/<Name>_frames/sec_NNNN.jpg)."""
    ds = "video_" + clip_name.lower()
    return load_dataset(ds, catalog_dir, data_root)


def clip_split(clip_name: str) -> str:
    s = _load_splits()
    return "test" if clip_name in s["test"]["ghost_fleet_clips"] else "dev"
