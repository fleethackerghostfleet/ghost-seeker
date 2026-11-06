#!/usr/bin/env python3
"""Ingest SeaDronesSee ODv2 (aerial UAV, CC0-1.0) from the pinned HF mirror, non-destructively.

* raw/seadronessee/       immutable snapshot (verified, never overwritten)
* datasets/seadronessee/images/{train,val}/<id>.jpg   model-ready copies (upstream split kept in `split`)
* catalog/{images,annotations}/seadronessee.jsonl + datasets.jsonl row
* <out>/sequences.jsonl   inferred recording per image + recording-level eval_split (train/dev/test/guard)

The HF mirror drops the official per-image video metadata and the upstream train/val split interleaves single
frames of the same video, so the upstream split cannot be used for held-out evaluation. Recordings are inferred:
images are ordered by id (ids are extraction order), and a new segment starts on a resolution change, an id gap
> --max-gap, or a thumbnail change > --cut. Segments are grouped into contiguous id blocks (>= --block images);
blocks get a deterministic train/dev/test split and the --guard images at either side of a split change are
marked `guard` (used for nothing).

  python datasets/ingest/seadronessee.py --snapshot ~/data/sds_snapshot --out ~/data/sds
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import numpy as np
import yaml
from PIL import Image

sys.path.insert(0, str(Path(__file__).resolve().parent))
from common import (GCLOUD, ensure_raw_matches_or_upload, gcs_uri, load_yolo_boxes, sha256_file,  # noqa: E402
                    utc_timestamp, write_catalog)

DATASET = "seadronessee"
REPO = "dronefreak/SeaDronesSee"
REVISION = "deb1f872ffe7efa4b7ee3a672562d16b6dc0ca5c"
SPLITS = (("test", 0.15), ("dev", 0.15))


def thumb(path: Path) -> tuple[tuple[int, int], np.ndarray]:
    with Image.open(path) as im:
        size = im.size
        im.draft("L", (128, 96))
        t = np.asarray(im.convert("L").resize((32, 24)), dtype=np.float32)
    return size, (t - t.mean()) / (t.std() + 1e-6)


def block_split(block: int) -> str:
    u = int(hashlib.sha256(f"{DATASET}:block{block}".encode()).hexdigest()[:8], 16) / 0xFFFFFFFF
    acc = 0.0
    for name, frac in SPLITS:
        acc += frac
        if u < acc:
            return name
    return "train"


def infer_sequences(items, max_gap, cut, block, guard):
    """items: [(id, size, thumb)] sorted by id -> per-id (sequence_id, eval_split)."""
    seg, segs, hard = 0, [], []
    for i, (iid, size, t) in enumerate(items):
        is_hard = False
        if i:
            pid, psize, pt = items[i - 1]
            is_hard = size != psize or iid - pid > max_gap
            if is_hard or float(np.mean(np.abs(t - pt))) > cut:
                seg += 1
        segs.append(seg)
        hard.append(is_hard)
    # contiguous blocks of whole segments; prefer to end a block at a hard cut (resolution change / id gap)
    blocks, b, count = [], 0, 0
    for i, s in enumerate(segs):
        if i and s != segs[i - 1] and ((hard[i] and count >= block) or count >= 4 * block):
            b, count = b + 1, 0
        blocks.append(b)
        count += 1
    splits = [block_split(x) for x in blocks]
    out = {}
    for i, (iid, _, _) in enumerate(items):
        lo, hi = max(0, i - guard), min(len(items), i + guard + 1)
        near_change = any(splits[j] != splits[i] for j in range(lo, hi))
        out[iid] = (f"sds_seq{segs[i]:04d}", f"sds_block{blocks[i]:03d}", "guard" if near_change else splits[i])
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--snapshot", type=Path, required=True, help=f"local snapshot of {REPO}@{REVISION}")
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--max-gap", type=int, default=12)
    ap.add_argument("--cut", type=float, default=0.55)
    ap.add_argument("--block", type=int, default=150)
    ap.add_argument("--guard", type=int, default=8)
    ap.add_argument("--skip-upload", action="store_true", help="compute rows/sequences only (dry run)")
    a = ap.parse_args()
    data = a.snapshot / "data"
    names = yaml.safe_load((data / "data.yaml").read_text())["names"]
    classes = [names[i] for i in sorted(names)]
    paths = sorted(p for p in (data / "images").rglob("*") if p.suffix.lower() in {".jpg", ".png"}
                   and re.fullmatch(r"\d+", p.stem))
    with ThreadPoolExecutor(16) as pool:
        thumbs = list(pool.map(thumb, paths))
        shas = list(pool.map(sha256_file, paths))
    order = sorted(range(len(paths)), key=lambda i: int(paths[i].stem))
    seq = infer_sequences([(int(paths[i].stem), thumbs[i][0], thumbs[i][1]) for i in order],
                          a.max_gap, a.cut, a.block, a.guard)
    images, anns, seq_rows = [], [], []
    for i, p in enumerate(paths):
        split = p.relative_to(data / "images").parts[0]
        iid = int(p.stem)
        image_id = f"{DATASET}/{split}/{iid}"
        (w, h) = thumbs[i][0]
        boxes = load_yolo_boxes(data / "labels" / p.relative_to(data / "images").with_suffix(".txt"), classes, w, h)
        images.append({"image_id": image_id, "dataset_id": DATASET,
                       "gcs_uri": gcs_uri(f"datasets/{DATASET}/images/{split}/{p.name}"), "split": split,
                       "width": w, "height": h, "bytes": p.stat().st_size, "sha256": shas[i],
                       "source_ref": p.relative_to(a.snapshot).as_posix(), "timestamp_s": None,
                       "has_annotations": bool(boxes)})
        anns += [dict(b, image_id=image_id, dataset_id=DATASET) for b in boxes]
        s, blk, ev = seq[iid]
        seq_rows.append({"image_id": image_id, "frame_id": iid, "upstream_split": split, "sequence_id": s,
                         "block_id": blk, "eval_split": ev})
    a.out.mkdir(parents=True, exist_ok=True)
    with (a.out / "sequences.jsonl").open("w") as f:
        for r in sorted(seq_rows, key=lambda r: r["frame_id"]):
            f.write(json.dumps(r) + "\n")
    for name, rows in (("images", images), ("annotations", anns)):
        with (a.out / f"{DATASET}_{name}.jsonl").open("w") as f:
            for r in rows:
                f.write(json.dumps(r) + "\n")
    from collections import Counter
    summary = {"images": len(images), "annotations": len(anns), "sequences": len({r["sequence_id"] for r in seq_rows}),
               "blocks": len({r["block_id"] for r in seq_rows}),
               "eval_split_images": dict(Counter(r["eval_split"] for r in seq_rows)),
               "labels": dict(Counter(x["label"] for x in anns))}
    print(json.dumps(summary, indent=2))
    if a.skip_upload:
        return
    ensure_raw_matches_or_upload(a.snapshot, gcs_uri(f"raw/{DATASET}/"))
    for split_dir in sorted((data / "images").iterdir()):
        for shard in sorted(split_dir.iterdir()):
            subprocess.run([GCLOUD, "storage", "rsync", "--recursive", "--no-clobber", str(shard),
                            gcs_uri(f"datasets/{DATASET}/images/{split_dir.name}/")], check=True)
    dataset_row = {
        "dataset_id": DATASET, "name": "SeaDronesSee Object Detection v2", "source_type": "huggingface",
        "source_url": f"https://huggingface.co/datasets/{REPO}", "source_revision": REVISION, "license": "cc0-1.0",
        "viewpoint": "aerial", "modality": "rgb", "synthetic": False, "annotation_format": "yolo",
        "native_classes": classes, "num_images": len(images), "num_annotations": len(anns),
        "bytes": sum(r["bytes"] for r in images), "raw_prefix": gcs_uri(f"raw/{DATASET}/"),
        "images_prefix": gcs_uri(f"datasets/{DATASET}/images/"), "ingested_at": utc_timestamp(),
        "ingest_script": "datasets/ingest/seadronessee.py",
        "notes": json.dumps({"upstream_splits": dict(Counter(r["split"] for r in images)),
                             "split_rule": "upstream train/val interleave frames; use sequences.jsonl eval_split",
                             "sequence_summary": summary}, sort_keys=True)}
    write_catalog(dataset_row, images, anns)
    subprocess.run([GCLOUD, "storage", "cp", "--if-generation-match=0", str(a.out / "sequences.jsonl"),
                    gcs_uri(f"datasets/{DATASET}/sequences.jsonl")], check=True)
    print("ingested", DATASET)


if __name__ == "__main__":
    main()
