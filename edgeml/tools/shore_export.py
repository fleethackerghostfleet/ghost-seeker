#!/usr/bin/env python3
"""Freeze clean-v2 shore COCO data in a NEW GCS prefix; never overwrite a freeze.

Requires edgeml[audit], Pillow, an authenticated gcloud, and splits.yaml.
Training data has train/valid only. Locked sets live under locked/, separately.
"""
from __future__ import annotations

import argparse
import base64
import collections
import concurrent.futures
import configparser
import hashlib
import io
import json
import math
import re
import sys
from pathlib import Path

import numpy as np
import yaml
from PIL import Image

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
from edgeml.detectors.labels import agnostic

BUCKET = "qwiklabs-gcp-02-34a870014566-ghost-seeker"
PROJECT = "qwiklabs-gcp-02-34a870014566"
CLEAN = "clean/v1/data-quality-20261105-reviewed/"
BUCKETS = ("tiny", "small", "medium", "large")


def size_bucket(area):
    return BUCKETS[0 if area < 256 else 1 if area < 1024 else 2 if area < 9216 else 3]


def route(dataset, source, upstream, policy):
    if dataset == "singapore_maritime":
        parts = policy["singapore_maritime_v2"]
        found = [k for k, v in parts.items() if source in v]
        if len(found) != 1:
            raise ValueError(f"Missing/ambiguous Singapore source: {source}")
        return {"train": "train", "dev": "valid", "test": "test_sg"}[found[0]]
    if dataset == "seaships7000":
        if upstream not in {"dev", "test"}:
            raise ValueError(f"Unexpected SeaShips split: {upstream}")
        return "train" if upstream == "dev" else "test_seaships"
    if dataset == "smd_vis_onboard_sample":
        norm = lambda s: re.sub(r"[^a-z0-9]", "", s.lower())
        return "test_smd" if norm(source) in map(norm, policy["test"][dataset]) else "valid" if norm(source) in map(norm, policy["dev"][dataset]) else None
    if source in policy.get("quarantine", {}).get(dataset, []):
        return None
    if source in policy["test"].get(dataset, []):
        return {"fvessel_clip10": "test_fvessel", "ghost_fleet_5fps": "test_gf"}[dataset]
    return "train" if source in policy["dev"].get(dataset, []) else None


def encode(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()


def sha(data):
    return hashlib.sha256(data).hexdigest()


def destination(split):
    return f"dataset/{split}" if split in {"train", "valid"} else f"locked/{split}"


def catalog_frame_index(uri):
    match = re.search(r"(?:frame_|_f)(\d+)(?:\.[^.]+)?$", Path(uri).name)
    return int(match[1]) if match else None


def catalog_annotation(row):
    if row["label"] == "horizon" and not row["box"]:
        return None
    return {"bbox": row["box"], "ignore": row["label_norm"] != "vessel"}


def main():
    from google.cloud import bigquery, storage
    from inventory_gcs import credentials

    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--prefix", required=True, help="New edgeml/shore/<freeze-id> prefix")
    ap.add_argument("--output", type=Path, required=True, help="New local metadata directory, outside repository")
    ap.add_argument("--manifests", type=Path, required=True, help="New directory of small commit-ready manifests")
    ap.add_argument("--splits", type=Path, default=Path(__file__).parents[1] / "configs/splits.yaml")
    ap.add_argument("--workers", type=int, default=16)
    ap.add_argument("--resume-incomplete", action="store_true", help="Resume only an unfinished prefix; verify existing bytes, never overwrite")
    args = ap.parse_args()
    prefix = args.prefix.strip("/") + "/"
    if not prefix.startswith("edgeml/shore/") or ".." in prefix.split("/"):
        ap.error("Expected edgeml/shore/<freeze-id>")
    auth = credentials()
    client = storage.Client(project=PROJECT, credentials=auth)
    bucket = client.bucket(BUCKET)
    if args.resume_incomplete and bucket.blob(prefix + "COMPLETE.json").exists():
        ap.error("Completed freezes cannot be resumed")
    if not args.resume_incomplete and next(iter(client.list_blobs(BUCKET, prefix=prefix, max_results=1)), None):
        ap.error("Destination exists; choose a new freeze ID")
    args.output.mkdir(parents=True, exist_ok=args.resume_incomplete)
    args.manifests.mkdir(parents=True, exist_ok=args.resume_incomplete)
    policy = yaml.safe_load(args.splits.read_text())
    sg = policy["singapore_maritime_v2"]
    assert [len(sg[k]) for k in ("train", "dev", "test")] == [26, 5, 5]
    assert len(set(sum(sg.values(), []))) == 36

    def read(path):
        return bucket.blob(path.removeprefix(f"gs://{BUCKET}/")).download_as_bytes()

    def publish(path, data):
        if args.resume_incomplete:
            existing = bucket.get_blob(prefix + path)
            if existing is not None:
                digest = base64.b64encode(hashlib.md5(data).digest()).decode()
                if existing.md5_hash != digest:
                    raise ValueError(f"Resume bytes differ: {path}")
                return
        bucket.blob(prefix + path).upload_from_string(data, if_generation_match=0, timeout=180)

    def artifact(path, data):
        local = args.output / path
        local.parent.mkdir(parents=True, exist_ok=True)
        local.write_bytes(data)
        publish(path, data)

    where = 'dataset_id IN ("seaships7000", "singapore_maritime", "smd_vis_onboard_sample")'
    catalog = {}
    for table in ("images", "annotations"):
        if args.resume_incomplete:
            rows = json.loads(read(prefix + f"inputs/{table}_clean_v2.json"))
        else:
            bq = bigquery.Client(project=PROJECT, credentials=auth)
            rows = [dict(r) for r in bq.query(f"SELECT * FROM `{PROJECT}.ghost_fleet.{table}_clean_v2` WHERE {where}").result()]
        catalog[table] = rows
        artifact(f"inputs/{table}_clean_v2.json", encode(rows))
    artifact("inputs/splits.yaml", args.splits.read_bytes())
    selected = read(CLEAN + "selection.jsonl")
    artifact("inputs/selection.jsonl", selected)
    excluded = {r["ref"].removeprefix(f"gs://{BUCKET}/") for r in map(json.loads, selected.splitlines()) if r["status"] == "quarantine"}
    annotations = collections.defaultdict(list)
    for a in catalog["annotations"]:
        box = catalog_annotation(a)
        if box is not None:
            annotations[a["dataset_id"], a["image_id"]].append(box)
    frames = []
    for im in catalog["images"]:
        split = route(im["dataset_id"], im["source_video"], im["eval_split"], policy)
        frames.append({"split": split, "uri": im["gcs_uri"], "original_id": im["image_id"],
                       "dataset": im["dataset_id"], "source_video": im["source_video"],
                       "frame_index": catalog_frame_index(im["gcs_uri"]), "fps": None,
                       "expected_sha256": im["sha256"], "annotations": annotations[im["dataset_id"], im["image_id"]]})
    for ds in ("fvessel_clip10", "ghost_fleet_5fps"):
        for seq in policy["dev"][ds] + policy["test"][ds]:
            base = CLEAN + f"mot/{ds}/{seq}/"
            metadata = {}
            for name in ("seqinfo.ini", "gt/labels.json", "gt/gt.txt"):
                raw = read(base + name)
                artifact(f"inputs/mot/{ds}/{seq}/{name}", raw)
                metadata[name] = raw.decode()
            ini = configparser.ConfigParser()
            ini.read_string(metadata["seqinfo.ini"])
            fps = float(ini["Sequence"]["framerate"])
            classes = json.loads(metadata["gt/labels.json"])["classes"]
            gt = collections.defaultdict(list)
            for line in metadata["gt/gt.txt"].splitlines():
                if not line.strip():
                    continue
                v = list(map(float, line.split(",")))
                gt[int(v[0])].append({"bbox": v[2:6], "track_id": int(v[1]), "ignore": agnostic(classes[str(int(v[7]))]) != "vessel"})
            for blob in client.list_blobs(BUCKET, prefix=f"edgeml/eval/{ds}/{seq}/img1/"):
                if not blob.name.lower().endswith((".jpg", ".png", ".jpeg")) or blob.name in excluded:
                    continue
                index = int(Path(blob.name).stem)
                frames.append({"split": route(ds, seq, None, policy), "uri": f"gs://{BUCKET}/{blob.name}",
                               "original_id": f"{ds}/{seq}/{index}", "dataset": ds, "source_video": seq,
                               "frame_index": index, "fps": fps, "expected_sha256": None, "annotations": gt[index]})
    assert {f["source_video"] for f in frames if f["dataset"] == "singapore_maritime"} == set(sum(sg.values(), []))
    if any(f["split"] is None for f in frames):
        raise ValueError("Unassigned source in export")
    frames.sort(key=lambda f: (f["split"], f["original_id"]))

    def export_frame(f):
        raw = read(f["uri"])
        digest = sha(raw)
        if f["expected_sha256"] and digest != f["expected_sha256"]:
            raise ValueError(f"SHA changed: {f['uri']}")
        im = Image.open(io.BytesIO(raw)).convert("RGB")
        mean_y = float(np.asarray(im.convert("YCbCr"))[:, :, 0].mean())
        filename = sha(f["original_id"].encode())[:24] + Path(f["uri"]).suffix.lower()
        publish(destination(f["split"]) + "/" + filename, raw)
        visual_low = f["dataset"] == "singapore_maritime" and f["source_video"] == "mvi1578vis"
        return {**f, "file_name": filename, "sha256": digest, "width": im.width, "height": im.height,
                "mean_y": round(mean_y, 4), "low_light": mean_y < 60 or visual_low,
                "low_light_method": "visual_twilight_source" if visual_low else "mean_y_lt_60"}

    groups = collections.defaultdict(list)
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        for n, f in enumerate(pool.map(export_frame, frames), 1):
            groups[f["split"]].append(f)
            if n % 1000 == 0:
                print(f"Exported {n}/{len(frames)}", flush=True)
    summaries = {}
    for split, fs in sorted(groups.items()):
        images, anns, entries = [], [], []
        counts = dict.fromkeys(BUCKETS, 0)
        for i, f in enumerate(fs, 1):
            image = {k: f[k] for k in ("file_name", "width", "height", "original_id", "dataset", "source_video", "frame_index", "fps", "mean_y", "low_light", "low_light_method")}
            image["id"] = i
            images.append(image)
            per_image = dict.fromkeys(BUCKETS, 0)
            for a in f["annotations"]:
                box = list(map(float, a["bbox"]))
                if len(box) != 4 or not all(math.isfinite(v) for v in box) or min(box[2:]) <= 0:
                    raise ValueError(f"Invalid bbox: {f['original_id']}: {box}")
                ann = {**a, "id": len(anns) + 1, "image_id": i, "category_id": 1, "bbox": box,
                       "area": box[2] * box[3], "iscrowd": int(a["ignore"]), "ignore": int(a["ignore"])}
                anns.append(ann)
                if not a["ignore"]:
                    key = size_bucket(ann["area"])
                    counts[key] += 1
                    per_image[key] += 1
            entries.append([f["file_name"], f["sha256"], *per_image.values()])
        coco = encode({"info": {"split": split, "freeze": prefix, "label_policy": "single vessel; non-vessel GT is ignore/crowd"},
                       "images": images, "annotations": anns, "categories": [{"id": 1, "name": "vessel", "supercategory": "vessel"}]})
        coco_path = destination(split) + "/_annotations.coco.json"
        artifact(coco_path, coco)
        shards = []
        for start in range(0, len(entries), 2000):
            name = f"{split}.images.{start // 2000:03d}.json"
            data = encode({"columns": ["file_name", "sha256", *BUCKETS], "images": entries[start:start + 2000]})
            (args.manifests / name).write_bytes(data)
            artifact("manifests/" + name, data)
            shards.append({"file": name, "sha256": sha(data)})
        manifest = {"split": split, "uri": f"gs://{BUCKET}/{prefix}{coco_path}", "coco_sha256": sha(coco),
                    "images": len(images), "vessel_boxes": sum(counts.values()), "buckets": counts,
                    "low_light_frames": sum(f["low_light"] for f in fs),
                    "low_light_vessels": sum(sum(not a["ignore"] for a in f["annotations"]) for f in fs if f["low_light"]),
                    "sources": sorted({f["dataset"] + "/" + f["source_video"] for f in fs}), "image_shards": shards}
        data = encode(manifest)
        (args.manifests / f"{split}.json").write_bytes(data)
        artifact(f"manifests/{split}.json", data)
        summaries[split] = {"images": len(images), **counts}
        print(split, summaries[split], flush=True)
    artifact("COMPLETE.json", encode({"splits": summaries, "policy_sha256": sha(args.splits.read_bytes())}))


if __name__ == "__main__":
    main()
