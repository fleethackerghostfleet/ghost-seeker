from dataclasses import dataclass, field
from typing import Literal, Optional

import numpy as np

Box = list[float]  # pixel [x, y, w, h], original image coordinates


@dataclass
class Detection:
    box: Box
    score: float
    label: str
    label_id: Optional[int] = None
    embedding: Optional[np.ndarray] = None


@dataclass
class Track:
    track_id: int
    box: Box
    score: float
    label: str
    frame_idx: int
    t_s: float
    state: Literal["tentative", "confirmed", "lost", "removed"] = "confirmed"
    embedding: Optional[np.ndarray] = None
    extra: dict = field(default_factory=dict)
