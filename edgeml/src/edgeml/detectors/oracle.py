"""GT-box oracle detector: separates tracking/aggregation errors from detection errors.

Reads a MOT `gt.txt` (frame,id,x,y,w,h,conf,class,vis) and returns the GT boxes of the current frame.
Optional `dropout` (miss probability), `jitter` (box noise as a fraction of size) and `score_noise`
make it a "noisy oracle" so we can study aggregator robustness without a real detector.
"""
from __future__ import annotations

import numpy as np

from ..types import Detection


class OracleDetector:
    name = "oracle"

    def __init__(self, gt_by_frame: dict[int, list[tuple[int, list[float], int]]], labels: dict | None = None,
                 dropout: float = 0.0, jitter: float = 0.0, score: float = 0.99, seed: int = 0):
        """gt_by_frame: frame_idx(1-based) -> [(obj_id, [x,y,w,h], class_id)]."""
        self.gt = gt_by_frame
        self.labels = {int(k): v for k, v in (labels or {}).items() if str(k).isdigit()}
        self.dropout, self.jitter, self.score = dropout, jitter, score
        self.rng = np.random.default_rng(seed)
        self.frame_idx = 0
        if dropout or jitter:
            self.name = f"oracle-d{dropout}-j{jitter}"

    def set_frame(self, frame_idx: int) -> None:
        self.frame_idx = frame_idx

    def detect(self, image_bgr: np.ndarray) -> list[Detection]:
        out = []
        for _oid, box, cid in self.gt.get(self.frame_idx, []):
            if self.dropout and self.rng.random() < self.dropout:
                continue
            b = list(box)
            if self.jitter:
                b[0] += self.rng.normal(0, self.jitter * b[2])
                b[1] += self.rng.normal(0, self.jitter * b[3])
                b[2] *= float(np.clip(1 + self.rng.normal(0, self.jitter), 0.5, 1.5))
                b[3] *= float(np.clip(1 + self.rng.normal(0, self.jitter), 0.5, 1.5))
            out.append(Detection(box=[float(v) for v in b], score=self.score, label=self.labels.get(cid, "vessel"), label_id=cid))
        return out
