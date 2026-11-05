#!/usr/bin/env python3
"""Coverage retrain: multi-class (80 COCO classes) YOLO dataset that ADDS maritime data as extra `boat` examples.

Keeps COCO knowledge instead of G's single_cls recipe (G_detector.md §9):
  * COCO replay: val2017 images with all 80-class labels (we never evaluate on COCO).
  * Maritime frames: vessel GT -> COCO `boat` (8), SeaDronesSee swimmers -> `person` (0); every other COCO class on
    those frames is pseudo-labelled by a larger COCO model (--teacher, conf >= --teacher-conf) so the maritime frames
    do not teach the head that people/cars/birds on deck are background.
  * Far/small oversampling: training frames with a vessel whose long side at --imgsz letterbox is < --far-px are
    repeated --far-repeat times and native-resolution --tile crops around those vessels are added.
Data discipline (clean v3 catalog, splits by recording):
  train  SeaShips upstream train, FVessel dev clips 01-06, GF 5 fps dev clips (minus holdout), SeaDronesSee eval_split=train
  val    (YOLO best.pt pick; dev data, not trained on) SeaShips upstream valid, holdout clips, SeaDronesSee dev (stride 4)
  never  every locked test source (splits.yaml test, clean v3 eval_split=test), SDS `guard`, SMD (quarantined in v2),
         ir_maritime_vessels (license unknown -> internal dev only; evaluated as a domain probe, never trained on)
The far/small dev slice (FARSMALL_SLICE) is all vessel GT < 32^2 px at 640 in the never-trained dev sets; it is written
to <out>/farsmall_dev.json for cov_eval_farsmall.py.

  python edgeml/tools/cov_build_detector_dataset.py --catalog ~/data/clean_v3/catalog --datasets ~/data/datasets \
      --eval-root ~/data/edgeml_eval --coco ~/data/coco --out ~/data/yolo_cov
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import sys
from collections import Counter, defaultdict
from pathlib import Path

import cv2
import yaml
from concurrent.futures import ThreadPoolExecutor

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
sys.path.insert(0, str(Path(__file__).resolve().parent))
from edgeml.data.mot import MOTSequence, split_seq_ids  # noqa: E402
from g_build_dataset import DEFAULT_HOLDOUT, FV_VESSEL_CLASSES, GF_VESSEL_CLASSES  # noqa: E402

BOAT, PERSON = 8, 0
SDS_CLASS = {"boat": BOAT, "jetski": BOAT, "swimmer": PERSON}
BUCKET = "gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/"


def long_side_at(box, w, h, imgsz):
    return max(box[2], box[3]) * imgsz / max(w, h)


class Writer:
    def __init__(self, out: Path, imgsz: int, far_px: float, far_repeat: int, tile: int, max_side: int):
        self.out, self.imgsz, self.far_px, self.far_repeat, self.tile, self.max_side = out, imgsz, far_px, far_repeat, tile, max_side
        self.items: list[tuple] = []  # (split, stem, img_path, [(cls, box)], w, h, source)
        self.stats = Counter()

    def add(self, split, stem, img, labelled, w, h, source):
        self.items.append((split, stem, Path(img), labelled, w, h, source))

    @staticmethod
    def _yolo(lines, labelled, w, h):
        for c, (x, y, bw, bh) in labelled:
            x1, y1, x2, y2 = max(0.0, x), max(0.0, y), min(float(w), x + bw), min(float(h), y + bh)
            if x2 - x1 >= 1 and y2 - y1 >= 1:
                lines.append(f"{c} {(x1 + x2) / 2 / w:.6f} {(y1 + y2) / 2 / h:.6f} {(x2 - x1) / w:.6f} {(y2 - y1) / h:.6f}")
        return lines

    def _emit(self, split, stem, src, labelled, w, h, image=None):
        (self.out / "images" / split).mkdir(parents=True, exist_ok=True)
        (self.out / "labels" / split).mkdir(parents=True, exist_ok=True)
        dst = self.out / "images" / split / f"{stem}.jpg"
        if image is not None:
            cv2.imwrite(str(dst), image, [cv2.IMWRITE_JPEG_QUALITY, 92])
        elif not dst.exists():
            os.symlink(src.resolve(), dst)
        (self.out / "labels" / split / f"{stem}.txt").write_text("\n".join(self._yolo([], labelled, w, h)) + "\n")

    def flush(self, teacher):
        for split, stem, img, labelled, w, h, source in self.items:
            labelled = labelled + teacher.get(str(img), [])
            img_arr = None
            if max(w, h) > self.max_side:  # 4K aerial frames: store a resized copy, tiles still come from native res
                img_arr = cv2.imread(str(img))
                s = self.max_side / max(w, h)
                small = cv2.resize(img_arr, (round(w * s), round(h * s)), interpolation=cv2.INTER_AREA)
                self._emit(split, stem, img, [(c, [v * s for v in b]) for c, b in labelled], small.shape[1], small.shape[0], small)
            else:
                self._emit(split, stem, img, labelled, w, h)
            self.stats[f"{source}_{split}_imgs"] += 1
            self.stats[f"{source}_{split}_boat"] += sum(c == BOAT for c, _ in labelled)
            if split != "train":
                continue
            far = [b for c, b in labelled if c == BOAT and long_side_at(b, w, h, self.imgsz) < self.far_px]
            if not far:
                continue
            self.stats[f"{source}_far_frames"] += 1
            for k in range(1, self.far_repeat):
                dst = self.out / "images" / split / f"{stem}_rep{k}.jpg"
                if not dst.exists():
                    os.symlink((self.out / "images" / split / f"{stem}.jpg").resolve(), dst)
                shutil.copy(self.out / "labels" / split / f"{stem}.txt", self.out / "labels" / split / f"{stem}_rep{k}.txt")
                self.stats["far_repeat_imgs"] += 1
            if max(w, h) < 1.5 * self.tile:
                continue
            img_arr = cv2.imread(str(img)) if img_arr is None else img_arr
            done = []
            for b in far:
                cx, cy = b[0] + b[2] / 2, b[1] + b[3] / 2
                if any(abs(cx - dx) < self.tile / 3 and abs(cy - dy) < self.tile / 3 for dx, dy in done):
                    continue
                done.append((cx, cy))
                x0 = int(min(max(0, cx - self.tile / 2), w - self.tile))
                y0 = int(min(max(0, cy - self.tile / 2), h - self.tile))
                crop = img_arr[y0:y0 + self.tile, x0:x0 + self.tile]
                kept = []
                for c, (x, y, bw, bh) in labelled:
                    ix = max(0, min(x + bw, x0 + self.tile) - max(x, x0))
                    iy = max(0, min(y + bh, y0 + self.tile) - max(y, y0))
                    if bw * bh > 0 and ix * iy / (bw * bh) >= 0.6:
                        kept.append((c, [x - x0, y - y0, bw, bh]))
                self._emit(split, f"{stem}_tile{len(done)}", img, kept, self.tile, self.tile, crop)
                self.stats["far_tiles"] += 1


def catalog_rows(catalog: Path, ds: str):
    anns = defaultdict(list)
    for line in (catalog / "annotations" / f"{ds}.jsonl").open():
        a = json.loads(line)
        anns[a["image_id"]].append(a)
    for line in (catalog / "images" / f"{ds}.jsonl").open():
        r = json.loads(line)
        yield r, anns.get(r["image_id"], [])


def local(datasets: Path, r):
    return datasets / r["gcs_uri"].split("/datasets/", 1)[1]


def add_seaships(wr, catalog, datasets, farsmall):
    for r, anns in catalog_rows(catalog, "seaships7000"):
        if r["eval_split"] != "dev":  # v2: dev = upstream train+valid; test is LOCKED
            continue
        split = {"train": "train", "valid": "val"}[r["split"]]
        boxes = [(BOAT, a["box"]) for a in anns]
        wr.add(split, "ss_" + r["image_id"].replace("/", "_"), local(datasets, r), boxes, r["width"], r["height"], "seaships")
        if split == "val":
            farsmall.append(("seaships7000", r["image_id"], [b for _, b in boxes], r["width"], r["height"]))


def add_sds(wr, catalog, datasets, farsmall, val_stride):
    n_dev = 0
    for r, anns in catalog_rows(catalog, "seadronessee"):
        boxes = [(SDS_CLASS[a["label"]], a["box"]) for a in anns if a["label"] in SDS_CLASS]
        stem = "sds_" + r["image_id"].replace("/", "_")
        if r["eval_split"] == "train":
            wr.add("train", stem, local(datasets, r), boxes, r["width"], r["height"], "sds")
        elif r["eval_split"] == "dev":
            n_dev += 1
            if n_dev % val_stride == 0:
                wr.add("val", stem, local(datasets, r), boxes, r["width"], r["height"], "sds")
                farsmall.append(("seadronessee", r["image_id"], [b for c, b in boxes if c == BOAT], r["width"], r["height"]))


def add_mot(wr, eval_root, set_name, vessel_classes, holdout, splits_yaml, farsmall, every):
    test = set(split_seq_ids("test", set_name, splits_yaml))
    for seq_id in split_seq_ids("dev", set_name, splits_yaml):
        assert seq_id not in test, f"{seq_id} is LOCKED test"
        seq = MOTSequence(eval_root / set_name / seq_id)
        status = seq.labels.get("qa", {}).get("gt_status", "complete")
        if status == "incomplete":
            continue
        split = "val" if seq_id in holdout else "train"
        by_frame = seq.gt_by_frame()
        h, w = cv2.imread(str(seq.frame_path(1))).shape[:2]
        for f in seq.frame_ids():
            if (f - 1) % every:
                continue
            boxes = [list(g.box) for g in by_frame.get(f, []) if g.class_id in vessel_classes]
            if status == "partial" and not boxes:
                continue
            p = seq.frame_path(f)
            if p.exists():
                wr.add(split, f"{set_name}_{seq_id}_{f:06d}", p, [(BOAT, b) for b in boxes], w, h, set_name)
                if split == "val":
                    farsmall.append((set_name, f"{seq_id}/{f}", boxes, w, h))


def add_coco(wr, coco: Path, n: int):
    imgs = sorted((coco / "val2017").glob("*.jpg"))[:n]
    for p in imgs:
        lab = coco / "coco" / "labels" / "val2017" / f"{p.stem}.txt"
        h, w = cv2.imread(str(p)).shape[:2]
        boxes = []
        if lab.exists():
            for line in lab.read_text().split("\n"):
                v = line.split()
                if len(v) == 5:
                    c, cx, cy, bw, bh = int(v[0]), *map(float, v[1:])
                    boxes.append((c, [(cx - bw / 2) * w, (cy - bh / 2) * h, bw * w, bh * h]))
        wr.add("train", f"coco_{p.stem}", p, boxes, w, h, "coco_replay")


def teacher_labels(wr, weights, conf, device):
    """Non-boat COCO classes on maritime frames (and non-person on SDS, whose swimmers are GT)."""
    from ultralytics import YOLO
    model = YOLO(weights)
    paths = [str(it[2]) for it in wr.items if it[6] != "coco_replay"]
    no_person = {str(it[2]) for it in wr.items if it[6] == "sds"}
    out = {}

    def load(p, side=1280):
        im = cv2.imread(p)
        k = min(1.0, side / max(im.shape[:2]))
        return (cv2.resize(im, None, fx=k, fy=k, interpolation=cv2.INTER_AREA) if k < 1 else im), k

    with ThreadPoolExecutor(12) as pool:  # JPEG decode of 4K UAV frames, not the GPU, is the bottleneck
        for i in range(0, len(paths), 16):
            chunk = paths[i:i + 16]
            loaded = list(pool.map(load, chunk))
            for bp, (_, bk), res in zip(chunk, loaded, model.predict([im for im, _ in loaded], imgsz=1280, conf=conf,
                                                                       device=device, verbose=False, half=True)):
                keep = []
                for (x1, y1, x2, y2), c in zip(res.boxes.xyxy.tolist(), res.boxes.cls.tolist()):
                    c = int(c)
                    if c == BOAT or (c == PERSON and bp in no_person):
                        continue
                    keep.append((c, [x1 / bk, y1 / bk, (x2 - x1) / bk, (y2 - y1) / bk]))
                out[bp] = keep
            if i % 1600 == 0:
                print(f"teacher {i}/{len(paths)}", flush=True)
    wr.stats["teacher_boxes"] = sum(map(len, out.values()))
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--catalog", type=Path, required=True, help="clean v3 catalog dir")
    ap.add_argument("--datasets", type=Path, default=Path("~/data/datasets").expanduser())
    ap.add_argument("--eval-root", type=Path, default=Path("~/data/edgeml_eval").expanduser())
    ap.add_argument("--coco", type=Path, default=Path("~/data/coco").expanduser())
    ap.add_argument("--coco-n", type=int, default=5000)
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--imgsz", type=int, default=640)
    ap.add_argument("--far-px", type=float, default=24.0)
    ap.add_argument("--far-repeat", type=int, default=3)
    ap.add_argument("--tile", type=int, default=640)
    ap.add_argument("--max-side", type=int, default=1920)
    ap.add_argument("--sds-val-stride", type=int, default=4)
    ap.add_argument("--teacher", default="yolo11x.pt")
    ap.add_argument("--teacher-conf", type=float, default=0.5)
    ap.add_argument("--device", default="0")
    ap.add_argument("--holdout", nargs="*", default=DEFAULT_HOLDOUT)
    ap.add_argument("--splits", type=Path, default=Path(__file__).resolve().parents[1] / "configs" / "splits.yaml")
    a = ap.parse_args()
    if a.out.exists():
        ap.error(f"{a.out} exists; build into a fresh directory")
    wr = Writer(a.out, a.imgsz, a.far_px, a.far_repeat, a.tile, a.max_side)
    farsmall = []
    add_seaships(wr, a.catalog, a.datasets, farsmall)
    add_sds(wr, a.catalog, a.datasets, farsmall, a.sds_val_stride)
    add_mot(wr, a.eval_root, "fvessel_clip10", FV_VESSEL_CLASSES, set(a.holdout), a.splits, farsmall, 1)
    add_mot(wr, a.eval_root, "ghost_fleet_5fps", GF_VESSEL_CLASSES, set(a.holdout), a.splits, farsmall, 1)
    add_coco(wr, a.coco, a.coco_n)
    teacher = teacher_labels(wr, a.teacher, a.teacher_conf, a.device) if a.teacher != "none" else {}
    wr.flush(teacher)
    from ultralytics.utils import YAML  # noqa: F401  (ensures ultralytics is importable for names)
    from ultralytics import YOLO
    names = YOLO("yolo11n.pt").names
    (a.out / "data.yaml").write_text(yaml.safe_dump({"path": str(a.out), "train": "images/train", "val": "images/val",
                                                     "names": names}, sort_keys=False))
    slice_ = [{"set": s, "image": i, "box": b, "w": w, "h": h} for s, i, boxes, w, h in farsmall for b in boxes
              if b[2] * b[3] * (a.imgsz / max(w, h)) ** 2 < 32 ** 2]
    (a.out / "farsmall_dev.json").write_text(json.dumps(slice_))
    manifest = {"args": {k: str(v) for k, v in vars(a).items()}, "stats": dict(sorted(wr.stats.items())),
                "farsmall_dev_objects": len(slice_), "farsmall_by_set": dict(Counter(x["set"] for x in slice_)),
                "train_imgs": len(list((a.out / "images/train").iterdir())),
                "val_imgs": len(list((a.out / "images/val").iterdir()))}
    (a.out / "manifest.json").write_text(json.dumps(manifest, indent=1))
    print(json.dumps(manifest, indent=1))


if __name__ == "__main__":
    main()
