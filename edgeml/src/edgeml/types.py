from dataclasses import dataclass, field
from typing import Literal

import numpy as np

Box = list[float]  # pixel [x, y, w, h], original image coordinates


@dataclass
class Detection:
    box: Box
    score: float
    label: str
    label_id: int | None = None
    embedding: np.ndarray | None = None


@dataclass
class Track:
    track_id: int
    box: Box
    score: float
    label: str
    frame_idx: int
    t_s: float
    state: Literal["tentative", "confirmed", "lost", "removed"] = "confirmed"
    embedding: np.ndarray | None = None
    extra: dict = field(default_factory=dict)
