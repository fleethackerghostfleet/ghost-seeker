"""Baselines that produce *events* in the same shape as the aggregator so every metric/selector applies unchanged.

* naive:        one report per detection per frame; form ladder = fact + full frame.
* image_dedup:  per-frame detections, but a frame is suppressed when its 8×8 colour fingerprint is within
                `sim_thr` of a frame reported in the last `window_s` (image-level near-duplicate suppression,
                the same idea as algorithm/'s fingerprint penalty — soft, no object identity).
Each reported detection becomes its own "sighting" (one frame long) so duplicates-per-object counts how many
times the same object is re-reported.
"""
from __future__ import annotations

import json
from typing import Iterable

import cv2
import numpy as np

from edgeml.types import Detection, Form


def fingerprint(image_bgr: np.ndarray) -> np.ndarray:
    small = cv2.resize(image_bgr, (8, 8), interpolation=cv2.INTER_AREA).astype(np.float32) / 255.0
    return small.reshape(-1)


def fp_similarity(a: np.ndarray, b: np.ndarray) -> float:
    return float(1.0 - np.sqrt(np.mean((a - b) ** 2)) / 0.25)  # 1 = identical, ~0 at rms diff 0.25


def _full_forms(img: np.ndarray, fact: dict, full_max_side: int = 1280, q: int = 80) -> list[Form]:
    h, w = img.shape[:2]
    s = min(1.0, full_max_side / max(h, w))
    im = img if s >= 1 else cv2.resize(img, (int(w * s), int(h * s)), interpolation=cv2.INTER_AREA)
    ok, buf = cv2.imencode(".jpg", im, [int(cv2.IMWRITE_JPEG_QUALITY), q])
    return [Form(kind="fact", bytes=len(json.dumps(fact, separators=(",", ":")).encode()), quality=0, w=0, h=0),
            Form(kind="full", bytes=int(len(buf)), quality=q, w=im.shape[1], h=im.shape[0])]


def _det_event(clip_id: str, sid: str, d: Detection, frame_idx: int, t_s: float, img: np.ndarray, full_bytes_cache: dict) -> dict:
    fact = {"sighting_id": sid, "clip_id": clip_id, "event": "open", "t_s": t_s, "frame_idx": frame_idx, "label": d.label,
            "box": [float(v) for v in d.box], "best_score": float(d.score)}
    key = frame_idx
    if key not in full_bytes_cache:
        full_bytes_cache[key] = _full_forms(img, fact)
    forms = full_bytes_cache[key]
    ev = {**fact, "label_scores": {d.label: 1.0}, "subtype": None, "unknownness": float(1 - d.score), "best_frame_idx": frame_idx,
          "best_box": [float(v) for v in d.box], "first_t_s": t_s, "last_t_s": t_s, "n_frames": 1, "track_ids": [],
          "value": float(min(1.0, 0.5 * d.score + 0.2 * (1 - d.score) + 0.2)), "forms": [f.to_dict() for f in forms]}
    return ev


def naive_events(clip_id: str, per_frame: Iterable[tuple[int, float, np.ndarray, list[Detection]]], min_score: float = 0.25):
    """Yields (events, sighting_tracks) for the per-detection baseline."""
    events, traj, cache = [], [], {}
    n = 0
    for frame_idx, t_s, img, dets in per_frame:
        cache = {}
        for d in dets:
            if d.score < min_score:
                continue
            n += 1
            sid = f"{clip_id}:naive{n:06d}"
            events.append(_det_event(clip_id, sid, d, frame_idx, t_s, img, cache))
            traj.append((sid, frame_idx, list(d.box), d.score))
    return events, traj


def image_dedup_events(clip_id: str, per_frame: Iterable[tuple[int, float, np.ndarray, list[Detection]]], min_score: float = 0.25,
                       sim_thr: float = 0.9, window_s: float = 30.0):
    events, traj = [], []
    sent: list[tuple[float, np.ndarray]] = []
    n = 0
    for frame_idx, t_s, img, dets in per_frame:
        dets = [d for d in dets if d.score >= min_score]
        if not dets:
            continue
        fp = fingerprint(img)
        sent = [(t, f) for t, f in sent if t_s - t <= window_s]
        if any(fp_similarity(fp, f) >= sim_thr for _t, f in sent):
            continue
        sent.append((t_s, fp))
        cache = {}
        for d in dets:
            n += 1
            sid = f"{clip_id}:dedup{n:06d}"
            events.append(_det_event(clip_id, sid, d, frame_idx, t_s, img, cache))
            traj.append((sid, frame_idx, list(d.box), d.score))
    return events, traj
