"""Reference runner: frames -> detector (every N frames, or adaptive) -> tracker -> sighting aggregator.

`run_clip(frames, detector, tracker, aggregator, embedder=None, detect_every=1)` (CONTRACTS §2).
`frames` may be an iterable of BGR arrays, of (frame_idx, t_s, image) tuples, or of image paths; `fps` gives
t_s when the frames don't carry it. Between detector runs the tracker coasts (no update), so a tracker's
lost-track timers are in *tracker updates*; `make_tracker(..., fps=fps/detect_every)` keeps them in seconds.
"""
from __future__ import annotations

import time
from dataclasses import dataclass, replace
from pathlib import Path
from typing import Iterable, Optional

import cv2
import numpy as np

from .types import Detection, Track


@dataclass
class AdaptiveRate:
    """Detect every `idle_every` frames; drop to `active_every` while the aggregator has unconfirmed or
    just-opened sightings (something new is happening), for `active_for_s` after an open."""

    idle_every: int = 5
    active_every: int = 1
    active_for_s: float = 3.0

    def every(self, aggregator, t_s: float) -> int:
        fn = getattr(aggregator, "needs_attention", None)
        if fn is not None and fn(t_s, self.active_for_s):
            return self.active_every
        return self.idle_every


def _iter_frames(frames, fps: Optional[float]):
    for i, f in enumerate(frames):
        if isinstance(f, tuple):
            frame_idx, t_s, img = f
            if isinstance(img, (str, Path)):
                img = cv2.imread(str(img))
        elif isinstance(f, (str, Path)):
            frame_idx, img = i + 1, cv2.imread(str(f))
            t_s = (i) / (fps or 1.0)
        else:
            frame_idx, img, t_s = i + 1, f, (i) / (fps or 1.0)
        yield int(frame_idx), float(t_s), img


def run_clip(frames: Iterable, detector, tracker, aggregator, embedder=None, detect_every: int | AdaptiveRate = 1, stabilizer=None,
             fps: Optional[float] = None, record_dets: bool = True) -> dict:
    """Returns {"events", "dets": [(frame_idx,t_s,[Detection])], "tracks": [(frame_idx,t_s,[Track])],
    "sighting_tracks": [(sighting_id, frame_idx, box, score)], "timing": {...}, "n_frames", "n_detect_frames"}."""
    if embedder is not None and hasattr(aggregator, "embedder"):
        aggregator.embedder = embedder   # embeddings are computed once per track at key-frame time, for relink only (literature §2)

    events: list[dict] = []
    dets_out: list[tuple[int, float, list[Detection]]] = []
    tracks_out: list[tuple[int, float, list[Track]]] = []
    t_det = t_trk = t_agg = 0.0
    n_frames = n_det = 0
    last_t = 0.0
    k = 0
    for frame_idx, t_s, img in _iter_frames(frames, fps):
        n_frames += 1
        last_t = t_s
        every = detect_every.every(aggregator, t_s) if isinstance(detect_every, AdaptiveRate) else int(detect_every)
        if k % max(1, every) != 0:
            k += 1
            continue
        k = 1
        n_det += 1
        if hasattr(detector, "set_frame"):
            detector.set_frame(frame_idx)
        t0 = time.perf_counter()
        dets = detector.detect(img)
        t1 = time.perf_counter()
        if stabilizer is not None:
            M = stabilizer.estimate(img)
            Minv = stabilizer.inverse(M)
            dets_in = [replace(d, box=stabilizer.warp_box(d.box, M)) for d in dets]
            img_in = cv2.warpAffine(img, M, (img.shape[1], img.shape[0])) if getattr(tracker, "uses_image", False) else img
            tracks = [replace(t, box=stabilizer.warp_box(t.box, Minv)) for t in tracker.update(dets_in, img_in, frame_idx, t_s)]
        else:
            tracks = tracker.update(dets, img, frame_idx, t_s)
        t2 = time.perf_counter()
        if hasattr(aggregator, "set_frame"):
            aggregator.set_frame(img)
        events.extend(aggregator.update(tracks, img, frame_idx, t_s))
        t3 = time.perf_counter()
        t_det += t1 - t0
        t_trk += t2 - t1
        t_agg += t3 - t2
        if record_dets:
            dets_out.append((frame_idx, t_s, dets))
        tracks_out.append((frame_idx, t_s, tracks))
    events.extend(aggregator.flush(last_t + 1e-3))
    traj = aggregator.trajectories() if hasattr(aggregator, "trajectories") else []
    return {
        "events": events,
        "dets": dets_out,
        "tracks": tracks_out,
        "sighting_tracks": traj,
        "n_frames": n_frames,
        "n_detect_frames": n_det,
        "timing": {"detect_s": t_det, "track_s": t_trk, "aggregate_s": t_agg,
                   "ms_per_detect_frame": 1000.0 * (t_det + t_trk + t_agg) / max(1, n_det)},
    }


def write_mot(path: str | Path, rows: Iterable[tuple[int, int, list[float], float]]) -> None:
    """rows: (frame_idx, id, [x,y,w,h], score) -> MOTChallenge txt."""
    with open(path, "w") as f:
        for fr, tid, b, sc in rows:
            f.write(f"{int(fr)},{int(tid)},{b[0]:.2f},{b[1]:.2f},{b[2]:.2f},{b[3]:.2f},{sc:.4f},-1,-1,-1\n")
