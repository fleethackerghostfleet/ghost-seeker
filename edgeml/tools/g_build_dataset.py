"""Workstream G: build the single-class ("boat") YOLO fine-tune dataset from DEV data only.

Sources (never test):
  * SeaShips7000 upstream train (-> train) and valid (-> val) from the catalog JSONL.
  * FVessel dev clips (splits.yaml dev/fvessel_clip10), MOT gt -> all frames (frames w/o GT are negatives).
  * Ghost Fleet 5 fps dev clips (splits.yaml dev/ghost_fleet_5fps) with QA'd teacher GT: gt_status
    'complete' -> all frames; 'partial' -> only frames with vessel GT; 'incomplete' -> skipped.
Holdout sequences (--holdout) go to val so model selection has some never-trained-on dev signal.

Usage (on the GPU VM):
  python edgeml/tools/g_build_dataset.py --catalog ~/data/catalog --datasets ~/data/datasets \
      --eval-root ~/data/edgeml_eval --out ~/data/yolo_g
"""
from __future__ import annotations

import argparse
import json
import os
import sys
from collections import Counter
from pathlib import Path

import cv2
import yaml

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
from edgeml.data.mot import MOTSequence, split_seq_ids  # noqa: E402

GF_VESSEL_CLASSES = {1, 2, 3, 4, 13, 14}  # ship, boat, sailboat, submarine, jet ski, kayak (no buoy/animals/aircraft/person)
FV_VESSEL_CLASSES = {1}
DEFAULT_HOLDOUT = ["fvessel_clip08", "fvessel_clip10", "Submarine_surfacing", "Ship_in_the_Distance_Utopia_of_the_Seas"]


def _write(out: Path, split: str, stem: str, img: Path, boxes: list[list[float]], w: int, h: int) -> None:
    (out / "images" / split).mkdir(parents=True, exist_ok=True)
    (out / "labels" / split).mkdir(parents=True, exist_ok=True)
    dst = out / "images" / split / f"{stem}{img.suffix}"
    if not dst.exists():
        os.symlink(img.resolve(), dst)
    lines = []
    for x, y, bw, bh in boxes:
        x1, y1 = max(0.0, x), max(0.0, y)
        x2, y2 = min(float(w), x + bw), min(float(h), y + bh)
        if x2 - x1 < 1 or y2 - y1 < 1:
            continue
        lines.append(f"0 {(x1 + x2) / 2 / w:.6f} {(y1 + y2) / 2 / h:.6f} {(x2 - x1) / w:.6f} {(y2 - y1) / h:.6f}")
    (out / "labels" / split / f"{stem}.txt").write_text("\n".join(lines) + ("\n" if lines else ""))


SMD_VESSEL_LABELS = {"Vessel/ship", "Ferry", "Speed boat", "Kayak", "Sail boat", "Boat"}  # not Buoy/Other/bird


def add_catalog_dataset(ds_id: str, split_map: dict[str, str], catalog: Path, datasets: Path, out: Path,
                        stats: Counter, labels: set[str] | None = None) -> None:
    """Catalog image/annotation JSONL -> YOLO. Splits not in split_map (the upstream test split) are LOCKED and skipped."""
    anns: dict[str, list[list[float]]] = {}
    for line in (catalog / "annotations" / f"{ds_id}.jsonl").read_text().splitlines():
        if line.strip():
            a = json.loads(line)
            if labels is None or a.get("label") in labels:
                anns.setdefault(a["image_id"], []).append(a["box"])
    for line in (catalog / "images" / f"{ds_id}.jsonl").read_text().splitlines():
        if not line.strip():
            continue
        r = json.loads(line)
        split = split_map.get(r["split"])
        if split is None:
            stats[f"{ds_id}_skipped_test"] += 1
            continue
        rel = r["gcs_uri"].split("/datasets/", 1)[1]
        img = datasets / rel
        if not img.exists():
            stats[f"{ds_id}_missing"] += 1
            continue
        boxes = anns.get(r["image_id"], [])
        _write(out, split, f"{ds_id}_" + r["image_id"].replace("/", "_"), img, boxes, r["width"], r["height"])
        stats[f"{ds_id}_{split}_imgs"] += 1
        stats[f"{ds_id}_{split}_boxes"] += len(boxes)


def add_seaships(catalog: Path, datasets: Path, out: Path, stats: Counter) -> None:
    add_catalog_dataset("seaships7000", {"train": "train", "valid": "val"}, catalog, datasets, out, stats)


def add_smd(catalog: Path, datasets: Path, out: Path, stats: Counter) -> None:
    """Singapore Maritime (VIS onshore) dev splits; its test split (and smd_vis_onboard_sample) stay locked."""
    add_catalog_dataset("singapore_maritime", {"train": "train", "validation": "val"}, catalog, datasets, out, stats,
                        labels=SMD_VESSEL_LABELS)


def add_mot_set(eval_root: Path, set_name: str, vessel_classes: set[int], out: Path, holdout: set[str],
                every: int, splits_yaml: Path, stats: Counter) -> None:
    dev = split_seq_ids("dev", set_name, splits_yaml)
    test = set(split_seq_ids("test", set_name, splits_yaml))
    for seq_id in dev:
        assert seq_id not in test, f"{seq_id} is LOCKED test"
        seq = MOTSequence(eval_root / set_name / seq_id)
        status = seq.labels.get("qa", {}).get("gt_status", "complete")
        if status == "incomplete":
            stats[f"{set_name}_skipped_incomplete"] += 1
            continue
        split = "val" if seq_id in holdout else "train"
        by_frame = seq.gt_by_frame()
        first = cv2.imread(str(seq.frame_path(1)))
        h, w = first.shape[:2]
        for f in seq.frame_ids():
            if (f - 1) % every:
                continue
            boxes = [g.box for g in by_frame.get(f, []) if g.class_id in vessel_classes]
            if status == "partial" and not boxes:
                continue
            p = seq.frame_path(f)
            if not p.exists():
                continue
            _write(out, split, f"{set_name}_{seq_id}_{f:06d}", p, boxes, w, h)
            stats[f"{set_name}_{split}_imgs"] += 1
            stats[f"{set_name}_{split}_boxes"] += len(boxes)
        stats[f"{set_name}_seqs_{split}"] += 1


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--catalog", default=os.path.expanduser("~/data/catalog"))
    ap.add_argument("--datasets", default=os.path.expanduser("~/data/datasets"))
    ap.add_argument("--eval-root", default=os.path.expanduser("~/data/edgeml_eval"))
    ap.add_argument("--out", default=os.path.expanduser("~/data/yolo_g"))
    ap.add_argument("--holdout", nargs="*", default=DEFAULT_HOLDOUT)
    ap.add_argument("--every", type=int, default=1, help="use every Nth MOT frame")
    ap.add_argument("--with-smd", action="store_true", help="also add Singapore Maritime dev splits (v2)")
    ap.add_argument("--splits", default=str(Path(__file__).resolve().parents[1] / "configs" / "splits.yaml"))
    a = ap.parse_args()
    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=True)
    stats: Counter = Counter()
    add_seaships(Path(a.catalog), Path(a.datasets), out, stats)
    if a.with_smd:
        add_smd(Path(a.catalog), Path(a.datasets), out, stats)
    add_mot_set(Path(a.eval_root), "fvessel_clip10", FV_VESSEL_CLASSES, out, set(a.holdout), a.every, Path(a.splits), stats)
    add_mot_set(Path(a.eval_root), "ghost_fleet_5fps", GF_VESSEL_CLASSES, out, set(a.holdout), a.every, Path(a.splits), stats)
    data = {"path": str(out), "train": "images/train", "val": "images/val", "names": {0: "boat"}}
    (out / "data.yaml").write_text(yaml.safe_dump(data, sort_keys=False))
    manifest = {"holdout": a.holdout, "every": a.every, "stats": dict(sorted(stats.items())),
                "train_imgs": len(list((out / "images" / "train").iterdir())),
                "val_imgs": len(list((out / "images" / "val").iterdir()))}
    (out / "manifest.json").write_text(json.dumps(manifest, indent=1))
    print(json.dumps(manifest, indent=1))


if __name__ == "__main__":
    main()
