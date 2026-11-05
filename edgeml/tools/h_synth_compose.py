#!/usr/bin/env python
"""Composite unusual-object sprites onto DEV sea footage -> `synth_unknown` MOT set (sequences, not stills).

Geometry: pinhole camera with f = max(W,H)/2/tan(hfov/2); an object of real width Wm at range d is f*Wm/d px wide and its
waterline sits f*cam_h/d px below the (per-frame, smoothed) horizon. Objects move with a constant world velocity, bob with
the sea, follow camera pans (phase correlation), and are haze/tone/blur/noise matched to the local background.

Only DEV sequences are allowed as backgrounds (checked against configs/splits.yaml). The synthetic split is by class:
TEST classes are never seen while tuning.
"""
from __future__ import annotations

import argparse
import json
import math
import random
import sys
from pathlib import Path

import cv2
import numpy as np
import yaml

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
from edgeml.data.seqs import append_index, load_sequence, write_sequence  # noqa: E402
from edgeml.detectors.tiled import estimate_horizon_y  # noqa: E402

sys.path.insert(0, str(Path(__file__).resolve().parent))
from h_gen_sprites import CLASSES  # noqa: E402

DEV_CLASSES = ["semi_submersible", "usv", "jet_ski", "buoy"]
TEST_CLASSES = ["periscope_mast", "life_raft", "debris_container"]

# world speed ranges m/s (lateral), range-rate fraction
SPEED = {"semi_submersible": (1.5, 5.0), "periscope_mast": (1.5, 4.0), "usv": (3.0, 9.0), "jet_ski": (6.0, 14.0),
         "life_raft": (0.2, 0.8), "debris_container": (0.1, 0.5), "buoy": (0.0, 0.05)}

# background: set, seq, cam height m, horizon search (lo,hi frac of H), water region (y0,y1,x0,x1 frac), visibility m
BACKGROUNDS = [
    ("ghost_fleet_5fps", "Submarine_surfacing", 3.0, (0.40, 0.60), (0.53, 0.95, 0.00, 1.00), 8000),
    ("ghost_fleet_5fps", "Sailing_POV_Corfu_Paxos", 2.0, (0.38, 0.55), (0.50, 0.97, 0.00, 1.00), 8000),
    ("ghost_fleet_5fps", "A_ship_in_the_distance", 3.0, (0.40, 0.52), (0.48, 0.56, 0.00, 1.00), 10000),
    ("ghost_fleet_5fps", "Cruise_ship_Mediterranean_sunset_off_Italy", 25.0, (0.22, 0.40), (0.32, 0.97, 0.00, 1.00), 7000),
    ("ghost_fleet_5fps", "Far_away_but_did_you_see_that_splash", 1.5, (0.45, 0.65), (0.62, 0.97, 0.00, 1.00), 5000),
    ("ghost_fleet_5fps", "Ship_in_the_Distance_Utopia_of_the_Seas", 5.0, (0.40, 0.55), (0.49, 0.97, 0.00, 1.00), 6000),
    ("ghost_fleet_r2_1fps", "Cargo_Ship_Uncut_View_w1", 25.0, (0.25, 0.45), (0.36, 0.62, 0.00, 0.35), 6000),
    ("ghost_fleet_r2_1fps", "Cruise_Ship_POV", 15.0, (0.30, 0.45), (0.42, 0.95, 0.00, 1.00), 8000),
    ("ghost_fleet_r2_1fps", "Sunrise_Somewhere_in_the_Ocean", 20.0, (0.40, 0.55), (0.49, 0.74, 0.00, 0.60), 7000),
    ("ghost_fleet_r2_1fps", "Ship_crossing_Mariana_Trench", 20.0, (0.28, 0.45), (0.40, 0.95, 0.15, 1.00), 1500),
]


def dev_only(splits_path: Path) -> set[str]:
    cfg = yaml.safe_load(splits_path.read_text())
    test = set()

    def walk(o):
        if isinstance(o, dict):
            for k, v in o.items():
                if k == "test":
                    collect(v)
                else:
                    walk(v)
        elif isinstance(o, list):
            for v in o:
                walk(v)

    def collect(v):
        if isinstance(v, str):
            test.add(v)
        elif isinstance(v, list):
            for x in v:
                collect(x)
        elif isinstance(v, dict):
            for x in v.values():
                collect(x)

    walk(cfg)
    return test


def load_sprites(sprite_root: Path) -> dict[str, list[np.ndarray]]:
    out = {}
    for c in CLASSES:
        out[c] = [cv2.imread(str(p), cv2.IMREAD_UNCHANGED) for p in sorted((sprite_root / c).glob("*.png"))]
    return out


def horizon_track(frames: list[np.ndarray], lo: float, hi: float) -> np.ndarray:
    H = frames[0].shape[0]
    ys = []
    for im in frames:
        y0, y1 = int(lo * H), int(hi * H)
        y = estimate_horizon_y(im[y0:y1]) + y0 if y1 - y0 > 40 else (y0 + y1) / 2
        ys.append(y)
    ys = np.asarray(ys, float)
    med = np.median(ys)
    ys = np.where(np.abs(ys - med) > 0.04 * H, med, ys)        # reject outliers
    k = 5
    pad = np.pad(ys, (k // 2, k // 2), mode="edge")
    return np.asarray([np.median(pad[i:i + k]) for i in range(len(ys))])


def pan_track(frames: list[np.ndarray], y_lo: int, y_hi: int) -> np.ndarray:
    """Cumulative horizontal camera shift (px) from phase correlation on the sea band."""
    dx = [0.0]
    prev = None
    for im in frames:
        g = cv2.cvtColor(im[y_lo:y_hi], cv2.COLOR_BGR2GRAY)
        g = cv2.resize(g, (g.shape[1] // 2, max(8, g.shape[0] // 2))).astype(np.float32)
        g = cv2.GaussianBlur(g, (0, 0), 2)
        if prev is not None:
            (sx, sy), resp = cv2.phaseCorrelate(prev, g)
            dx.append(dx[-1] + (2 * sx if resp > 0.15 and abs(sx) < 40 else 0.0))
        prev = g
    return np.asarray(dx)


def render(frame: np.ndarray, sprite: np.ndarray, cx: float, y_wl: float, w_px: float, h_px: float, haze: float,
           horizon_col: np.ndarray, moving: bool, rng: random.Random) -> tuple[np.ndarray, list[float] | None]:
    H, W = frame.shape[:2]
    sh, sw = sprite.shape[:2]
    s = min(w_px / sw, h_px / sh)
    nw, nh = max(2, int(round(sw * s))), max(2, int(round(sh * s)))
    sp = cv2.resize(sprite, (nw, nh), interpolation=cv2.INTER_AREA)
    rgb, a = sp[..., :3].astype(np.float32), sp[..., 3].astype(np.float32) / 255.0
    x0, y0 = int(round(cx - nw / 2)), int(round(y_wl - nh))
    if x0 + nw <= 0 or x0 >= W or y0 + nh <= 0 or y0 >= H:
        return frame, None
    # local background statistics (3x box)
    px0, py0 = max(0, x0 - nw), max(0, y0 - nh)
    px1, py1 = min(W, x0 + 2 * nw), min(H, y0 + 2 * nh)
    patch = frame[py0:py1, px0:px1].astype(np.float32)
    gp = cv2.cvtColor(patch.astype(np.uint8), cv2.COLOR_BGR2GRAY).astype(np.float32)
    lap_var = float(cv2.Laplacian(gp, cv2.CV_32F).var()) if gp.size else 100.0
    noise_std = float((gp - cv2.GaussianBlur(gp, (0, 0), 1.5)).std()) if gp.size else 2.0
    lum_p = float(gp.mean()) if gp.size else 128.0
    # tone: pull luminance partially toward the background, haze toward the horizon colour
    lum_s = float((rgb.mean(axis=2) * a).sum() / max(a.sum(), 1e-6))
    gain = float(np.clip((0.35 * lum_p + 0.65 * lum_s) / max(lum_s, 1e-6), 0.6, 1.5))
    rgb = rgb * gain
    rgb = rgb * (1 - haze) + horizon_col[None, None, :] * haze
    # blur: small objects + soft backgrounds + distance
    sigma = 0.35 + 0.6 * max(0.0, 1 - nw / 60.0) + float(np.clip(0.8 - lap_var / 250.0, 0, 0.8)) + 0.8 * haze
    rgb = cv2.GaussianBlur(rgb, (0, 0), sigma)
    a = cv2.GaussianBlur(a, (0, 0), max(0.3, 0.7 * sigma))
    rgb += np.random.default_rng(rng.randrange(1 << 30)).normal(0, noise_std, rgb.shape).astype(np.float32)
    # paste with clipping
    fx0, fy0, fx1, fy1 = max(0, x0), max(0, y0), min(W, x0 + nw), min(H, y0 + nh)
    sx0, sy0 = fx0 - x0, fy0 - y0
    sub_rgb = rgb[sy0:sy0 + fy1 - fy0, sx0:sx0 + fx1 - fx0]
    sub_a = a[sy0:sy0 + fy1 - fy0, sx0:sx0 + fx1 - fx0][..., None]
    out = frame.copy()
    # waterline: faint darkening under the hull, foam streak behind moving objects
    ov = out.astype(np.float32)
    ew, eh = int(nw * 0.55) + 1, max(1, int(nh * 0.08) + 1)
    cxi, cyi = int(round(cx)), int(round(y_wl))
    mask = np.zeros((H, W), np.float32)
    cv2.ellipse(mask, (cxi, cyi), (ew, eh), 0, 0, 360, 1.0, -1)
    mask = cv2.GaussianBlur(mask, (0, 0), max(1.0, eh))
    ov = ov * (1 - 0.18 * mask[..., None])
    if moving:
        foam = np.zeros((H, W), np.float32)
        cv2.ellipse(foam, (cxi, cyi), (int(nw * 0.9) + 1, eh), 0, 0, 360, 1.0, -1)
        foam = cv2.GaussianBlur(foam, (0, 0), max(1.0, 1.5 * eh)) * (1 - haze)
        ov = ov + 55.0 * foam[..., None]
    out = ov.clip(0, 255).astype(np.uint8)
    region = out[fy0:fy1, fx0:fx1].astype(np.float32)
    out[fy0:fy1, fx0:fx1] = (sub_rgb * sub_a + region * (1 - sub_a)).clip(0, 255).astype(np.uint8)
    ys, xs = np.where(sub_a[..., 0] > 0.25)
    if len(xs) < 4:
        return out, None
    box = [float(fx0 + xs.min()), float(fy0 + ys.min()), float(xs.max() - xs.min() + 1), float(ys.max() - ys.min() + 1)]
    return out, box


def compose_sequence(bg, sprites, cls: str, rng: random.Random, out_root: Path, name: str, win: int, eval_root: Path,
                     n_obj: int, hfov_deg: float = 65.0) -> dict | None:
    set_name, seq_name, cam_h, (hz_lo, hz_hi), (wy0, wy1, wx0, wx1), vis_m = bg
    seq = load_sequence(eval_root / set_name / seq_name)
    n = seq.n_frames
    L = min(win, n)
    start = rng.randint(1, max(1, n - L + 1))
    frames = [cv2.imread(str(seq.frame_path(start + i))) for i in range(L)]
    H, W = frames[0].shape[:2]
    f = max(W, H) / 2 / math.tan(math.radians(hfov_deg) / 2)
    hz = horizon_track(frames, hz_lo, hz_hi)
    pan = pan_track(frames, int(wy0 * H), int(wy1 * H))
    dt = 1.0 / seq.fps
    Wm, Hm = CLASSES[cls][1], CLASSES[cls][2]
    objs = []
    for _ in range(n_obj):
        for _try in range(40):
            w_target = math.exp(rng.uniform(math.log(14), math.log(min(0.25 * W, 320))))
            d = f * Wm / w_target
            y_off = f * cam_h / d
            if not (wy0 * H < hz[0] + y_off < wy1 * H):
                continue
            x0 = rng.uniform((wx0 + 0.1) * W, (wx1 - 0.1) * W)
            v = rng.uniform(*SPEED[cls]) * rng.choice([-1, 1])
            vr = rng.uniform(-0.3, 0.3) * abs(v)
            si = rng.randrange(len(sprites[cls]))
            sp = sprites[cls][si]
            if rng.random() < 0.5:
                sp = sp[:, ::-1].copy()
            objs.append(dict(x_world=(x0 - W / 2) * d / f, d=d, v=v, vr=vr, sprite=sp, phase=rng.uniform(0, 6.28),
                             bob=rng.uniform(0.03, 0.10), sprite_idx=si))
            break
    if not objs:
        return None
    labels = dict(seq.labels)
    classes = {int(k): v for k, v in labels.get("classes", {}).items()}
    cid = max(classes, default=0) + 100
    classes[cid] = cls
    labels["classes"] = {str(k): v for k, v in sorted(classes.items())}
    labels["synthetic"] = {"class_ids": {str(cid): cls}, "background": f"{set_name}/{seq_name}", "frames": [start, start + L - 1]}
    gt_rows = []
    max_id = 0
    for fr, oid, box, conf, c, vis in seq.gt:
        if not (start <= fr < start + L):
            continue
        gt_rows.append((fr - start + 1, oid, box, conf, c, vis))
        max_id = max(max_id, oid)
    out_dir = out_root / name
    (out_dir / "img1").mkdir(parents=True, exist_ok=True)
    traj = {i: [] for i in range(len(objs))}
    alive = [True] * len(objs)
    for i, im in enumerate(frames):
        t = i * dt
        out = im
        hcol_y = int(np.clip(hz[i] + 3, 0, H - 8))
        for j, o in enumerate(objs):
            if not alive[j]:
                continue
            d = o["d"] + o["vr"] * t
            if d < 5:
                alive[j] = False
                continue
            xw = o["x_world"] + o["v"] * t
            cx = W / 2 + f * xw / d + pan[i]
            w_px, h_px = f * Wm / d, f * Hm / d
            if w_px < 3 or h_px < 3:                 # decide before painting: no unannotated pixels in complete-GT frames
                alive[j] = False
                continue
            y_wl = hz[i] + f * cam_h / d + o["bob"] * h_px * math.sin(2 * math.pi * 0.45 * t + o["phase"])
            haze = float(np.clip(1 - math.exp(-d / vis_m), 0, 0.75))
            strip = im[hcol_y:hcol_y + 6, max(0, int(cx) - 40):max(1, int(cx) + 40)]
            hcol = strip.reshape(-1, 3).mean(axis=0).astype(np.float32) if strip.size else np.array([180, 180, 180], np.float32)
            out, box = render(out, o["sprite"], cx, y_wl, w_px, h_px, haze, hcol, abs(o["v"]) > 1.0, rng)
            if box is None or box[2] < 3 or box[3] < 3:
                alive[j] = False
                continue
            traj[j].append((i + 1, box, d))
        cv2.imwrite(str(out_dir / "img1" / f"{i + 1:06d}.jpg"), out, [cv2.IMWRITE_JPEG_QUALITY, 90])
    syn = []
    for j, rows in traj.items():
        if len(rows) < 8:
            continue
        oid = max_id + 1 + j
        for fr, box, d in rows:
            gt_rows.append((fr, oid, box, 1, cid, 1.0))
        syn.append({"track_id": oid, "class": cls, "sprite_idx": objs[j]["sprite_idx"], "range0_m": objs[j]["d"],
                    "speed_mps": objs[j]["v"], "n_frames": len(rows), "box_w_px_median": float(np.median([r[1][2] for r in rows]))})
    if not syn:
        return None
    gt_rows.sort(key=lambda r: (r[0], r[1]))
    write_sequence(out_dir, name, seq.fps, W, H, gt_rows, labels, L,
                   extra_ini={"source": "synthetic_composite", "background": f"{set_name}/{seq_name}", "bg_start_frame": start,
                              "cam_height_m": cam_h, "hfov_deg": hfov_deg})
    (out_dir / "synth.json").write_text(json.dumps({"objects": syn, "horizon_y": hz.tolist(), "pan_px": pan.tolist()}, indent=1))
    return {"seq_id": name, "set": "synth_unknown", "source_dataset": "synth_unknown", "split": "test" if cls in TEST_CLASSES else "dev",
            "fps": seq.fps, "n_frames": L, "n_tracks": len({r[1] for r in gt_rows}), "n_boxes": len(gt_rows), "width": W, "height": H,
            "synthetic": True, "synth_class": cls, "synth_tracks": [s["track_id"] for s in syn], "background": f"{set_name}/{seq_name}",
            "classes": sorted({labels["classes"][str(r[4])] for r in gt_rows}), "label_source": "synthetic+teacher_bg",
            "gt_status": "complete", "license": "derived from dev clips; internal use only"}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--eval-root", default="~/data/edgeml_eval")
    ap.add_argument("--sprites", default="~/data/synth_src/sprites")
    ap.add_argument("--out", default="~/data/edgeml_eval/synth_unknown")
    ap.add_argument("--per-class", type=int, default=5)
    ap.add_argument("--win", type=int, default=40)
    ap.add_argument("--seed", type=int, default=7)
    ap.add_argument("--splits", default=str(Path(__file__).resolve().parents[1] / "configs" / "splits.yaml"))
    a = ap.parse_args()
    eval_root, out_root = Path(a.eval_root).expanduser(), Path(a.out).expanduser()
    locked = dev_only(Path(a.splits))
    for bg in BACKGROUNDS:
        assert bg[1] not in locked, f"{bg[1]} is a locked TEST sequence"
    sprites = load_sprites(Path(a.sprites).expanduser())
    rng = random.Random(a.seed)
    out_root.mkdir(parents=True, exist_ok=True)
    k = 0
    for cls in DEV_CLASSES + TEST_CLASSES:
        made = 0
        bgs = BACKGROUNDS[:]
        rng.shuffle(bgs)
        for bg in bgs * 3:
            if made >= a.per_class:
                break
            name = f"synth_{cls}_{bg[1][:18]}_{k:02d}"
            row = compose_sequence(bg, sprites, cls, rng, out_root, name, a.win, eval_root, n_obj=1 + (rng.random() < 0.4))
            k += 1
            if row is None:
                continue
            append_index(out_root, row)
            made += 1
            print(f"{row['split']:4s} {name}: {row['n_frames']} frames, synth tracks {row['synth_tracks']}", flush=True)


if __name__ == "__main__":
    main()
