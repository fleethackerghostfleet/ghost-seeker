"""Model-free thermal contact proposals; no IR training data or fitted weights."""
from __future__ import annotations

import time

import cv2
import numpy as np

from ..types import Detection
from .anomaly import AnomalyFusionDetector


def is_thermal(img_bgr: np.ndarray) -> bool:
    if img_bgr.size == 0:
        return False
    if img_bgr.ndim == 2 or img_bgr.shape[2] == 1:
        return True
    h, w = img_bgr.shape[:2]
    small = cv2.resize(img_bgr[..., :3], (64, max(1, round(h * 64 / w))), interpolation=cv2.INTER_AREA)
    channels = small.astype(np.float32)
    delta = np.abs(channels[..., 0] - channels[..., 1]).mean() + np.abs(channels[..., 1] - channels[..., 2]).mean()
    return bool(delta <= 6 and cv2.cvtColor(small, cv2.COLOR_BGR2HSV)[..., 1].mean() <= 18)


class ThermalHotspotProposer:
    def __init__(self, work_width=480, k=8.0, min_delta=40.0, min_area=12,
                 max_area_frac=0.04, aspect=(0.2, 8.0), max_props=8, polarity="auto", horizon_margin=0.04):
        if work_width < 16 or k <= 0 or min_delta <= 0 or min_area < 1 or not 0 < max_area_frac <= 1:
            raise ValueError("invalid thermal proposal thresholds")
        if polarity not in {"auto", "white", "black"} or max_props < 1 or not 0 <= horizon_margin <= 1:
            raise ValueError("invalid thermal proposal mode")
        if len(aspect) != 2 or not 0 < aspect[0] <= aspect[1]:
            raise ValueError("invalid thermal aspect range")
        self.work_width, self.k, self.min_delta = work_width, k, min_delta
        self.min_area, self.max_area_frac, self.aspect = min_area, max_area_frac, aspect
        self.max_props, self.polarity, self.horizon_margin = max_props, polarity, horizon_margin
        self.name = "thermal-hotspots"

    @staticmethod
    def horizon(gray):
        # Require a row-wide edge; compact objects must not masquerade as a horizon.
        h, w = gray.shape
        if h < 16 or w < 16:
            return None
        smooth = cv2.GaussianBlur(gray.astype(np.float32), (5, 5), 0)
        diff = np.diff(smooth, axis=0)
        strength = np.abs(np.median(diff, axis=1))
        lo, hi = int(h * .15), int(h * .85)
        y = lo + int(np.argmax(strength[lo:hi]))
        if strength[y] < max(2.0, 6 * float(np.median(strength))):
            return None
        return y + 1

    def detect(self, image_bgr):
        if image_bgr.size == 0:
            return []
        gray = image_bgr if image_bgr.ndim == 2 else (image_bgr[..., 0] if image_bgr.shape[2] == 1 else cv2.cvtColor(image_bgr, cv2.COLOR_BGR2GRAY))
        h, w = gray.shape
        scale = min(1.0, self.work_width / w)
        gray = cv2.resize(gray, (max(1, round(w * scale)), max(1, round(h * scale))), interpolation=cv2.INTER_AREA).astype(np.float32)
        hz = self.horizon(gray)
        y0 = max(0, int(hz - self.horizon_margin * gray.shape[0])) if hz is not None else 0
        sea = gray[y0:]
        background = np.median(sea, axis=1, keepdims=True)
        residual = cv2.GaussianBlur(sea - background, (3, 3), 0)
        median = np.median(residual, axis=1, keepdims=True)
        sigma = 1.4826 * np.median(np.abs(residual - median), axis=1, keepdims=True)
        threshold = np.maximum(self.k * sigma, self.min_delta)
        out = []
        for sign in ([1, -1] if self.polarity == "auto" else [1 if self.polarity == "white" else -1]):
            contrast = sign * (residual - median)
            mask = (contrast > threshold).astype(np.uint8)
            mask = cv2.morphologyEx(mask, cv2.MORPH_OPEN, np.ones((2, 2), np.uint8))
            n, labels, stats, _ = cv2.connectedComponentsWithStats(mask, connectivity=8)
            for i in range(1, n):
                x, y, bw, bh, area = stats[i]
                if not self.min_area <= area <= self.max_area_frac * gray.size or min(bw, bh) < 3:
                    continue
                if not self.aspect[0] <= bw / bh <= self.aspect[1]:
                    continue
                ratio = float((contrast / threshold)[labels == i].mean())
                score = float(np.clip(.3 + .3 * (ratio - 1), .3, .9))
                out.append(Detection([float(x / scale), float((y + y0) / scale), float(bw / scale), float(bh / scale)], score, "thermal contact"))
        return sorted(out, key=lambda d: -d.score)[:self.max_props]

    def info(self):
        return {"name": self.name, "work_width": self.work_width, "k": self.k, "min_delta": self.min_delta, "polarity": self.polarity}


class ThermalFusionDetector(AnomalyFusionDetector):
    def __init__(self, base, proposer=None, mode="when_empty", cover_thr=.25, **kwargs):
        if mode not in {"when_empty", "uncovered"}:
            raise ValueError("thermal mode must be when_empty or uncovered")
        super().__init__(base, proposer or ThermalHotspotProposer(), cover_thr=cover_thr, **kwargs)
        self.mode = mode

    def detect(self, image_bgr):
        dets = self.base.detect(image_bgr)
        start = time.perf_counter()
        props = []
        if is_thermal(image_bgr) and (self.mode == "uncovered" or not any(d.score >= self.cover_thr for d in dets)):
            props = [p for p in self.proposer.detect(image_bgr) if not self._covered(p, dets)]
        self.last_timing_ms = dict(getattr(self.base, "last_timing_ms", {}) or {}, thermal=(time.perf_counter() - start) * 1000)
        return dets + props


def configure_thermal(base, config):
    if config is None or config is False:
        return base, .9
    config = {} if config is True else dict(config)
    floor = float(config.pop("unknownness_floor", .9))
    if not 0 <= floor <= 1:
        raise ValueError("thermal unknownness_floor must be in [0, 1]")
    fusion = {k: config.pop(k) for k in ("mode", "cover_thr", "iou_suppress", "pad") if k in config}
    return ThermalFusionDetector(base, ThermalHotspotProposer(**config), **fusion), floor


def thermal_unknown_first(state, floor=.9):
    votes = getattr(state, "label_votes", {})
    label = max(votes, key=votes.get) if votes else state.s.label
    if label == "thermal contact":
        state.s.label = "thermal contact"
        state.s.unknownness = max(state.s.unknownness, floor)
