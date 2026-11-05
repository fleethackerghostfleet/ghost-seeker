#!/usr/bin/env python3
"""Semi-automatic identity labelling for MOT-layout sequences (teacher stage).

Open-vocabulary detector (Grounding DINO, HF transformers) on every frame + BoT-SORT with ReID
(boxmot) for identity. Produces, per sequence:
    <seq>/teacher/dets.jsonl        raw detections per frame (for audit / re-tracking)
    <seq>/teacher/tracks.jsonl      tracker output rows
    <seq>/gt/gt.txt, gt/labels.json MOT GT with label_source="teacher" (human QA happens later, see qa_apply.py)
    <seq>/qa/track_<id>.jpg         per-track contact sheets for the human QA pass
    <seq>/qa/overview.jpg           trajectory overview
Runs on the L4 VM (edgeml-a-l4). CPU is fine for small clips.

  python autolabel_tracks.py --root ~/eval/ghost_fleet_5fps --seqs Spotted_A_Submarine ...
"""
import argparse
import itertools
import json
import shutil
from collections import Counter, defaultdict
from pathlib import Path

import cv2
import numpy as np
import torch

PROMPT_CLASSES = ["ship", "boat", "sailboat", "submarine", "buoy", "dolphin", "whale", "shark fin", "bird",
                  "helicopter", "airplane", "person", "jet ski", "kayak"]
CLASS_IDS = {c: i + 1 for i, c in enumerate(PROMPT_CLASSES)}


def load_detector(name="IDEA-Research/grounding-dino-base", device="cuda"):
    from transformers import AutoModelForZeroShotObjectDetection, AutoProcessor

    proc = AutoProcessor.from_pretrained(name)
    model = AutoModelForZeroShotObjectDetection.from_pretrained(name).to(device).eval()
    return proc, model


@torch.no_grad()
def detect(proc, model, img_bgr, device, box_thr=0.2, text_thr=0.15):
    rgb = cv2.cvtColor(img_bgr, cv2.COLOR_BGR2RGB)
    text = ". ".join(PROMPT_CLASSES) + "."
    inputs = proc(images=rgb, text=text, return_tensors="pt").to(device)
    out = model(**inputs)
    h, w = img_bgr.shape[:2]
    try:
        res = proc.post_process_grounded_object_detection(out, inputs.input_ids, threshold=box_thr,
                                                          text_threshold=text_thr, target_sizes=[(h, w)])[0]
    except TypeError:
        res = proc.post_process_grounded_object_detection(out, inputs.input_ids, box_threshold=box_thr,
                                                          text_threshold=text_thr, target_sizes=[(h, w)])[0]
    labels = res.get("text_labels", res.get("labels"))
    dets = []
    for b, s, lab in zip(res["boxes"].cpu().numpy(), res["scores"].cpu().numpy(), labels):
        lab = str(lab).strip()
        cls = next((CLASS_IDS[c] for c in sorted(PROMPT_CLASSES, key=len, reverse=True) if c in lab), None)
        if cls is None:
            continue
        x1, y1, x2, y2 = (float(v) for v in b)
        if (x2 - x1) * (y2 - y1) > 0.5 * w * h:  # whole-frame hallucinations
            continue
        dets.append([x1, y1, x2, y2, float(s), cls, lab])
    return nms_class_agnostic(dets)


def nms_class_agnostic(dets, iou_thr=0.6):
    """One prompt per object: 'ship' and 'boat' both fire on the same hull, which otherwise becomes two tracks."""
    keep = []
    for d in sorted(dets, key=lambda d: -d[4]):
        ax1, ay1, ax2, ay2 = d[:4]
        area_a = (ax2 - ax1) * (ay2 - ay1)
        ok = True
        for k in keep:
            bx1, by1, bx2, by2 = k[:4]
            iw, ih = max(0.0, min(ax2, bx2) - max(ax1, bx1)), max(0.0, min(ay2, by2) - max(ay1, by1))
            inter = iw * ih
            union = area_a + (bx2 - bx1) * (by2 - by1) - inter
            if union > 0 and inter / union > iou_thr:
                ok = False
                break
        if ok:
            keep.append(d)
    return keep


def make_tracker(fps):
    from boxmot import BotSort, BotSortConfig

    # low thresholds: far/tiny vessels score 0.2-0.35 with Grounding DINO; QA removes the FPs
    cfg = BotSortConfig(track_high_thresh=0.22, track_low_thresh=0.08, new_track_thresh=0.25,
                        max_lost_frames=int(6 * fps), match_thresh=0.8, det_thresh=0.1, use_cmc=True)
    return BotSort(cfg)


def tracks_to_rows(t):
    if isinstance(t, np.ndarray):
        return t
    if hasattr(t, "to_aabb_rows"):
        r = t.to_aabb_rows()
        return r.cpu().numpy() if hasattr(r, "cpu") else np.asarray(r)
    return np.zeros((0, 8))


def postprocess(track_rows, fps, min_hits=5, max_gap_s=2.0):
    """rows: (frame, id, x1,y1,x2,y2, conf, cls). Drop short tracks, interpolate gaps."""
    by_id = defaultdict(dict)
    cls_votes = defaultdict(Counter)
    for fr, tid, x1, y1, x2, y2, conf, cls in track_rows:
        by_id[int(tid)][int(fr)] = (x1, y1, x2, y2, conf)
        cls_votes[int(tid)][int(cls)] += 1
    out = []
    for tid, frames in by_id.items():
        if len(frames) < min_hits:
            continue
        cls = cls_votes[tid].most_common(1)[0][0]
        fs = sorted(frames)
        for a, b in itertools.pairwise(fs):
            if 1 < b - a <= max_gap_s * fps:
                pa, pb = np.array(frames[a][:4]), np.array(frames[b][:4])
                for f in range(a + 1, b):
                    t = (f - a) / (b - a)
                    frames[f] = (*(pa * (1 - t) + pb * t), -1.0)  # conf<0 marks interpolated (internal)
        for f, (x1, y1, x2, y2, conf) in sorted(frames.items()):
            # interpolated boxes are still evaluated (conf=1); visibility=0.5 flags them for audit
            out.append((f, tid + 1, x1, y1, x2 - x1, y2 - y1, 1, cls, 1.0 if conf >= 0 else 0.5))  # MOT ids are 1-based
    out.sort()
    return out


def contact_sheets(seq_dir, rows, img_shape, n_tiles=16, tile=160):
    qa = seq_dir / "qa"
    qa.mkdir(exist_ok=True)
    by_id = defaultdict(list)
    for r in rows:
        by_id[r[1]].append(r)
    h, w = img_shape[:2]
    overview = cv2.imread(str(seq_dir / "img1" / "000001.jpg"))
    rng = np.random.default_rng(0)
    for tid, rs in sorted(by_id.items()):
        color = tuple(int(c) for c in rng.integers(60, 255, 3))
        idx = np.linspace(0, len(rs) - 1, min(n_tiles, len(rs))).astype(int)
        tiles = []
        for i in idx:
            f, _, x, y, bw, bh, _conf, cls, _ = rs[i]
            img = cv2.imread(str(seq_dir / "img1" / f"{f:06d}.jpg"))
            cx, cy = x + bw / 2, y + bh / 2
            s = max(bw, bh) * 2.5 + 40
            x0, y0 = int(max(0, cx - s / 2)), int(max(0, cy - s / 2))
            x1, y1 = int(min(w, cx + s / 2)), int(min(h, cy + s / 2))
            crop = img[y0:y1, x0:x1].copy()
            cv2.rectangle(crop, (int(x - x0), int(y - y0)), (int(x + bw - x0), int(y + bh - y0)), (0, 255, 0) if rs[i][8] >= 1 else (0, 165, 255), 1)
            crop = cv2.resize(crop, (tile, tile))
            cv2.putText(crop, f"f{f}", (3, 14), cv2.FONT_HERSHEY_SIMPLEX, 0.45, (255, 255, 0), 1)
            tiles.append(crop)
        while len(tiles) % 8:
            tiles.append(np.zeros((tile, tile, 3), np.uint8))
        grid = np.vstack([np.hstack(tiles[i:i + 8]) for i in range(0, len(tiles), 8)])
        hdr = np.zeros((28, grid.shape[1], 3), np.uint8)
        cls = Counter(r[7] for r in rs).most_common(1)[0][0]
        name = PROMPT_CLASSES[cls - 1] if 0 < cls <= len(PROMPT_CLASSES) else str(cls)
        cv2.putText(hdr, f"{seq_dir.name} track {tid} cls={name} frames {rs[0][0]}-{rs[-1][0]} n={len(rs)} med_box={int(np.median([r[4] for r in rs]))}x{int(np.median([r[5] for r in rs]))}",
                    (5, 19), cv2.FONT_HERSHEY_SIMPLEX, 0.55, (255, 255, 255), 1)
        cv2.imwrite(str(qa / f"track_{tid:03d}.jpg"), np.vstack([hdr, grid]), [cv2.IMWRITE_JPEG_QUALITY, 80])
        pts = [(int(r[2] + r[4] / 2), int(r[3] + r[5] / 2)) for r in rs]
        for a, b in itertools.pairwise(pts):
            cv2.line(overview, a, b, color, 2)
        cv2.putText(overview, str(tid), pts[0], cv2.FONT_HERSHEY_SIMPLEX, 0.7, color, 2)
    cv2.imwrite(str(qa / "overview.jpg"), overview, [cv2.IMWRITE_JPEG_QUALITY, 80])


def label_sequence(seq_dir: Path, proc, model, device, args):
    import configparser

    cp = configparser.ConfigParser()
    cp.read(seq_dir / "seqinfo.ini")
    fps = float(cp["Sequence"]["frameRate"])
    n = int(cp["Sequence"]["seqLength"])
    tdir = seq_dir / "teacher"
    tdir.mkdir(exist_ok=True)
    tracker = make_tracker(fps)
    track_rows = []
    shape = None
    det_f = (tdir / "dets.jsonl").open("w")
    for f in range(1, n + 1):
        img = cv2.imread(str(seq_dir / "img1" / f"{f:06d}.jpg"))
        shape = img.shape
        dets = detect(proc, model, img, device, args.box_thr, args.text_thr)
        det_f.write(json.dumps({"frame": f, "dets": dets}) + "\n")
        arr = np.array([d[:6] for d in dets], dtype=np.float32).reshape(-1, 6)
        rows = tracks_to_rows(tracker.update(arr, img))
        for r in rows:
            x1, y1, x2, y2, tid, conf, cls = (float(v) for v in r[:7])
            track_rows.append((f, int(tid), x1, y1, x2, y2, conf, int(cls)))
        if f % 100 == 0:
            print(f"  {seq_dir.name} {f}/{n} dets={len(dets)} tracks={len(rows)}", flush=True)
    det_f.close()
    with open(tdir / "tracks.jsonl", "w") as fh:
        for r in track_rows:
            fh.write(json.dumps(r) + "\n")
    gt_rows = postprocess(track_rows, fps, args.min_hits, args.max_gap_s)
    (seq_dir / "gt").mkdir(exist_ok=True)
    with open(seq_dir / "gt" / "gt.txt", "w") as fh:
        for f, tid, x, y, w, h, conf, cls, vis in gt_rows:
            fh.write(f"{f},{tid},{x:.1f},{y:.1f},{w:.1f},{h:.1f},{conf},{cls},{vis}\n")
    (seq_dir / "teacher").mkdir(exist_ok=True)
    shutil.copy(seq_dir / "gt" / "gt.txt", seq_dir / "teacher" / "gt_teacher.txt")
    (seq_dir / "gt" / "labels.json").write_text(json.dumps({
        "classes": {str(v): k for k, v in CLASS_IDS.items()},
        "label_source": "teacher",
        "method": {"detector": args.detector, "box_thr": args.box_thr, "text_thr": args.text_thr,
                   "tracker": "boxmot BoT-SORT + ReID (default encoder)", "min_hits": args.min_hits,
                   "max_gap_s": args.max_gap_s, "visibility_field": "1.0=detected, 0.5=interpolated (conf=1 for both)"},
    }, indent=1))
    if gt_rows:
        contact_sheets(seq_dir, gt_rows, shape)
    ids = {r[1] for r in gt_rows}
    print(f"{seq_dir.name}: {n} frames, {len(ids)} tracks, {len(gt_rows)} boxes", flush=True)
    return {"seq_id": seq_dir.name, "frames": n, "tracks": len(ids), "boxes": len(gt_rows), "fps": fps}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", required=True, type=Path)
    ap.add_argument("--seqs", nargs="*")
    ap.add_argument("--detector", default="IDEA-Research/grounding-dino-base")
    ap.add_argument("--box-thr", type=float, default=0.2)
    ap.add_argument("--text-thr", type=float, default=0.15)
    ap.add_argument("--min-hits", type=int, default=5)
    ap.add_argument("--max-gap-s", type=float, default=2.0)
    args = ap.parse_args()
    device = "cuda" if torch.cuda.is_available() else "cpu"
    proc, model = load_detector(args.detector, device)
    seqs = args.seqs or sorted(p.name for p in args.root.iterdir() if (p / "seqinfo.ini").exists())
    summary = [label_sequence(args.root / s, proc, model, device, args) for s in seqs]
    (args.root / "teacher_summary.json").write_text(json.dumps(summary, indent=1))


if __name__ == "__main__":
    main()
