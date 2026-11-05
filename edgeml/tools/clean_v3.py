#!/usr/bin/env python3
"""Build clean v3 = clean v2 (copied unchanged) + SeaDronesSee + ir_maritime_vessels, as a NEW local tree.

Publish the output with publish_clean_data.py to a new clean/v3/... prefix and *_clean_v3 tables; nothing
existing is edited. Splits are by source recording (never by frame):
  * ir_maritime_vessels: recording = VOC <folder> (camera id + timestamp). Generic folders ("pic", digits, ...)
    have no lineage, so their frames go to train only. Upstream original/images/test (1,000 frames) has no labels
    and is excluded. License unknown -> rows carry internal_dev_only and must not ship in a product model.
  * seadronessee: recording = inferred sequence id from the ingest (`sequences.jsonl`), because the HF mirror
    drops the official per-image video metadata and the upstream train/val split interleaves frames.

  python edgeml/tools/clean_v3.py --v2 ~/data/clean_v2/catalog --ir-raw ~/data/raw/ir_maritime_vessels \
      --ir-catalog ~/data/catalog_raw --sds-catalog ~/data/catalog_raw --sds-sequences ~/data/sds/sequences.jsonl \
      --output ~/data/clean_v3
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import shutil
import xml.etree.ElementTree as ET
from collections import Counter, defaultdict
from pathlib import Path

IR_TYPE = {"liner": "passenger", "bulk carrier": "cargo", "warship": "warship", "sailboat": "sailboat",
           "canoe": "small_boat", "container ship": "container", "fishing boat": "fishing"}
SDS_NORM = {"boat": "vessel", "jetski": "vessel", "swimmer": "person", "buoy": "buoy",
            "life_saving_appliances": "object"}
IR_RECORDING = [re.compile(r"^B\d{7}(\d{8})(\d{6})T$"), re.compile(r"^WIN_(\d{8})_(\d\d)_(\d\d)_(\d\d)_Pro"),
                re.compile(r"^IRX_\d+$")]
SPLIT_FRACTIONS = (("test", 0.15), ("dev", 0.15))  # remainder -> train


def read_jsonl(path: Path) -> list[dict]:
    return [json.loads(line) for line in path.open() if line.strip()]


def write_jsonl(path: Path, rows) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("x") as out:
        for row in rows:
            out.write(json.dumps(row, sort_keys=True) + "\n")


def split_for(group: str, salt: str) -> str:
    """Deterministic recording-level split: the same group id always lands in the same split."""
    u = int(hashlib.sha256(f"{salt}:{group}".encode()).hexdigest()[:8], 16) / 0xFFFFFFFF
    acc = 0.0
    for name, frac in SPLIT_FRACTIONS:
        acc += frac
        if u < acc:
            return name
    return "train"


def ir_recording(folder: str | None) -> tuple[str | None, int | None]:
    """(recording id, local hour) for timestamped camera folders; (None, None) when lineage is unknown."""
    folder = (folder or "").strip()
    for pattern in IR_RECORDING:
        m = pattern.match(folder)
        if m:
            hhmmss = "".join(m.groups()[1:])
            return m.group(0), int(hhmmss[:2]) if hhmmss[:2].isdigit() else None
    return None, None


def clip(box, w, h):
    x1, y1, x2, y2 = max(0.0, box[0]), max(0.0, box[1]), min(float(w), box[2]), min(float(h), box[3])
    return None if x2 - x1 < 1 or y2 - y1 < 1 else [x1, y1, x2 - x1, y2 - y1]


def clean_ir(raw: Path, catalog: Path, issues: list, recordings: list):
    by_id = {r["image_id"]: r for r in read_jsonl(catalog / "ir_images.jsonl")}
    images, anns = [], []
    groups = Counter()
    for xml in sorted((raw / "original/labels/train_xml").glob("*.xml")):
        stem = xml.stem
        root = ET.parse(xml).getroot()
        image_id = f"ir_maritime_vessels/train/{stem}"
        src = by_id.get(image_id)
        if src is None:
            issues.append({"dataset_id": "ir_maritime_vessels", "ref": stem, "reason": "xml_without_catalog_image"})
            continue
        folder = root.findtext("folder")
        embedded = root.findtext("filename")
        if embedded and Path(embedded).stem != stem:
            issues.append({"dataset_id": "ir_maritime_vessels", "ref": stem, "reason": "xml_filename_differs_from_stem",
                           "embedded": embedded, "action": "joined on stem (cropped/*.csv agree with stem join)"})
        rec, hour = ir_recording(folder)
        if rec is None:
            source, eval_split = f"ir_unlineaged_{folder or 'none'}", "train"
        else:
            source, eval_split = f"ir_{rec}", split_for(rec, "ir_maritime_vessels")
        groups[(source, eval_split)] += 1
        w, h = src["width"], src["height"]
        kept = 0
        for o in root.findall("object"):
            b = [float(o.find("bndbox").findtext(k)) for k in ("xmin", "ymin", "xmax", "ymax")]
            box = clip(b, w, h)
            if box is None or b[0] < 0 or b[1] < 0 or b[2] > w or b[3] > h:
                issues.append({"dataset_id": "ir_maritime_vessels", "ref": stem, "reason": "xml_box_out_of_bounds",
                               "box_xyxy": b, "size": [w, h], "action": "clipped" if box else "dropped"})
            if box is None:
                continue
            name = o.findtext("name")
            anns.append({"image_id": image_id, "dataset_id": "ir_maritime_vessels", "label": name,
                         "label_norm": "vessel", "box": box, "conf": 1.0, "source": "gt",
                         "attrs": json.dumps({"ship_type": IR_TYPE.get(name), "truncated": o.findtext("truncated"),
                                              "license_use": "internal_dev_only"}, sort_keys=True)})
            kept += 1
        row = dict(src)
        row.update(source_video=source, eval_split=eval_split, has_annotations=kept > 0)
        row["timestamp_s"] = None
        images.append(row)
        recordings.append({"dataset_id": "ir_maritime_vessels", "image_id": image_id, "source_video": source,
                           "eval_split": eval_split, "hour": hour, "folder": folder})
    for image_id, r in by_id.items():
        if r["split"] == "test":
            issues.append({"dataset_id": "ir_maritime_vessels", "ref": image_id, "reason": "upstream_test_unlabeled",
                           "action": "excluded"})
    return images, anns, groups


def clean_sds(catalog: Path, sequences: Path):
    seq = {r["image_id"]: r for r in read_jsonl(sequences)}
    images, anns = [], []
    groups = Counter()
    for r in read_jsonl(catalog / "seadronessee_images.jsonl"):
        s = seq[r["image_id"]]
        row = dict(r)
        row.update(source_video=s["sequence_id"], eval_split=s["eval_split"])
        groups[(s["sequence_id"], s["eval_split"])] += 1
        images.append(row)
    for a in read_jsonl(catalog / "seadronessee_annotations.jsonl"):
        a = dict(a)
        a["label_norm"] = SDS_NORM.get(a["label"], "object")
        anns.append(a)
    return images, anns, groups


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--v2", type=Path, required=True, help="local copy of clean v2 catalog/ dir")
    ap.add_argument("--ir-raw", type=Path, required=True)
    ap.add_argument("--ir-catalog", type=Path, required=True, help="dir with ir_images.jsonl from catalog/images")
    ap.add_argument("--sds-catalog", type=Path, required=True)
    ap.add_argument("--sds-sequences", type=Path, required=True)
    ap.add_argument("--output", type=Path, required=True)
    a = ap.parse_args()
    out = a.output
    if out.exists():
        ap.error("output exists; clean versions are never rebuilt in place")
    raw_ds = {r["dataset_id"]: r for c in (a.ir_catalog, a.sds_catalog) if (c / "datasets.jsonl").exists()
              for r in read_jsonl(c / "datasets.jsonl")}
    missing = {"ir_maritime_vessels", "seadronessee"} - raw_ds.keys()
    if missing:
        ap.error(f"datasets.jsonl rows missing for {sorted(missing)} in --ir-catalog/--sds-catalog")
    shutil.copytree(a.v2, out / "catalog")
    issues, recordings = [], []
    ir_img, ir_ann, ir_groups = clean_ir(a.ir_raw, a.ir_catalog, issues, recordings)
    sds_img, sds_ann, sds_groups = clean_sds(a.sds_catalog, a.sds_sequences)
    datasets = read_jsonl(a.v2 / "datasets.jsonl")
    for ds, img, ann in (("ir_maritime_vessels", ir_img, ir_ann), ("seadronessee", sds_img, sds_ann)):
        write_jsonl(out / f"catalog/images/{ds}.jsonl", img)
        write_jsonl(out / f"catalog/annotations/{ds}.jsonl", ann)
        row = dict(raw_ds[ds])
        row.update(num_images=len(img), num_annotations=len(ann), bytes=sum(r["bytes"] for r in img),
                   ingest_script="edgeml/tools/clean_v3.py")
        note = {"splits": dict(Counter(r["eval_split"] for r in img)), "split_rule": "source recording"}
        if ds == "ir_maritime_vessels":
            note["license_use"] = "internal_dev_only: upstream card states no license; not for shipped models"
        else:
            note["sequence_rule"] = "inferred from image-id contiguity + resolution + thumbnail similarity"
        row["notes"] = json.dumps(note, sort_keys=True)
        datasets = [d for d in datasets if d["dataset_id"] != ds] + [row]
    (out / "catalog/datasets.jsonl").unlink()
    write_jsonl(out / "catalog/datasets.jsonl", datasets)
    write_jsonl(out / "issues.jsonl", issues)
    write_jsonl(out / "ir_recordings.jsonl", recordings)
    summary = {
        "base": "clean v2 (clean/v1/data-quality-20261105-reviewed), copied unchanged",
        "added": {
            "ir_maritime_vessels": {"images": len(ir_img), "annotations": len(ir_ann),
                                    "recordings": len({g for g, _ in ir_groups}),
                                    "split_images": dict(Counter(r["eval_split"] for r in ir_img))},
            "seadronessee": {"images": len(sds_img), "annotations": len(sds_ann),
                             "sequences": len({g for g, _ in sds_groups}),
                             "split_images": dict(Counter(r["eval_split"] for r in sds_img))},
        },
        "issues": dict(Counter(i["reason"] for i in issues)),
    }
    (out / "audit.json").write_text(json.dumps(summary, indent=2, sort_keys=True))
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
