"""Common base for detector adapters. Subclasses implement `_infer(image_bgr) -> list[Detection]` with
boxes already in pixel [x, y, w, h] of the input image and `label` set to the model's native label."""
from __future__ import annotations

import time
from typing import Optional

import numpy as np

from ..types import Detection
from .labels import agnostic


class BaseDetector:
    name: str = "base"
    #: static model facts filled by adapters when known
    params_m: Optional[float] = None
    gflops: Optional[float] = None
    imgsz: Optional[int] = None
    training_data: str = "COCO"

    def __init__(self, score_thr: float = 0.05, keep_non_maritime: bool = False, max_det: int = 300):
        self.score_thr = score_thr
        self.keep_non_maritime = keep_non_maritime
        self.max_det = max_det
        self.last_ms: float = float("nan")

    # ---- Detector protocol -------------------------------------------------------------------
    def detect(self, image_bgr: np.ndarray) -> list[Detection]:
        t0 = time.perf_counter()
        dets = self._infer(image_bgr)
        self._sync()
        self.last_ms = (time.perf_counter() - t0) * 1000.0
        out = []
        h, w = image_bgr.shape[:2]
        for d in dets:
            if d.score < self.score_thr:
                continue
            if not self.keep_non_maritime and agnostic(d.label) is None:
                continue
            x, y, bw, bh = d.box
            x0, y0 = max(0.0, x), max(0.0, y)
            x1, y1 = min(float(w), x + bw), min(float(h), y + bh)
            if x1 - x0 < 1 or y1 - y0 < 1:
                continue
            d.box = [x0, y0, x1 - x0, y1 - y0]
            out.append(d)
        out.sort(key=lambda d: -d.score)
        return out[: self.max_det]

    # ---- to implement -----------------------------------------------------------------------
    def _infer(self, image_bgr: np.ndarray) -> list[Detection]:
        raise NotImplementedError

    def _sync(self) -> None:  # GPU adapters override to synchronise before timing
        pass

    def info(self) -> dict:
        return {
            "name": self.name,
            "params_m": self.params_m,
            "gflops": self.gflops,
            "imgsz": self.imgsz,
            "training_data": self.training_data,
            "score_thr": self.score_thr,
        }


def xyxy_to_xywh(b) -> list[float]:
    x0, y0, x1, y1 = [float(v) for v in b]
    return [x0, y0, x1 - x0, y1 - y0]


def letterbox(image_bgr: np.ndarray, size: int, pad_value: int = 114):
    """Resize keeping aspect, pad bottom/right to size x size. Returns (img, scale)."""
    import cv2

    h, w = image_bgr.shape[:2]
    r = min(size / h, size / w)
    nh, nw = int(round(h * r)), int(round(w * r))
    resized = cv2.resize(image_bgr, (nw, nh), interpolation=cv2.INTER_LINEAR)
    canvas = np.full((size, size, 3), pad_value, dtype=np.uint8)
    canvas[:nh, :nw] = resized
    return canvas, r


def nms_xywh(boxes: np.ndarray, scores: np.ndarray, iou_thr: float) -> np.ndarray:
    """Class-agnostic NMS on [x,y,w,h] boxes; returns kept indices (numpy, no torch dependency)."""
    if len(boxes) == 0:
        return np.zeros((0,), dtype=int)
    x1, y1 = boxes[:, 0], boxes[:, 1]
    x2, y2 = boxes[:, 0] + boxes[:, 2], boxes[:, 1] + boxes[:, 3]
    areas = boxes[:, 2] * boxes[:, 3]
    order = scores.argsort()[::-1]
    keep = []
    while order.size:
        i = order[0]
        keep.append(i)
        xx1 = np.maximum(x1[i], x1[order[1:]])
        yy1 = np.maximum(y1[i], y1[order[1:]])
        xx2 = np.minimum(x2[i], x2[order[1:]])
        yy2 = np.minimum(y2[i], y2[order[1:]])
        inter = np.clip(xx2 - xx1, 0, None) * np.clip(yy2 - yy1, 0, None)
        iou = inter / (areas[i] + areas[order[1:]] - inter + 1e-9)
        order = order[1:][iou <= iou_thr]
    return np.array(keep, dtype=int)
