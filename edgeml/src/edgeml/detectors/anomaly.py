"""Cheap "anything-on-water" proposer and its fusion with the closed-set detector (task H).

``SeaAnomalyProposer`` finds blobs that deviate from the local horizontal sea statistics below the (temporally smoothed)
horizon: per pixel z = ||(Lab - mean_x(Lab)) / std_x(Lab)|| with running means over a wide horizontal window, so sea
texture, glare gradients and haze (which vary mostly with row / range) are normalised away while compact objects of any
class stick out. Costs ~2-4 ms/frame at 480 px on CPU; no learned weights.

``AnomalyFusionDetector`` appends proposals that no closed-set detection already covers, so the tracker and the
sighting aggregator see unusual contacts the COCO ``boat`` head misses, while vessels keep their YOLO box/score.
"""
from __future__ import annotations

import numpy as np

from ..types import Detection
from .tiled import estimate_horizon_y


class SeaAnomalyProposer:
    def __init__(self, work_width: int = 480, z_thr: float = 3.5, win: int = 61, min_area: int = 6, max_area_frac: float = 0.02,
                 max_props: int = 8, horizon_ema: float = 0.8, score_lo: float = 0.3, score_hi: float = 0.9,
                 bright_achromatic_penalty: float = 0.5, aspect: tuple[float, float] = (0.25, 12.0), name: str = "sea-anomaly"):
        self.work_width, self.z_thr, self.win = work_width, z_thr, win | 1
        self.min_area, self.max_area_frac, self.max_props = min_area, max_area_frac, max_props
        self.horizon_ema, self.score_lo, self.score_hi = horizon_ema, score_lo, score_hi
        self.bright_achromatic_penalty, self.aspect = bright_achromatic_penalty, aspect
        self.name = name
        self.horizon_y: float | None = None
        self.last_z: np.ndarray | None = None

    def horizon(self, image_bgr: np.ndarray) -> float:
        y = estimate_horizon_y(image_bgr)
        if self.horizon_y is None or abs(y - self.horizon_y) > 0.25 * image_bgr.shape[0]:
            self.horizon_y = y
        else:
            self.horizon_y = self.horizon_ema * self.horizon_y + (1 - self.horizon_ema) * y
        return self.horizon_y

    def zmap(self, image_bgr: np.ndarray) -> tuple[np.ndarray, float, int]:
        import cv2

        H, W = image_bgr.shape[:2]
        s = min(1.0, self.work_width / W)
        small = cv2.resize(image_bgr, (int(W * s), int(H * s)), interpolation=cv2.INTER_AREA) if s < 1 else image_bgr
        hz = self.horizon(image_bgr)
        y0 = int(np.clip(hz * s + 2, 0, small.shape[0] - 4))
        lab = cv2.cvtColor(small[y0:], cv2.COLOR_BGR2LAB).astype(np.float32)
        k = (self.win, 1)
        mu = cv2.blur(lab, k)
        var = np.maximum(cv2.blur(lab * lab, k) - mu * mu, 1.0)
        dev = (lab - mu) / np.sqrt(var)
        # second pass: re-estimate the sea statistics from inliers only, so an object that fills a good part of the
        # window does not inflate the local variance and hide itself
        w = (np.sqrt((dev ** 2).sum(axis=2)) < 2.5).astype(np.float32)
        wsum = np.maximum(cv2.blur(w, k), 1e-3)[..., None]
        w = w[..., None]
        mu = cv2.blur(lab * w, k) / wsum
        var = np.maximum(cv2.blur(lab * lab * w, k) / wsum - mu * mu, 1.0)
        dev = (lab - mu) / np.sqrt(var)
        z = np.sqrt((dev ** 2).sum(axis=2))
        # bright, low-chroma deviations are mostly foam / glint
        bright = (dev[..., 0] > 0) & (np.abs(dev[..., 1]) + np.abs(dev[..., 2]) < 0.5 * np.abs(dev[..., 0]))
        z = np.where(bright, z * self.bright_achromatic_penalty, z)
        z = cv2.blur(z, (3, 3))                       # pixel noise averages out, compact objects keep their z
        return z, s, y0

    def detect(self, image_bgr: np.ndarray) -> list[Detection]:
        import cv2

        z, s, y0 = self.zmap(image_bgr)
        self.last_z = z
        mask = (z > self.z_thr).astype(np.uint8)
        mask = cv2.morphologyEx(mask, cv2.MORPH_OPEN, np.ones((2, 2), np.uint8))
        mask = cv2.morphologyEx(mask, cv2.MORPH_CLOSE, np.ones((3, 3), np.uint8))
        n, lab, stats, _ = cv2.connectedComponentsWithStats(mask, connectivity=8)
        H, W = image_bgr.shape[:2]
        max_area = self.max_area_frac * z.size
        out = []
        for i in range(1, n):
            x, y, w, h, area = stats[i]
            if area < self.min_area or area > max_area or w < 2 or h < 2:
                continue
            if not (self.aspect[0] <= w / h <= self.aspect[1]):
                continue
            zi = float(z[lab == i].mean())
            score = round(min(self.score_hi, self.score_lo + (self.score_hi - self.score_lo) * float(np.clip((zi - self.z_thr) / (2.5 * self.z_thr), 0, 1))), 4)
            box = [float(x / s), float((y + y0) / s), float(w / s), float(h / s)]
            out.append(Detection(box=box, score=score, label="anomaly", label_id=None))
        out.sort(key=lambda d: -d.score)
        return out[:self.max_props]

    def info(self) -> dict:
        return {"name": self.name, "work_width": self.work_width, "z_thr": self.z_thr, "win": self.win}


def _iou(a, b) -> float:
    ax2, ay2, bx2, by2 = a[0] + a[2], a[1] + a[3], b[0] + b[2], b[1] + b[3]
    iw, ih = max(0.0, min(ax2, bx2) - max(a[0], b[0])), max(0.0, min(ay2, by2) - max(a[1], b[1]))
    inter = iw * ih
    return inter / (a[2] * a[3] + b[2] * b[3] - inter + 1e-9)


class AnomalyFusionDetector:
    """Closed-set detections first; anomaly proposals only where nothing (score >= ``cover_thr``) already covers them."""

    def __init__(self, base, proposer: SeaAnomalyProposer | None = None, iou_suppress: float = 0.2, cover_thr: float = 0.05,
                 pad: float = 0.15):
        self.base, self.proposer = base, proposer or SeaAnomalyProposer()
        self.iou_suppress, self.cover_thr, self.pad = iou_suppress, cover_thr, pad
        self.name = f"{getattr(base, 'name', 'base')}+{self.proposer.name}"
        self.model_bytes = getattr(base, "model_bytes", 0)
        self.last_timing_ms: dict[str, float] = {}

    def _covered(self, p: Detection, dets: list[Detection]) -> bool:
        px, py = p.box[0] + p.box[2] / 2, p.box[1] + p.box[3] / 2
        for d in dets:
            if d.score < self.cover_thr:
                continue
            if _iou(p.box, d.box) >= self.iou_suppress:
                return True
            x0, y0 = d.box[0] - self.pad * d.box[2], d.box[1] - self.pad * d.box[3]
            if x0 <= px <= x0 + (1 + 2 * self.pad) * d.box[2] and y0 <= py <= y0 + (1 + 2 * self.pad) * d.box[3]:
                return True
        return False

    def detect(self, image_bgr: np.ndarray) -> list[Detection]:
        import time

        dets = self.base.detect(image_bgr)
        t0 = time.perf_counter()
        props = [p for p in self.proposer.detect(image_bgr) if not self._covered(p, dets)]
        self.last_timing_ms = dict(getattr(self.base, "last_timing_ms", {}) or {}, anomaly=(time.perf_counter() - t0) * 1e3)
        return dets + props

    def info(self) -> dict:
        base = self.base.info() if hasattr(self.base, "info") else {"name": getattr(self.base, "name", "base")}
        return {**base, "fusion": self.proposer.info()}
