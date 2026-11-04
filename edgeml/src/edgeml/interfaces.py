from typing import Protocol

import numpy as np

from .types import Detection, Track


class Detector(Protocol):
    name: str

    def detect(self, image_bgr: np.ndarray) -> list[Detection]: ...


class Tracker(Protocol):
    def update(self, dets: list[Detection], image_bgr: np.ndarray, frame_idx: int, t_s: float) -> list[Track]: ...


class Embedder(Protocol):
    dim: int

    def embed_crops(self, crops: list[np.ndarray]) -> np.ndarray: ...

    def embed_text(self, texts: list[str]) -> np.ndarray: ...


class Aggregator(Protocol):
    def update(self, tracks: list[Track], image_bgr: np.ndarray, frame_idx: int, t_s: float) -> list[dict]: ...

    def flush(self, t_s: float) -> list[dict]: ...
