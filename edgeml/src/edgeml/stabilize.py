"""Optional horizon-pinned stabilisation pre-step (literature §2: ablation E2).

Estimates the sea horizon (longest near-horizontal Hough line on a downscaled edge map, EMA-smoothed) and returns a 2x3
affine that rotates/shifts the frame so the horizon stays at the first frame's angle and row. The pipeline applies it to
*detection boxes* (and to the image handed to the tracker, for CMC/ReID), tracks in the pinned frame, and maps tracks back
with the inverse, so GT comparison and sightings stay in native pixel coordinates.
"""
from __future__ import annotations

import math
import time
from typing import Optional

import cv2
import numpy as np

from .types import Box


class HorizonStabilizer:
    def __init__(self, work_width: int = 480, max_tilt_deg: float = 30.0, ema: float = 0.5, min_len_frac: float = 0.25):
        self.work_width, self.max_tilt, self.ema, self.min_len_frac = work_width, max_tilt_deg, ema, min_len_frac
        self.ref: Optional[tuple[float, float]] = None     # (angle_deg, horizon row at image centre) of the first frame
        self.cur: Optional[tuple[float, float]] = None
        self.n_found = 0
        self.n_frames = 0
        self.ms = 0.0

    def detect_horizon(self, image_bgr: np.ndarray) -> Optional[tuple[float, float]]:
        H, W = image_bgr.shape[:2]
        k = self.work_width / W
        g = cv2.cvtColor(cv2.resize(image_bgr, (self.work_width, int(H * k))), cv2.COLOR_BGR2GRAY)
        g = cv2.GaussianBlur(g, (5, 5), 0)
        edges = cv2.Canny(g, 40, 120)
        lines = cv2.HoughLinesP(edges, 1, np.pi / 360, threshold=40, minLineLength=int(self.work_width * self.min_len_frac), maxLineGap=20)
        if lines is None:
            return None
        best, best_len = None, 0.0
        for x1, y1, x2, y2 in lines[:, 0]:
            ang = math.degrees(math.atan2(y2 - y1, x2 - x1))
            if abs(ang) > self.max_tilt:
                continue
            ln = math.hypot(x2 - x1, y2 - y1)
            if ln > best_len:
                best_len, best = ln, (x1, y1, x2, y2, ang)
        if best is None:
            return None
        x1, y1, x2, y2, ang = best
        # row where the line crosses the image centre column, in full-res pixels
        cx = self.work_width / 2
        y_c = y1 + (y2 - y1) * ((cx - x1) / (x2 - x1)) if x2 != x1 else (y1 + y2) / 2
        return float(ang), float(y_c / k)

    def estimate(self, image_bgr: np.ndarray) -> np.ndarray:
        """Return the 2x3 affine mapping native -> pinned coordinates (identity if no horizon has been seen yet)."""
        t0 = time.perf_counter()
        self.n_frames += 1
        h = self.detect_horizon(image_bgr)
        if h is not None:
            self.n_found += 1
            if self.cur is None:
                self.cur = h
            else:
                a = self.ema
                self.cur = (a * h[0] + (1 - a) * self.cur[0], a * h[1] + (1 - a) * self.cur[1])
            if self.ref is None:
                self.ref = self.cur
        self.ms += (time.perf_counter() - t0) * 1000.0
        if self.cur is None or self.ref is None:
            return np.array([[1.0, 0.0, 0.0], [0.0, 1.0, 0.0]])
        H, W = image_bgr.shape[:2]
        d_ang = self.cur[0] - self.ref[0]
        M = cv2.getRotationMatrix2D((W / 2.0, self.cur[1]), d_ang, 1.0)   # un-rotate about the current horizon centre
        M[1, 2] += self.ref[1] - self.cur[1]                               # then shift the horizon to the reference row
        return M

    @staticmethod
    def inverse(M: np.ndarray) -> np.ndarray:
        return cv2.invertAffineTransform(M)

    @staticmethod
    def warp_box(box: Box, M: np.ndarray) -> Box:
        x, y, w, h = box
        pts = np.array([[x, y], [x + w, y], [x, y + h], [x + w, y + h]], dtype=np.float64)
        q = pts @ M[:, :2].T + M[:, 2]
        x0, y0 = q.min(0)
        x1, y1 = q.max(0)
        return [float(x0), float(y0), float(x1 - x0), float(y1 - y0)]

    def stats(self) -> dict:
        return {"frames": self.n_frames, "horizon_found": self.n_found, "ms_per_frame": self.ms / max(1, self.n_frames),
                "ref": self.ref, "last": self.cur}
