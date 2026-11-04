"""Detector adapters (CONTRACTS §2). Every model sits behind the `Detector` protocol.

Workstream C: `UltralyticsDetector` (via `make_detector`), `OracleDetector`, `CachedDetector`.
Workstream B: the zero-shot zoo (`build(name)` / `names()`), tiling wrappers and label mapping.
"""
from .base import BaseDetector
from .cached import CachedDetector, dets_to_jsonl, jsonl_to_dets
from .labels import MARITIME_PROMPTS, agnostic
from .oracle import OracleDetector
from .tiled import HorizonBandDetector, TiledDetector, estimate_horizon_y
from .zoo import EXCLUDE, TIER, build, names

__all__ = [
    "EXCLUDE",
    "MARITIME_PROMPTS",
    "TIER",
    "BaseDetector",
    "CachedDetector",
    "HorizonBandDetector",
    "OracleDetector",
    "TiledDetector",
    "agnostic",
    "build",
    "dets_to_jsonl",
    "estimate_horizon_y",
    "jsonl_to_dets",
    "make_detector",
    "names",
]


def make_detector(name: str, **kw):
    """Factory: 'yolo11s', 'yolo11m', 'rtdetr-l', ... (ultralytics) or a path to .pt/.onnx."""
    from .ultralytics_det import UltralyticsDetector

    return UltralyticsDetector(name, **kw)
