#!/usr/bin/env python
"""Contact sheets for the synth_unknown set: one crop per synthetic track (context crop + GT box) and a few full frames."""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

import cv2
import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "edgeml" / "src"))
from edgeml_research.data.seqs import load_sequence  # noqa: E402


def tile(img, size):
    h, w = img.shape[:2]
    s = size / max(h, w)
    im = cv2.resize(img, (max(1, int(w * s)), max(1, int(h * s))), interpolation=cv2.INTER_AREA)
    t = np.zeros((size, size, 3), np.uint8)
    t[:im.shape[0], :im.shape[1]] = im
    return t


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default="~/data/edgeml_eval/synth_unknown")
    ap.add_argument("--out", default="~/data/synth_unknown_contact.jpg")
    ap.add_argument("--frames-out", default="~/data/synth_unknown_frames.jpg")
    ap.add_argument("--size", type=int, default=224)
    ap.add_argument("--cols", type=int, default=8)
    a = ap.parse_args()
    root = Path(a.root).expanduser()
    rows = [json.loads(l) for l in (root / "index.jsonl").read_text().splitlines() if l.strip()]
    tiles, frames = [], []
    for r in sorted(rows, key=lambda r: (r["split"], r["synth_class"], r["seq_id"])):
        seq = load_sequence(root / r["seq_id"])
        syn = {int(t) for t in r["synth_tracks"]}
        by_track = {}
        for fr, oid, box, _c, _cls, _v in seq.gt:
            if oid in syn:
                by_track.setdefault(oid, []).append((fr, box))
        for oid, lst in by_track.items():
            fr, box = lst[len(lst) // 2]
            im = cv2.imread(str(seq.frame_path(fr)))
            x, y, w, h = box
            cx, cy, half = x + w / 2, y + h / 2, max(w, h) * 1.6 + 12
            x0, y0 = int(max(0, cx - half)), int(max(0, cy - half))
            x1, y1 = int(min(im.shape[1], cx + half)), int(min(im.shape[0], cy + half))
            crop = im[y0:y1, x0:x1].copy()
            s = a.size / max(crop.shape[:2])
            crop = cv2.resize(crop, (max(1, int(crop.shape[1] * s)), max(1, int(crop.shape[0] * s))), interpolation=cv2.INTER_CUBIC)
            cv2.rectangle(crop, (int((x - x0) * s), int((y - y0) * s)), (int((x + w - x0) * s), int((y + h - y0) * s)), (0, 255, 0), 1)
            t = tile(crop, a.size)
            cv2.putText(t, f"{r['split']}:{r['synth_class'][:14]}", (2, 12), cv2.FONT_HERSHEY_SIMPLEX, 0.38, (0, 255, 255), 1)
            cv2.putText(t, f"{int(w)}x{int(h)}px {r['background'].split('/')[1][:16]}", (2, a.size - 4), cv2.FONT_HERSHEY_SIMPLEX, 0.33, (0, 255, 255), 1)
            tiles.append(t)
        if len(frames) < 12 and len(frames) % 1 == 0:
            fr, box = next(iter(by_track.values()))[len(next(iter(by_track.values()))) // 2]
            im = cv2.imread(str(seq.frame_path(fr)))
            for oid, lst in by_track.items():
                for f2, b in lst:
                    if f2 == fr:
                        x, y, w, h = [int(v) for v in b]
                        cv2.rectangle(im, (x - 3, y - 3), (x + w + 3, y + h + 3), (0, 255, 0), 2)
            t = tile(im, 480)
            cv2.putText(t, f"{r['seq_id'][:40]}", (2, 14), cv2.FONT_HERSHEY_SIMPLEX, 0.45, (0, 255, 255), 1)
            frames.append(t)
    while len(tiles) % a.cols:
        tiles.append(np.zeros((a.size, a.size, 3), np.uint8))
    sheet = np.vstack([np.hstack(tiles[i:i + a.cols]) for i in range(0, len(tiles), a.cols)])
    cv2.imwrite(str(Path(a.out).expanduser()), sheet, [cv2.IMWRITE_JPEG_QUALITY, 85])
    frames = frames[:12]
    while len(frames) % 4:
        frames.append(np.zeros((480, 480, 3), np.uint8))
    cv2.imwrite(str(Path(a.frames_out).expanduser()), np.vstack([np.hstack(frames[i:i + 4]) for i in range(0, len(frames), 4)]), [cv2.IMWRITE_JPEG_QUALITY, 85])
    print(len(tiles), "crops")


if __name__ == "__main__":
    main()
