"""Converters that build MOT-layout dev sequences from public data while workstream A's sets land.

* SMD VIS onboard sample (catalog jsonl with `object_id` attrs, every 30th frame = 1 fps) -> one seq per video.
* MVTD (GOT-10k single-object format; HF `AhsanBB/Maritime_Visual_Tracking_Dataset_MVTD`) -> one seq per video,
  id=1, `absence.label` rows become conf=0 (ignored), optional frame stride to emulate a lower camera rate.
"""
from __future__ import annotations

import json
import shutil
from collections import defaultdict
from pathlib import Path

import cv2

from .mot import append_index, write_sequence

SMD_SPLIT = {"MVI_0790": "dev", "MVI_0799": "dev", "MVI_0797": "test", "MVI_0801": "test"}


def smd_sample_to_mot(images_dir: Path, ann_jsonl: Path, img_index_jsonl: Path, out_root: Path, set_name: str = "smd_vis_onboard_1fps") -> list[str]:
    imgs = {}
    for l in Path(img_index_jsonl).read_text().splitlines():
        if l.strip():
            r = json.loads(l)
            imgs[r["image_id"].split("/")[-1]] = r
    anns = defaultdict(list)
    for l in Path(ann_jsonl).read_text().splitlines():
        if not l.strip():
            continue
        r = json.loads(l)
        if r["label"] == "horizon" or not r["box"]:
            continue
        a = json.loads(r["attrs"])
        anns[a["video"]].append((a["frame_index"], a["object_id"], r["box"], r["label"]))
    by_video = defaultdict(list)
    for stem in sorted(imgs):
        vid = stem.rsplit("_f", 1)[0]
        by_video[vid].append(stem)
    out_root = Path(out_root) / set_name
    seqs = []
    for vid, stems in by_video.items():
        stems = sorted(stems, key=lambda s: int(s.rsplit("_f", 1)[1]))
        frame_of_native = {int(s.rsplit("_f", 1)[1]): i + 1 for i, s in enumerate(stems)}
        root = out_root / vid
        (root / "img1").mkdir(parents=True, exist_ok=True)
        w, h = imgs[stems[0]]["width"], imgs[stems[0]]["height"]
        for i, s in enumerate(stems):
            dst = root / "img1" / f"{i+1:06d}.jpg"
            if not dst.exists():
                shutil.copy(images_dir / f"{s}.jpg", dst)
        labels = {}
        rows = []
        for native, oid, box, label in anns.get(vid, []):
            if native not in frame_of_native:
                continue
            if label not in labels.values():
                labels[str(len(labels) + 1)] = label
            cid = int([k for k, v in labels.items() if v == label][0])
            rows.append((frame_of_native[native], int(oid), box, 1, cid, 1.0))
        labels["label_source"] = "public_gt"
        write_sequence(root, vid, 1.0, w, h, rows, labels, len(stems),
                       {"source_dataset": "smd_vis_onboard_sample", "license": "SMD academic use", "viewpoint": "onboard"})
        n_tracks = len({r[1] for r in rows})
        append_index(out_root, {"seq_id": vid, "set": set_name, "source_dataset": "smd_vis_onboard_sample", "split": SMD_SPLIT.get(vid, "dev"),
                                "fps": 1.0, "n_frames": len(stems), "n_tracks": n_tracks, "viewpoint": "onboard", "label_source": "public_gt"})
        seqs.append(vid)
    return seqs


def mvtd_to_mot(mvtd_root: Path, out_root: Path, seq_names: list[str], stride: int = 6, native_fps: float = 30.0,
                set_name: str = "mvtd_5fps", split_of=None) -> list[str]:
    """stride=6 at 30 fps -> 5 fps sequences (what a 1-5 Hz edge detector would see)."""
    out_root = Path(out_root) / set_name
    seqs = []
    for name in seq_names:
        src = Path(mvtd_root) / name
        gts = [l for l in (src / "groundtruth.txt").read_text().splitlines() if l.strip()]
        absent = []
        if (src / "absence.label").exists():
            absent = [int(float(l)) for l in (src / "absence.label").read_text().split()]
        frames = sorted(p for p in src.glob("*.jpg"))
        n = min(len(frames), len(gts))
        sel = list(range(0, n, stride))
        root = out_root / name
        (root / "img1").mkdir(parents=True, exist_ok=True)
        img0 = cv2.imread(str(frames[0]))
        h, w = img0.shape[:2]
        rows = []
        for j, i in enumerate(sel):
            dst = root / "img1" / f"{j+1:06d}.jpg"
            if not dst.exists():
                shutil.copy(frames[i], dst)
            x, y, bw, bh = [float(v) for v in gts[i].replace("\t", ",").split(",")[:4]]
            conf = 0 if (i < len(absent) and absent[i]) or bw <= 0 or bh <= 0 else 1
            rows.append((j + 1, 1, [x, y, bw, bh], conf, 1, 1.0))
        cat = name.split("-", 1)[1].lower() if "-" in name else "vessel"
        labels = {"1": cat, "label_source": "public_gt"}
        fps = native_fps / stride
        write_sequence(root, name, fps, w, h, rows, labels, len(sel),
                       {"source_dataset": "mvtd", "license": "CC0-1.0", "viewpoint": "mixed", "note": "single-object GT; other vessels unlabeled"})
        split = split_of(name) if split_of else "dev"
        append_index(out_root, {"seq_id": name, "set": set_name, "source_dataset": "mvtd", "split": split, "fps": fps, "n_frames": len(sel),
                                "n_tracks": 1, "viewpoint": "mixed", "label_source": "public_gt", "single_object_gt": True})
        seqs.append(name)
    return seqs
