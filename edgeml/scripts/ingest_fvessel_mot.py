#!/usr/bin/env python3
"""Convert FVessel Clip-10 clips (video + 1 Hz tracking GT) to MOTChallenge layout (CONTRACTS §3).

FVessel GT (gy65896/FVessel README): one row per labelled vessel per *second*:
    <second>,<id>,<bb_left>,<bb_top>,<bb_width>,<bb_height>,<conf>,<x>,<y>,<z>
The videos are 25 fps. We sample one frame per second: MOT frame k (1-based) is the
video frame at t = k s (frame index 25*k), so GT second s maps to MOT frame s.
Only the clips actually present under --raw are converted.

Usage:
  python scripts/ingest_fvessel_mot.py --raw ~/data/fvessel/raw/Clip-10 --out ~/data/eval/fvessel_clip10
"""
import argparse
import json
import math
import re
import subprocess
from pathlib import Path

import cv2

LICENSE = "FVessel (github.com/gy65896/FVessel): academic/research use; redistribution of the video is per the authors' terms"
SOURCE = "fvessel_clip10"


def convert_clip(clip_dir: Path, out_dir: Path, fps_native: int = 25) -> dict:
    name = clip_dir.name  # clip-01
    seq_id = f"fvessel_{name.replace('-', '')}"
    video = next(p for p in clip_dir.glob("*.mp4"))
    gt_in = clip_dir / "gt" / f"{name}_gt_tracking.txt"
    seq_dir = out_dir / seq_id
    img1 = seq_dir / "img1"
    (seq_dir / "gt").mkdir(parents=True, exist_ok=True)
    img1.mkdir(exist_ok=True)

    cap = cv2.VideoCapture(str(video))
    w, h = int(cap.get(cv2.CAP_PROP_FRAME_WIDTH)), int(cap.get(cv2.CAP_PROP_FRAME_HEIGHT))
    n_native = int(cap.get(cv2.CAP_PROP_FRAME_COUNT))
    r = cap.get(cv2.CAP_PROP_FPS)
    cap.release()
    assert abs(r - fps_native) < 0.5, (video, r)
    n_expect = (n_native - 1) // fps_native  # frames k=1..n_expect at index 25*k (container count may be off by 1)

    if not (n_expect - 3 <= len(list(img1.glob("*.jpg"))) <= n_expect):
        for p in img1.glob("*.jpg"):
            p.unlink()
        # keep frames with index n = 25*k, k>=1; output numbered 1..N
        subprocess.run(
            ["ffmpeg", "-loglevel", "error", "-y", "-i", str(video), "-vf",
             f"select='gt(n\\,0)*not(mod(n\\,{fps_native}))'", "-vsync", "vfr",
             "-q:v", "2", "-start_number", "1", str(img1 / "%06d.jpg")],
            check=True,
        )
    n_frames = len(list(img1.glob("*.jpg")))
    # container frame counts are unreliable near the end of some clips (decode errors); tolerate a short tail
    assert n_expect - 3 <= n_frames <= n_expect, (seq_id, n_frames, n_expect)
    if n_frames != n_expect:
        print(f"warning: {seq_id}: extracted {n_frames} 1 Hz frames, container suggested {n_expect}")

    rows, ids = [], set()
    for line in gt_in.read_text().strip().splitlines():
        parts = [p.strip() for p in line.split(",")]
        sec, tid = int(parts[0]), int(parts[1])
        x, y, bw, bh = (float(v) for v in parts[2:6])
        if sec < 1 or sec > n_frames:
            continue
        # FVessel ids are 0-based; MOT ids are 1-based
        rows.append((sec, tid + 1, x, y, bw, bh))
        ids.add(tid + 1)
    rows.sort()
    with open(seq_dir / "gt" / "gt.txt", "w") as f:
        for sec, tid, x, y, bw, bh in rows:
            f.write(f"{sec},{tid},{x:.0f},{y:.0f},{bw:.0f},{bh:.0f},1,1,1.0\n")
    (seq_dir / "gt" / "labels.json").write_text(json.dumps(
        {"classes": {"1": "vessel"}, "label_source": "public_gt",
         "notes": "FVessel tracking GT (1 Hz). ids re-based to 1. visibility not annotated upstream (set to 1.0)."},
        indent=1))
    # camera_para.txt: [lon, lat, ..., fx, fy, u0, v0] (last four inferred to be intrinsics; u0 ~ W/2)
    cam = (clip_dir / "camera_para.txt").read_text().strip().strip("[]")
    fx = float(cam.split(",")[-4])
    hfov = 2 * math.degrees(math.atan(w / (2 * fx)))
    (seq_dir / "seqinfo.ini").write_text(
        "[Sequence]\n" f"name={seq_id}\nimDir=img1\nframeRate=1\nseqLength={n_frames}\nimWidth={w}\nimHeight={h}\n"
        f"imExt=.jpg\nsource={SOURCE}\nlicense={LICENSE}\nupstream_video={video.name}\n"
        f"camera_para={cam}\nhfov_deg={hfov:.1f}\nplatform_motion=static\n")
    return {"seq_id": seq_id, "set": out_dir.name, "source": SOURCE, "upstream": f"Clip-10/{name}",
            "fps": 1, "frames": n_frames, "tracks": len(ids), "boxes": len(rows), "width": w, "height": h,
            "viewpoint": "shore_fixed", "platform_motion": "static", "sea_state": "calm_river",
            "hfov_deg": round(hfov, 1), "label_source": "public_gt", "license": "FVessel (gy65896/FVessel) research use"}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--raw", required=True, type=Path)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--clips", nargs="*", default=None)
    a = ap.parse_args()
    a.out.mkdir(parents=True, exist_ok=True)
    clips = sorted(p for p in a.raw.iterdir() if re.match(r"clip-\d+", p.name) and (p / "gt").exists())
    if a.clips:
        clips = [c for c in clips if c.name in a.clips]
    entries = [convert_clip(c, a.out) for c in clips]
    with open(a.out / "index.jsonl", "w") as f:
        for e in entries:
            f.write(json.dumps(e) + "\n")
            print(e)


if __name__ == "__main__":
    main()
