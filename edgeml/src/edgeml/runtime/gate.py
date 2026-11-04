"""Frame gate: a cheap stage that decides whether a frame is worth running the detector on.

Signals (all computed on a <=320 px grayscale copy, a few ms on one core):
  * blank      the frame is (nearly) constant, or almost entirely saturated / black
  * blur       variance of the Laplacian, bounded to 0..1 like ``algorithm/ghost_priority/analysis.py``
  * no_change  mean absolute difference and perceptual-hash distance against the *last processed* frame
  * horizon    optional straight-line fit (Canny + Hough) that yields a band ROI around the sea/sky boundary

A frame passes if it is usable (not blank / not blurred) AND changed enough since the last frame that was
passed to the detector. Every decision is returned with the signals so the eval can show what was dropped.
``force_every_s`` guarantees a detector run at least that often so static scenes are never starved.
"""
from __future__ import annotations

import time
from dataclasses import dataclass, field

import cv2
import numpy as np


@dataclass
class GateDecision:
    run_detector: bool
    reason: str
    signals: dict = field(default_factory=dict)
    roi: tuple[int, int, int, int] | None = None  # pixel [x, y, w, h] band around the horizon, if estimated
    cost_ms: float = 0.0


def phash(gray_small: np.ndarray, hash_size: int = 8) -> np.ndarray:
    img = cv2.resize(gray_small, (hash_size * 4, hash_size * 4), interpolation=cv2.INTER_AREA).astype(np.float32)
    dct = cv2.dct(img)[:hash_size, :hash_size]
    med = np.median(dct[1:, 1:]) if hash_size > 1 else dct.mean()
    return (dct > med).ravel()


def estimate_horizon(gray: np.ndarray) -> tuple[float, float] | None:
    """Return (y_at_center, slope) of the dominant near-horizontal line in a small grayscale image, or None."""
    _h, w = gray.shape
    blur = cv2.GaussianBlur(gray, (5, 5), 0)
    edges = cv2.Canny(blur, 40, 120)
    lines = cv2.HoughLinesP(edges, 1, np.pi / 180, threshold=max(20, w // 8), minLineLength=w // 3, maxLineGap=w // 10)
    if lines is None:
        return None
    best, best_len = None, 0.0
    for x1, y1, x2, y2 in lines.reshape(-1, 4):
        if abs(x2 - x1) < 1:
            continue
        slope = (y2 - y1) / (x2 - x1)
        if abs(slope) > 0.35:  # > ~20 degrees is not a horizon
            continue
        length = float(np.hypot(x2 - x1, y2 - y1))
        if length > best_len:
            best_len = length
            yc = y1 + slope * (w / 2 - x1)
            best = (float(yc), float(slope))
    return best


class FrameGate:
    def __init__(self, work_width: int = 320, blur_thr: float = 0.0, blank_std_thr: float = 4.0,
                 dark_frac_thr: float = 0.97, diff_thr: float = 4.0, phash_thr: int = 6, force_every_s: float = 10.0,
                 horizon: bool = True, horizon_band_frac: float = 0.25):
        self.work_width = work_width
        self.blur_thr = blur_thr
        self.blank_std_thr = blank_std_thr
        self.dark_frac_thr = dark_frac_thr
        self.diff_thr = diff_thr
        self.phash_thr = phash_thr
        self.force_every_s = force_every_s
        self.horizon = horizon
        self.horizon_band_frac = horizon_band_frac
        self._last_gray: np.ndarray | None = None
        self._last_hash: np.ndarray | None = None
        self._last_run_t: float | None = None
        self._horizon_ema: tuple[float, float] | None = None

    def _small_gray(self, image_bgr: np.ndarray) -> np.ndarray:
        h, w = image_bgr.shape[:2]
        scale = min(1.0, self.work_width / max(h, w))
        small = cv2.resize(image_bgr, (max(1, round(w * scale)), max(1, round(h * scale))), interpolation=cv2.INTER_AREA) \
            if scale < 1.0 else image_bgr
        return cv2.cvtColor(small, cv2.COLOR_BGR2GRAY)

    def calibrate(self, frames: list[np.ndarray], quantile: float = 0.5, floor: float = 1.5) -> dict:
        """Reducto-style per-clip threshold: set ``diff_thr`` to the given quantile of consecutive-frame MAD over a short
        warm-up (a static shore camera gets a low threshold, a pitching bow camera a high one). Returns the stats used."""
        mads = []
        prev = None
        for im in frames:
            g = self._small_gray(im)
            if prev is not None:
                mads.append(float(np.abs(g.astype(np.int16) - prev).mean()))
            prev = g
        if mads:
            self.diff_thr = max(floor, float(np.quantile(mads, quantile)))
        return {"n": len(mads), "mad_median": float(np.median(mads)) if mads else None, "diff_thr": self.diff_thr}

    def __call__(self, image_bgr: np.ndarray, t_s: float) -> GateDecision:
        t0 = time.perf_counter()
        gray = self._small_gray(image_bgr)
        H, W = image_bgr.shape[:2]
        sig: dict = {}
        std = float(gray.std())
        sig["std"] = std
        sig["dark_frac"] = float((gray < 16).mean())
        sig["bright_frac"] = float((gray > 240).mean())
        lap_var = float(cv2.Laplacian(gray, cv2.CV_32F).var())
        sig["sharpness"] = lap_var / (lap_var + 300.0)

        # conservative: a frame is "blank" only when it is flat AND almost entirely black/saturated (a hazy grey frame
        # with a faint vessel on the horizon has std < 4 but is not dark; SMD MVI_0799 has several of these with GT)
        if std < self.blank_std_thr and (sig["dark_frac"] > self.dark_frac_thr or sig["bright_frac"] > self.dark_frac_thr):
            return self._done(False, "blank", sig, None, t0)
        if self.blur_thr > 0 and sig["sharpness"] < self.blur_thr:  # off by default: SMD dev has GT in frames with sharpness 0.008
            return self._done(False, "blur", sig, None, t0)

        roi = None
        if self.horizon:
            est = estimate_horizon(gray)
            if est is not None:
                if self._horizon_ema is None:
                    self._horizon_ema = est
                else:
                    self._horizon_ema = (0.7 * self._horizon_ema[0] + 0.3 * est[0], 0.7 * self._horizon_ema[1] + 0.3 * est[1])
            if self._horizon_ema is not None:
                yc = self._horizon_ema[0] * (H / gray.shape[0])
                band = self.horizon_band_frac * H
                y0 = int(max(0, yc - band))
                y1 = int(min(H, yc + band))
                roi = (0, y0, W, max(1, y1 - y0))
                sig["horizon_y_frac"] = float(yc / H)
                sig["horizon_slope"] = float(self._horizon_ema[1])

        forced = self._last_run_t is None or (t_s - self._last_run_t) >= self.force_every_s
        if self._last_gray is not None and self._last_gray.shape == gray.shape:
            sig["mad"] = float(np.abs(gray.astype(np.int16) - self._last_gray.astype(np.int16)).mean())
            h = phash(gray)
            sig["phash_dist"] = int((h != self._last_hash).sum())
            unchanged = sig["mad"] < self.diff_thr and sig["phash_dist"] <= self.phash_thr
            if unchanged and not forced:
                return self._done(False, "no_change", sig, roi, t0)
        else:
            h = phash(gray)
        self._last_gray, self._last_hash, self._last_run_t = gray, h, t_s
        return self._done(True, "forced" if forced and sig.get("mad") is not None and sig["mad"] < self.diff_thr else "pass", sig, roi, t0)

    def _done(self, run, reason, sig, roi, t0) -> GateDecision:
        return GateDecision(run_detector=run, reason=reason, signals=sig, roi=roi, cost_ms=(time.perf_counter() - t0) * 1e3)


class AdaptiveRate:
    """Detector cadence controller (literature.md §2.3): never below ``floor_hz`` (default 1 Hz), raised to
    ``max_hz`` while any track is young (< ``young_s`` since first seen), tentative, or within
    ``border_frac`` of the image edge (about to leave / just entered). State is the last emitted track list,
    so it costs nothing per frame."""

    def __init__(self, floor_hz: float = 1.0, max_hz: float = 5.0, young_s: float = 2.0, border_frac: float = 0.05):
        self.floor_hz, self.max_hz, self.young_s, self.border_frac = floor_hz, max_hz, young_s, border_frac
        self.last_det_t: float | None = None
        self._first_seen: dict = {}
        self.hz = floor_hz

    def target_hz(self, tracks, img_wh: tuple[int, int], t_s: float) -> float:
        w, h = img_wh
        live = set()
        fast = False
        for tr in tracks:
            tid = getattr(tr, "track_id", None)
            live.add(tid)
            self._first_seen.setdefault(tid, t_s)
            if getattr(tr, "state", "confirmed") == "tentative" or t_s - self._first_seen[tid] < self.young_s:
                fast = True
            x, y, bw, bh = tr.box
            m = self.border_frac
            if x < m * w or y < m * h or x + bw > (1 - m) * w or y + bh > (1 - m) * h:
                fast = True
        for tid in [k for k in self._first_seen if k not in live]:
            del self._first_seen[tid]
        self.hz = self.max_hz if fast else self.floor_hz
        return self.hz

    def due(self, t_s: float) -> bool:
        if self.last_det_t is None or t_s - self.last_det_t >= 1.0 / self.hz - 1e-6:
            self.last_det_t = t_s
            return True
        return False
