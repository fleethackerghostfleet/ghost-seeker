"""Synthesize DEV sequences with dense clusters of small boats (I_tracking §2).

No dev clip has more than 3 concurrent objects and none has a GT track shorter than 3 frames, so the two C.md §6b
failure modes (single-detection losses, dense-target collapse) cannot be reproduced on real dev footage. This tool
composites small boat sprites — crops of GT boxes from *dev* sequences only — onto downscaled fvessel *dev* frames
(whose own GT ships are kept as large-object tracks) and writes MOT-layout sequences with `split=dev`.

  python research/edgeml/tools/synth_dense.py --data ~/data/edgeml_eval --out ~/data/edgeml_eval/synth_dense_5fps

Per sequence: N sprites in K clusters, spacing 0.5–2 box diagonals, slow drift, plus cameo objects that are visible
for 1, 2 or 3 frames and "blinkers" visible on alternate frames for a short burst. Deterministic (seeded).
"""
from __future__ import annotations

import argparse
import json
import math
import random
from pathlib import Path

import cv2
import numpy as np

from edgeml_research.data.seqs import append_index, list_sequences, write_sequence

W, H = 1920, 1080
FPS = 5.0

# dev-only sprite sources (set, seq) — never a test sequence
SPRITE_SRC = {
    "fvessel_clip10": ["fvessel_clip01", "fvessel_clip02", "fvessel_clip03", "fvessel_clip08", "fvessel_clip10"],
    "ghost_fleet_5fps": ["Cruise_ship_Mediterranean_sunset_off_Italy", "A_ship_in_the_distance", "Submarine_surfacing"],
}
BG_SRC = ["fvessel_clip04", "fvessel_clip05", "fvessel_clip06"]

SCENARIOS = [  # name, n_cluster_objects, n_clusters, cameo count, blinker count, seed
    ("synth_dense_12", 12, 2, 4, 2, 1),
    ("synth_dense_20", 20, 3, 6, 3, 2),
    ("synth_dense_30", 30, 4, 8, 4, 3),
]
DURATION_S = 120.0


def harvest_sprites(data: Path, rng: random.Random, per_seq: int = 12) -> list[np.ndarray]:
    out = []
    for set_name, seqs in SPRITE_SRC.items():
        for seq in list_sequences(data / set_name, split="dev"):
            if seq.seq_id not in seqs:
                continue
            rows = [(fr, box) for oid, lst in seq.gt_tracks().items() for fr, box in lst]
            rng.shuffle(rows)
            taken = 0
            for fr, box in rows:
                if taken >= per_seq:
                    break
                x, y, w, h = [int(round(v)) for v in box]
                if w < 24 or h < 10:
                    continue
                img = cv2.imread(str(seq.root / "img1" / f"{fr:06d}.jpg"))
                if img is None:
                    continue
                crop = img[max(0, y):y + h, max(0, x):x + w]
                if crop.size == 0 or crop.shape[0] < 8:
                    continue
                out.append(crop)
                taken += 1
    return out


def feather_mask(h: int, w: int) -> np.ndarray:
    yy, xx = np.mgrid[0:h, 0:w]
    cx, cy = (w - 1) / 2, (h - 1) / 2
    r = np.sqrt(((xx - cx) / (0.55 * w)) ** 2 + ((yy - cy) / (0.6 * h)) ** 2)
    return np.clip(1.25 - r, 0.0, 1.0)[..., None].astype(np.float32)


def paste(frame: np.ndarray, sprite: np.ndarray, box: list[float]) -> None:
    x, y, w, h = [int(round(v)) for v in box]
    if w < 2 or h < 2 or x < 0 or y < 0 or x + w > frame.shape[1] or y + h > frame.shape[0]:
        return
    sp = cv2.resize(sprite, (w, h), interpolation=cv2.INTER_AREA).astype(np.float32)
    m = feather_mask(h, w)
    roi = frame[y:y + h, x:x + w].astype(np.float32)
    frame[y:y + h, x:x + w] = (m * sp + (1 - m) * roi).astype(np.uint8)


class Obj:
    def __init__(self, oid, sprite, x, y, side, aspect, vx, vy, t0, t1, pattern="solid"):
        self.oid, self.sprite, self.x, self.y, self.side, self.aspect = oid, sprite, x, y, side, aspect
        self.vx, self.vy, self.t0, self.t1, self.pattern = vx, vy, t0, t1, pattern

    def box(self, t: float) -> list[float]:
        w = self.side * math.sqrt(self.aspect)
        h = self.side / math.sqrt(self.aspect)
        return [self.x + self.vx * t - w / 2, self.y + self.vy * t - h / 2, w, h]

    def visible(self, fi: int, t: float) -> bool:
        if not (self.t0 <= t < self.t1):
            return False
        return fi % 2 == 0 if self.pattern == "blink" else True


def build(name: str, n_obj: int, n_clusters: int, n_cameo: int, n_blink: int, seed: int,
          data: Path, out_root: Path, sprites: list[np.ndarray]) -> dict:
    rng = random.Random(seed)
    bg_seq = next(s for s in list_sequences(data / "fvessel_clip10", split="dev") if s.seq_id == BG_SRC[seed % len(BG_SRC)])
    bg_frames = [(fi, p) for fi, _t, p in bg_seq.frames()]
    bg_gt = bg_seq.gt_tracks()
    sx, sy = W / bg_seq.width, H / bg_seq.height
    n_frames = int(DURATION_S * FPS)
    water_y0, water_y1 = int(0.70 * H), int(0.97 * H)

    objs: list[Obj] = []
    oid = 100   # background ships keep ids 1..n (they are re-based below)
    centers = [(rng.uniform(0.1 * W, 0.9 * W), rng.uniform(water_y0 + 40, water_y1 - 40)) for _ in range(n_clusters)]
    for k in range(n_obj):
        cx, cy = centers[k % n_clusters]
        side = rng.uniform(28, 90) * (0.6 + 0.8 * (cy - water_y0) / (water_y1 - water_y0))  # nearer = bigger
        # spacing 0.5–2 diag from the cluster centre, spread around it
        d = side * rng.uniform(0.5, 2.0) * (1 + 0.15 * (k // n_clusters))
        ang = rng.uniform(0, 2 * math.pi)
        x, y = min(max(60, cx + d * math.cos(ang)), W - 60), min(max(water_y0 + 20, cy + d * math.sin(ang) * 0.4), water_y1 - 20)
        vx, vy = rng.uniform(-6, 6), rng.uniform(-1.0, 1.0)
        t0 = 0.0 if rng.random() < 0.6 else rng.uniform(0, DURATION_S * 0.5)
        t1 = DURATION_S if rng.random() < 0.6 else min(DURATION_S, t0 + rng.uniform(15, 80))
        oid += 1
        objs.append(Obj(oid, rng.choice(sprites), x, y, side, rng.uniform(1.6, 3.2), vx, vy, t0, t1))
    for k in range(n_cameo):
        n_vis = [1, 1, 2, 3][k % 4]
        t0 = rng.uniform(5, DURATION_S - 5)
        t0 = round(t0 * FPS) / FPS
        side = rng.uniform(26, 60)
        x, y = rng.uniform(80, W - 80), rng.uniform(water_y0 + 20, water_y1 - 20)
        oid += 1
        objs.append(Obj(oid, rng.choice(sprites), x, y, side, rng.uniform(1.6, 3.0), 0.0, 0.0, t0, t0 + n_vis / FPS - 1e-6))
    for k in range(n_blink):
        t0 = round(rng.uniform(5, DURATION_S - 10) * FPS) / FPS
        side = rng.uniform(26, 50)
        x, y = rng.uniform(80, W - 80), rng.uniform(water_y0 + 20, water_y1 - 20)
        oid += 1
        objs.append(Obj(oid, rng.choice(sprites), x, y, side, rng.uniform(1.6, 3.0), rng.uniform(-3, 3), 0.0, t0, t0 + 3.0, "blink"))

    root = out_root / name
    (root / "img1").mkdir(parents=True, exist_ok=True)
    gt_rows: list[tuple[int, int, list[float], int, int, float]] = []
    bg_cache: dict[int, np.ndarray] = {}
    for fi in range(1, n_frames + 1):
        t = (fi - 1) / FPS
        bi = min(len(bg_frames) - 1, int(t))          # 1 Hz background held for 5 frames
        bfi, bpath = bg_frames[bi]
        if bi not in bg_cache:
            bg_cache.clear()
            bg_cache[bi] = cv2.resize(cv2.imread(str(bpath)), (W, H), interpolation=cv2.INTER_AREA)
        frame = bg_cache[bi].copy()
        for boid, rows in bg_gt.items():
            for fr, b in rows:
                if fr == bfi:
                    gt_rows.append((fi, boid, [b[0] * sx, b[1] * sy, b[2] * sx, b[3] * sy], 1, 1, 1.0))
        # far (small, high y0) first so near objects occlude them
        for o in sorted(objs, key=lambda o: o.y):
            if not o.visible(fi, t):
                continue
            b = o.box(t)
            if b[0] < 0 or b[1] < 0 or b[0] + b[2] > W or b[1] + b[3] > H:
                continue
            paste(frame, o.sprite, b)
            gt_rows.append((fi, o.oid, b, 1, 2, 1.0))
        cv2.imwrite(str(root / "img1" / f"{fi:06d}.jpg"), frame, [cv2.IMWRITE_JPEG_QUALITY, 90])
    write_sequence(root, name, FPS, W, H, gt_rows, {"classes": {"1": "vessel", "2": "boat"}, "label_source": "synthetic",
                   "notes": f"composited from dev-only sources; background {bg_seq.seq_id} (fvessel dev) with its GT ships"},
                   n_frames, extra_ini={"source": "synthetic", "background": bg_seq.seq_id})
    ids = {r[1] for r in gt_rows}
    row = {"seq_id": name, "set": out_root.name, "source_dataset": "synthetic", "split": "dev", "fps": FPS, "n_frames": n_frames,
           "n_tracks": len(ids), "n_boxes": len(gt_rows), "width": W, "height": H, "viewpoint": "shore_fixed",
           "platform_motion": "static", "sea_state": "calm_river", "hfov_deg": 56.3, "label_source": "synthetic",
           "gt_status": "complete", "background": bg_seq.seq_id, "n_cluster_objects": n_obj, "n_clusters": n_clusters,
           "n_cameo": n_cameo, "n_blink": n_blink, "seed": seed,
           "notes": f"dense small boats composited on fvessel dev frames; {n_cameo} cameos (1-3 frames), {n_blink} blinkers"}
    append_index(out_root, row)
    return row


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", required=True)
    ap.add_argument("--out", required=True)
    a = ap.parse_args()
    data, out_root = Path(a.data).expanduser(), Path(a.out).expanduser()
    sprites = harvest_sprites(data, random.Random(0))
    print(f"{len(sprites)} sprites")
    for sc in SCENARIOS:
        row = build(*sc, data=data, out_root=out_root, sprites=sprites)
        print(json.dumps({k: row[k] for k in ("seq_id", "n_tracks", "n_boxes", "background")}))


if __name__ == "__main__":
    main()
