#!/usr/bin/env python3
"""Viewpoint x modality x light x object-size coverage of the clean v3 catalog + the MOT eval clips.

viewpoint  fixed (static surveillance camera: SeaShips, SMD onshore, FVessel) | shore (hand-held from land) |
           onboard (camera on a vessel) | aerial (UAV) | surface (eye level, platform unknown: IR set)
           Video clips are hand-assigned from their titles (VIDEO_VIEW); unverified -> caveat in the report.
light      day / night / twilight from frame luminance (median of a 64x48 grey thumbnail: <45 night, <80 twilight);
           IR uses the recording timestamp hour instead (19-05 night), `unknown` when the folder has no timestamp.
size       per vessel box, long side after letterbox to 640: far < 24 px, mid 24-96 px, near >= 96 px.

  python edgeml/tools/coverage_table.py --catalog ~/data/clean_v3/catalog --data-root ~/data \
      --ir-recordings ~/data/clean_v3/ir_recordings.jsonl --eval-root ~/data/edgeml_eval --out ~/runs/coverage.json
"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
from collections import Counter, defaultdict
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import numpy as np
from PIL import Image

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

VESSEL = {"seaships7000": None, "singapore_maritime": {"Vessel/ship", "Ferry", "Speed boat", "Kayak", "Sail boat", "Boat"},
          "smd_vis_onboard_sample": {"Vessel/ship", "Ferry", "Speed boat", "Kayak", "Sail boat", "Boat"},
          "ir_maritime_vessels": None, "seadronessee": {"boat", "jetski"}}
VIEW = {"seaships7000": "fixed", "singapore_maritime": "fixed", "smd_vis_onboard_sample": "onboard",
        "seadronessee": "aerial", "ir_maritime_vessels": "surface", "fvessel_clip10": "fixed"}
SHORE_CLIPS = {"dolphins_pungo_ferry_bridge", "ferry_passing_by_on_river", "two_dolphins_near_the_coast_of_gibraltar"}
GF_VESSEL_CLASSES = {1, 2, 3, 4, 13, 14}


def video_view(name: str) -> str:
    return "shore" if name.lower().removeprefix("video_") in SHORE_CLIPS else "onboard"


def light_of(path) -> str:
    try:
        with Image.open(path) as im:
            im.draft("L", (128, 96))
            g = float(np.median(np.asarray(im.convert("L").resize((64, 48)))))
    except Exception:
        return "unknown"
    return "night" if g < 45 else "twilight" if g < 80 else "day"


def size_bucket(box, w, h) -> str:
    s = max(box[2], box[3]) * 640 / max(w, h)
    return "far" if s < 24 else "mid" if s < 96 else "near"


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--catalog", type=Path, required=True)
    ap.add_argument("--data-root", type=Path, required=True)
    ap.add_argument("--ir-recordings", type=Path, required=True)
    ap.add_argument("--eval-root", type=Path, required=True)
    ap.add_argument("--video-stride", type=int, default=10, help="sample every Nth unlabeled video frame for light")
    ap.add_argument("--out", type=Path, required=True)
    a = ap.parse_args()
    from edgeml.data.catalog import load_dataset
    from edgeml.data.mot import MOTSequence

    ir_hour = {r["image_id"]: r["hour"] for r in map(json.loads, a.ir_recordings.open())}
    cells = defaultdict(lambda: Counter())  # (view, modality, light) -> counts
    per_ds = {}
    for f in sorted((a.catalog / "images").glob("*.jsonl")):
        ds = f.stem
        imgs = load_dataset(ds, str(a.catalog), str(a.data_root))
        if ds.startswith("video_"):
            view, imgs_light = video_view(ds), imgs[:: a.video_stride]
            tmp = a.data_root / "coverage_frames" / ds
            tmp.mkdir(parents=True, exist_ok=True)
            uris = [im.gcs_uri for im in imgs_light if not (tmp / Path(im.gcs_uri).name).exists()]
            if uris:
                subprocess.run(["gcloud", "storage", "cp", "-q", *uris, str(tmp)], check=False,
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            paths = [tmp / Path(im.gcs_uri).name for im in imgs_light]
        else:
            view, imgs_light = VIEW[ds], imgs
            paths = [im.local_path for im in imgs]
        modality = "ir" if ds == "ir_maritime_vessels" else "rgb"
        if modality == "ir":
            lights = [("unknown" if ir_hour.get(im.image_id) is None else
                       "night" if (ir_hour[im.image_id] >= 19 or ir_hour[im.image_id] < 5) else "day") for im in imgs]
        else:
            with ThreadPoolExecutor(16) as pool:
                sampled = list(pool.map(light_of, paths))
            if len(imgs_light) != len(imgs):  # extrapolate the sampled mix to all frames of the clip
                lights = [sampled[min(i // a.video_stride, len(sampled) - 1)] for i in range(len(imgs))]
            else:
                lights = sampled
        ds_c = Counter()
        for im, light in zip(imgs, lights):
            key = (view, modality, light)
            cells[key]["images"] += 1
            ds_c[f"images_{light}"] += 1
            keep = VESSEL.get(ds)
            for b, lab in zip(im.boxes, im.labels):
                if keep is not None and lab not in keep:
                    continue
                sb = size_bucket(b, im.width, im.height)
                cells[key][f"boxes_{sb}"] += 1
                ds_c[f"boxes_{sb}"] += 1
        per_ds[ds] = {"view": view, "modality": modality, **ds_c}
    for set_name in ("ghost_fleet_5fps", "fvessel_clip10"):
        for seq_dir in sorted(p for p in (a.eval_root / set_name).iterdir() if p.is_dir()):
            seq = MOTSequence(seq_dir)
            view = VIEW.get(set_name) or video_view(seq_dir.name)
            by_frame = seq.gt_by_frame()
            fids = list(seq.frame_ids())
            first = Image.open(seq.frame_path(fids[0]))
            w, h = first.size
            light = light_of(seq.frame_path(fids[len(fids) // 2]))
            ds_c = Counter()
            for fr in fids:
                cells[(view, "rgb", light)]["images"] += 1
                ds_c[f"images_{light}"] += 1
                for g in by_frame.get(fr, []):
                    if set_name == "ghost_fleet_5fps" and g.class_id not in GF_VESSEL_CLASSES:
                        continue
                    sb = size_bucket(g.box, w, h)
                    cells[(view, "rgb", light)][f"boxes_{sb}"] += 1
                    ds_c[f"boxes_{sb}"] += 1
            per_ds[f"{set_name}/{seq_dir.name}"] = {"view": view, "modality": "rgb", **ds_c}
    table = [{"viewpoint": v, "modality": m, "light": l, **dict(c)} for (v, m, l), c in sorted(cells.items())]
    a.out.write_text(json.dumps({"cells": table, "per_dataset": per_ds}, indent=1))
    print("| viewpoint | modality | light | images | far boxes | mid boxes | near boxes |\n|---|---|---|---|---|---|---|")
    for r in table:
        print(f"| {r['viewpoint']} | {r['modality']} | {r['light']} | {r.get('images', 0)} | {r.get('boxes_far', 0)} | "
              f"{r.get('boxes_mid', 0)} | {r.get('boxes_near', 0)} |")


if __name__ == "__main__":
    main()
