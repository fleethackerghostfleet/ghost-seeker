"""Replay detections from `dets.jsonl` so tracker/aggregator sweeps don't re-run the model."""
from __future__ import annotations

import json
from collections import defaultdict
from pathlib import Path
from typing import Iterable

import numpy as np

from ..types import Detection


def dets_to_jsonl(path: str | Path, per_frame: Iterable[tuple[int, float, list[Detection]]]) -> None:
    with open(path, "w") as f:
        for frame_idx, t_s, dets in per_frame:
            for d in dets:
                f.write(json.dumps({"frame_idx": int(frame_idx), "t_s": float(t_s), "box": [float(v) for v in d.box],
                                    "score": float(d.score), "label": d.label, "label_id": d.label_id}) + "\n")


def jsonl_to_dets(path: str | Path) -> dict[int, list[Detection]]:
    out: dict[int, list[Detection]] = defaultdict(list)
    with open(path) as f:
        for line in f:
            if not line.strip():
                continue
            r = json.loads(line)
            out[int(r["frame_idx"])].append(Detection(box=r["box"], score=r["score"], label=r["label"], label_id=r.get("label_id")))
    return out


class CachedDetector:
    def __init__(self, path: str | Path, name: str = "cached", min_score: float = 0.0):
        self.by_frame = jsonl_to_dets(path)
        self.name = name
        self.min_score = min_score
        self.frame_idx = 0

    def set_frame(self, frame_idx: int) -> None:
        self.frame_idx = frame_idx

    def detect(self, image_bgr: np.ndarray) -> list[Detection]:
        return [d for d in self.by_frame.get(self.frame_idx, []) if d.score >= self.min_score]
