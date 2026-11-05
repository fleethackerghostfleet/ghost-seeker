#!/usr/bin/env python3
"""Register the synthetic unusual-contact set (task H, `edgeml/eval/synth_unknown`) in the dataset catalog.

Same catalog schemas as datasets/README.md / research/edgeml/scripts/register_r2_catalog.py: writes catalog/images/<id>.jsonl,
catalog/annotations/<id>.jsonl and upserts one row in catalog/datasets.jsonl (generation-matched). Synthetic tracks carry
`attrs.synthetic=true` + `synth_class`; the original background GT vessels keep `synthetic=false`.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import random
import subprocess
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

BUCKET = "qwiklabs-gcp-02-34a870014566-ghost-seeker"
DATASET_ID = "edgeml_synth_unknown"
DATASET_FIELDS = ("dataset_id", "name", "source_type", "source_url", "source_revision", "license", "viewpoint", "modality",
                  "synthetic", "annotation_format", "native_classes", "num_images", "num_annotations", "bytes", "raw_prefix",
                  "images_prefix", "ingested_at", "ingest_script", "notes")
IMAGE_FIELDS = ("image_id", "dataset_id", "gcs_uri", "split", "width", "height", "bytes", "sha256", "source_ref", "timestamp_s",
                "has_annotations")
ANNOTATION_FIELDS = ("image_id", "dataset_id", "label", "label_norm", "box", "conf", "source", "attrs")


def normalized(row: dict[str, Any], fields: tuple[str, ...]) -> dict[str, Any]:
    return {f: row.get(f) for f in fields}


def write_jsonl(path: Path, rows: list[dict[str, Any]], fields: tuple[str, ...]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as fh:
        for r in rows:
            fh.write(json.dumps(normalized(r, fields), ensure_ascii=False, separators=(",", ":")) + "\n")


def jsonl(path: Path) -> list[dict[str, Any]]:
    return [json.loads(l) for l in path.read_text(encoding="utf-8").splitlines() if l.strip()] if path.exists() else []


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def gcloud(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["gcloud", *args], check=check, capture_output=True, text=True)


def catalog_rows(root: Path) -> tuple[dict[str, Any], list[dict[str, Any]], list[dict[str, Any]]]:
    index = {r["seq_id"]: r for r in jsonl(root / "index.jsonl")}
    if not index:
        raise ValueError(f"missing {root / 'index.jsonl'}")
    images, anns, total, classes, backgrounds = [], [], 0, set(), set()
    for seq_id, meta in sorted(index.items()):
        seq_dir = root / seq_id
        info = dict(l.split("=", 1) for l in (seq_dir / "seqinfo.ini").read_text().splitlines() if "=" in l)
        labels = json.loads((seq_dir / "gt" / "labels.json").read_text())["classes"]
        synth = json.loads((seq_dir / "synth.json").read_text()) if (seq_dir / "synth.json").exists() else {}
        synth_tracks = set(int(t) for t in meta.get("synth_tracks", []))
        classes.update(labels.values())
        backgrounds.add(meta["background"])
        gt_by_frame: dict[int, list[list[str]]] = {}
        for line in (seq_dir / "gt" / "gt.txt").read_text().splitlines():
            f = line.split(",")
            gt_by_frame.setdefault(int(f[0]), []).append(f)
        n = int(info["seqLength"])
        for frame in range(1, n + 1):
            img = seq_dir / "img1" / f"{frame:06d}.jpg"
            nbytes = img.stat().st_size
            total += nbytes
            image_id = f"{DATASET_ID}/{meta['split']}/{seq_id}_{frame:06d}"
            images.append({"image_id": image_id, "dataset_id": DATASET_ID,
                           "gcs_uri": f"gs://{BUCKET}/edgeml/eval/{root.name}/{seq_id}/img1/{frame:06d}.jpg",
                           "split": meta["split"], "width": int(info["imWidth"]), "height": int(info["imHeight"]),
                           "bytes": nbytes, "sha256": sha256_file(img),
                           "source_ref": f"{meta['background']}@{int(info.get('bg_start_frame', 1)) + frame - 1}",
                           "timestamp_s": float(frame - 1) / float(meta.get("fps", 1.0)), "has_annotations": bool(gt_by_frame.get(frame))})
            for g in gt_by_frame.get(frame, []):
                _, tid, x, y, w, h, _conf, cid, vis = g[:9]
                is_synth = int(tid) in synth_tracks
                anns.append({"image_id": image_id, "dataset_id": DATASET_ID, "label": labels[str(int(float(cid)))],
                             "label_norm": None, "box": [float(x), float(y), float(w), float(h)], "conf": 1.0,
                             "source": "synthetic:composite" if is_synth else "gt",
                             "attrs": json.dumps({"track_id": int(tid), "visibility": float(vis), "synthetic": is_synth,
                                                  "synth_class": meta["synth_class"] if is_synth else None,
                                                  "class_id": int(float(cid)), "background": meta["background"],
                                                  "sprite_source": synth.get("sprite_source", "gemini-2.5-flash-image") if is_synth else None},
                                                 ensure_ascii=False, separators=(",", ":"))})
    row = {"dataset_id": DATASET_ID, "name": "Synthetic unusual contacts composited on dev sea footage (task H)",
           "source_type": "synthetic", "source_url": None, "source_revision": None,
           "license": "derived from team video frames (see per-background sources/*.json); Gemini-generated cut-outs; internal use only",
           "viewpoint": "mixed", "modality": "rgb", "synthetic": True, "annotation_format": "mot",
           "native_classes": sorted(classes), "num_images": len(images), "num_annotations": len(anns), "bytes": total,
           "raw_prefix": f"gs://{BUCKET}/edgeml/eval/synth_unknown_sprites/",
           "images_prefix": f"gs://{BUCKET}/edgeml/eval/{root.name}/",
           "ingested_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
           "ingest_script": "research/edgeml/scripts/register_synth_unknown_catalog.py",
           "notes": "Dev-only backgrounds: " + ", ".join(sorted(backgrounds)) +
                    ". split=test rows are the held-out synthetic classes (periscope_mast, life_raft, debris_container); never tune on them."}
    return row, images, anns


def read_remote_datasets() -> list[dict[str, Any]]:
    r = gcloud("storage", "cat", f"gs://{BUCKET}/catalog/datasets.jsonl", check=False)
    if r.returncode:
        if any(m in r.stderr.lower() for m in ("matched no objects", "no urls matched", "404", "not found")):
            return []
        raise RuntimeError(r.stderr.strip())
    return [json.loads(l) for l in r.stdout.splitlines() if l.strip()]


def upsert(rows: list[dict[str, Any]], row: dict[str, Any]) -> list[dict[str, Any]]:
    out = [normalized(r, DATASET_FIELDS) for r in rows if r.get("dataset_id") != DATASET_ID]
    out.append(normalized(row, DATASET_FIELDS))
    return out


def remote_generation(uri: str) -> int:
    r = gcloud("storage", "objects", "describe", uri, "--format=value(generation)", check=False)
    if r.returncode:
        if any(m in r.stderr.lower() for m in ("404", "not found", "matched no objects")):
            return 0
        raise RuntimeError(r.stderr.strip())
    return int(r.stdout.strip())


def upload_and_upsert(row: dict[str, Any], image_path: Path, ann_path: Path) -> None:
    gcloud("storage", "cp", str(image_path), f"gs://{BUCKET}/catalog/images/{DATASET_ID}.jsonl")
    gcloud("storage", "cp", str(ann_path), f"gs://{BUCKET}/catalog/annotations/{DATASET_ID}.jsonl")
    uri = f"gs://{BUCKET}/catalog/datasets.jsonl"
    for attempt in range(5):
        gen = remote_generation(uri)
        updated = upsert(read_remote_datasets(), row)
        with tempfile.TemporaryDirectory(prefix="ghost-seeker-catalog-") as d:
            p = Path(d) / "datasets.jsonl"
            write_jsonl(p, updated, DATASET_FIELDS)
            r = gcloud("storage", "cp", f"--if-generation-match={gen}", str(p), uri, check=False)
        if r.returncode == 0:
            return
        if "412" not in r.stderr and "precondition" not in r.stderr.lower():
            raise RuntimeError(r.stderr.strip())
        time.sleep(random.uniform(0.1, 0.4))
    raise RuntimeError("catalog changed during all 5 write attempts")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", type=Path, default=Path("~/data/edgeml_eval/synth_unknown").expanduser())
    ap.add_argument("--dry-run", action="store_true")
    a = ap.parse_args()
    row, images, anns = catalog_rows(a.root.expanduser())
    out = a.root.expanduser() / "catalog"
    image_path, ann_path = out / "images" / f"{DATASET_ID}.jsonl", out / "annotations" / f"{DATASET_ID}.jsonl"
    write_jsonl(image_path, images, IMAGE_FIELDS)
    write_jsonl(ann_path, anns, ANNOTATION_FIELDS)
    before = read_remote_datasets()
    n_synth = sum(json.loads(r["attrs"])["synthetic"] for r in anns)
    print(f"{'dry-run' if a.dry_run else 'write'} dataset_id={DATASET_ID} images={len(images)} annotations={len(anns)} "
          f"dataset_rows={len(before)}->{len(upsert(before, row))} synthetic_boxes={n_synth}")
    print(json.dumps(normalized(row, DATASET_FIELDS), ensure_ascii=False))
    if not a.dry_run:
        upload_and_upsert(row, image_path, ann_path)
        print("catalog files uploaded and dataset row upserted")


if __name__ == "__main__":
    main()
