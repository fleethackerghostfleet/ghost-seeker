"""Budget-admitted horizon tiles for live CPU inference; no model or threshold changes.

The budget is a soft deadline: an ONNX call already running cannot be preempted.
Every frame gets its native pass; extra calls are admitted using measured cost.
"""
from __future__ import annotations

from dataclasses import dataclass, replace
import math
import threading
import time

import cv2
import numpy as np

from ..types import Detection
from .tiled import TiledDetector, _merge

MOB_LABEL = "person in water"
VESSELS = {"boat", "ship", "vessel", "sailboat", "tanker", "cargo ship", "small boat"}


@dataclass(frozen=True)
class LiveTilingConfig:
    enabled: bool = True
    min_pixels: int = 1280 * 720
    tile: int = 640
    overlap: float = 0.2
    max_tiles: int = 2
    budget_ms: float = 3500.0
    cost_margin: float = 1.2
    merge_iou: float = 0.5

    def __post_init__(self):
        if (not isinstance(self.enabled, bool)
                or any(type(v) is not int for v in (self.min_pixels, self.tile, self.max_tiles))
                or self.min_pixels < 0 or self.tile < 32 or self.max_tiles < 0
                or not 0 <= self.overlap < 1 or not 0 < self.merge_iou <= 1
                or int(self.tile * (1 - self.overlap)) < 1
                or not math.isfinite(self.budget_ms) or self.budget_ms < 0
                or not math.isfinite(self.cost_margin) or self.cost_margin < 1):
            raise ValueError("invalid live tiling configuration")


def horizon_y(image: np.ndarray) -> float | None:
    """Abstain on low-contrast/too-small frames; this is not water segmentation."""
    h, w = image.shape[:2]
    if min(h, w) < 32:
        return None
    g = cv2.cvtColor(cv2.resize(image, (320, 180)), cv2.COLOR_BGR2GRAY)
    row = cv2.GaussianBlur(g, (0, 0), 2).mean(axis=1)
    grad = np.abs(np.diff(row))
    y = 27 + int(np.argmax(grad[27:153]))
    if grad[y] < 2:
        return None
    return float((y + 0.5) * h / 180)


def water_people(dets: list[Detection], horizon: float | None, height: int, score_thr: float) -> list[Detection]:
    """Possible MOB: person below the maritime horizon, excluding people aboard vessels.

    Geometry is only a water-context proxy: shore/pier people can still be false
    positives. Keep generic people unchanged when context is uncertain.
    """
    if horizon is None:
        return dets
    vessels = [d for d in dets if d.label in VESSELS and d.score >= score_thr]
    out = []
    for d in dets:
        x, y, w, h = d.box
        aboard = any(v.box[0] - .1 * v.box[2] <= x + w / 2 <= v.box[0] + 1.1 * v.box[2]
                     and v.box[1] - .5 * v.box[3] <= y + h <= v.box[1] + 1.1 * v.box[3] for v in vessels)
        if d.label == "person" and y + h / 2 > horizon + .02 * height and not aboard:
            d = replace(d, label=MOB_LABEL)
        out.append(d)
    return out


class LiveTiledDetector:
    def __init__(self, base, config: LiveTilingConfig | None = None, *, score_thr: float = .35,
                 clock=time.perf_counter, horizon_fn=horizon_y):
        self.base, self.config = base, config or LiveTilingConfig()
        self.score_thr, self.clock, self.horizon_fn = score_thr, clock, horizon_fn
        self.name = f"{base.name}+live-tiles{self.config.max_tiles}-mob"
        self.model_bytes = getattr(base, "model_bytes", 0)
        self.last_timing_ms: dict[str, float] = {}
        self.n_calls = 0
        self._cost_ms = 0.0
        self._cursor: dict[tuple[int, int], int] = {}
        self._lock = threading.Lock()

    def detect(self, image_bgr: np.ndarray) -> list[Detection]:
        with self._lock:
            return self._detect(image_bgr)

    def _detect(self, image):
        cfg = self.config
        start = self.clock()
        dets = list(self.base.detect(image))
        native_ms = (self.clock() - start) * 1000
        self._cost_ms = max(native_ms, self._cost_ms * .9)
        self.n_calls = 1
        h, w = image.shape[:2]
        horizon = self.horizon_fn(image)
        if cfg.enabled and h * w > cfg.min_pixels and cfg.max_tiles:
            grid = TiledDetector(self.base, tile=cfg.tile, overlap=cfg.overlap)
            if horizon is None:
                crops = grid.crops(h, w)
            else:
                y0 = max(0, min(h - min(cfg.tile, h), int(horizon - cfg.tile * .2)))
                crops = [(x0, y0, x1, min(h, y0 + cfg.tile)) for x0, _, x1, _ in grid.crops(min(h, cfg.tile), w)]
            key = (h, w)
            cursor = self._cursor.get(key, 0) % len(crops)
            for i in range(min(cfg.max_tiles, len(crops))):
                elapsed = (self.clock() - start) * 1000
                if elapsed + self._cost_ms * cfg.cost_margin > cfg.budget_ms:
                    break
                x0, y0, x1, y1 = crops[(cursor + i) % len(crops)]
                t0 = self.clock()
                local = self.base.detect(image[y0:y1, x0:x1])
                self._cost_ms = max(self._cost_ms, (self.clock() - t0) * 1000)
                dets.extend(replace(d, box=[d.box[0] + x0, d.box[1] + y0, d.box[2], d.box[3]]) for d in local)
                self.n_calls += 1
            self._cursor[key] = (cursor + max(1, self.n_calls - 1)) % len(crops)
        # Unlike the offline agnostic merge, don't erase a person overlapping a boat.
        merged = []
        for label in sorted({d.label for d in dets}):
            merged.extend(_merge([d for d in dets if d.label == label], cfg.merge_iou, 300))
        out = water_people(merged, horizon, h, self.score_thr)
        self.last_timing_ms = {"native": native_ms, "total": (self.clock() - start) * 1000,
                               "tiles": self.n_calls - 1}
        return sorted(out, key=lambda d: d.score, reverse=True)[:300]

    def info(self):
        return {"name": self.name, "tiling": vars(self.config), "mob": "horizon-context candidate"}
