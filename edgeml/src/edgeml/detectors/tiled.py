"""Small-object helpers that wrap any Detector: SAHI-style tiling and a horizon-band crop.

Both run the base detector on the full frame plus on crops at (near) native resolution, map boxes back,
and merge with class-agnostic NMS. Far contacts on the horizon are a few pixels tall at 1080p, so a 640
full-frame pass (3x downscale) loses them; crops recover them at the cost of extra inferences.
"""
from __future__ import annotations

from typing import Callable, Optional

import numpy as np

from ..types import Detection
from .base import nms_xywh


def _merge(dets: list[Detection], iou_thr: float, max_det: int) -> list[Detection]:
    if not dets:
        return []
    boxes = np.array([d.box for d in dets], dtype=np.float32)
    scores = np.array([d.score for d in dets], dtype=np.float32)
    keep = nms_xywh(boxes, scores, iou_thr)
    return [dets[i] for i in keep[:max_det]]


def _shift(dets: list[Detection], dx: float, dy: float) -> list[Detection]:
    for d in dets:
        d.box = [d.box[0] + dx, d.box[1] + dy, d.box[2], d.box[3]]
    return dets


def _run_crops(base, image_bgr, crops, include_full, merge_iou, max_det):
    dets: list[Detection] = []
    n_calls = 0
    if include_full:
        dets += base.detect(image_bgr)
        n_calls += 1
    for (x0, y0, x1, y1) in crops:
        sub = image_bgr[y0:y1, x0:x1]
        if sub.size == 0:
            continue
        dets += _shift(base.detect(sub), x0, y0)
        n_calls += 1
    return _merge(dets, merge_iou, max_det), n_calls


class TiledDetector:
    """SAHI-style: fixed square tiles with overlap over the whole frame (+ optional full-frame pass)."""

    def __init__(self, base, tile: int = 640, overlap: float = 0.2, include_full: bool = True,
                 merge_iou: float = 0.5, max_det: int = 300):
        self.base, self.tile, self.overlap, self.include_full = base, tile, overlap, include_full
        self.merge_iou, self.max_det = merge_iou, max_det
        self.name = f"{base.name}+sahi{tile}"
        self.n_calls = 0
        self.last_ms = float("nan")

    def crops(self, h: int, w: int):
        step = int(self.tile * (1 - self.overlap))
        xs = list(range(0, max(w - self.tile, 0) + 1, step)) or [0]
        ys = list(range(0, max(h - self.tile, 0) + 1, step)) or [0]
        if xs[-1] + self.tile < w:
            xs.append(w - self.tile)
        if ys[-1] + self.tile < h:
            ys.append(h - self.tile)
        return [(max(x, 0), max(y, 0), min(x + self.tile, w), min(y + self.tile, h)) for y in ys for x in xs]

    def detect(self, image_bgr: np.ndarray) -> list[Detection]:
        import time

        t0 = time.perf_counter()
        h, w = image_bgr.shape[:2]
        out, self.n_calls = _run_crops(self.base, image_bgr, self.crops(h, w), self.include_full, self.merge_iou, self.max_det)
        self.last_ms = (time.perf_counter() - t0) * 1000
        return out

    def info(self):
        return {**self.base.info(), "name": self.name, "tiling": "sahi", "tile": self.tile, "overlap": self.overlap,
                "include_full": self.include_full}


def estimate_horizon_y(image_bgr: np.ndarray) -> float:
    """Cheap horizon estimate: row with the strongest mean vertical gradient of the blurred luminance,
    searched in the central 15–85% of the height. Good enough to place a band; not a line fit."""
    import cv2

    g = cv2.cvtColor(image_bgr, cv2.COLOR_BGR2GRAY)
    g = cv2.resize(g, (g.shape[1] // 4, g.shape[0] // 4), interpolation=cv2.INTER_AREA)
    g = cv2.GaussianBlur(g, (0, 0), 3)
    row = g.astype(np.float32).mean(axis=1)
    grad = np.abs(np.diff(row))
    h = len(grad)
    lo, hi = int(0.15 * h), int(0.85 * h)
    y = lo + int(np.argmax(grad[lo:hi]))
    return float((y + 0.5) * 4)


class HorizonBandDetector:
    """Crop a horizontal band around the horizon and tile it with square crops of the band height."""

    def __init__(self, base, band_frac: float = 0.2, overlap: float = 0.2, include_full: bool = True,
                 merge_iou: float = 0.5, max_det: int = 300, n_tiles: int = 3,
                 horizon_fn: Optional[Callable[[np.ndarray], float]] = None):
        self.base, self.band_frac, self.overlap, self.include_full = base, band_frac, overlap, include_full
        self.n_tiles = n_tiles
        self.merge_iou, self.max_det = merge_iou, max_det
        self.horizon_fn = horizon_fn or estimate_horizon_y
        self.horizon_override: Optional[float] = None  # set per image to use GT horizon (SMD dev analysis)
        self.name = f"{base.name}+hband{int(band_frac*100)}x{n_tiles or 'sq'}"
        self.n_calls = 0
        self.last_ms = float("nan")
        self.last_horizon_y = float("nan")

    def crops(self, image_bgr: np.ndarray):
        h, w = image_bgr.shape[:2]
        yh = self.horizon_override if self.horizon_override is not None else self.horizon_fn(image_bgr)
        self.last_horizon_y = yh
        bh = int(round(h * self.band_frac))
        y0 = int(np.clip(yh - bh / 2, 0, h - bh))
        y1 = y0 + bh
        if self.n_tiles:
            # n_tiles crops across the band (literature: full frame + 2-4 band tiles), widened by overlap
            tw = int(min(w, np.ceil(w / self.n_tiles * (1 + self.overlap))))
            xs = [int(round(i * (w - tw) / max(self.n_tiles - 1, 1))) for i in range(self.n_tiles)] if self.n_tiles > 1 else [0]
            return [(x, y0, min(x + tw, w), y1) for x in xs]
        step = int(bh * (1 - self.overlap))
        xs = list(range(0, max(w - bh, 0) + 1, step))
        if xs[-1] + bh < w:
            xs.append(w - bh)
        return [(x, y0, min(x + bh, w), y1) for x in xs]

    def detect(self, image_bgr: np.ndarray) -> list[Detection]:
        import time

        t0 = time.perf_counter()
        out, self.n_calls = _run_crops(self.base, image_bgr, self.crops(image_bgr), self.include_full, self.merge_iou, self.max_det)
        self.last_ms = (time.perf_counter() - t0) * 1000
        return out

    def info(self):
        return {**self.base.info(), "name": self.name, "tiling": "horizon_band", "band_frac": self.band_frac,
                "n_tiles": self.n_tiles, "overlap": self.overlap, "include_full": self.include_full}
