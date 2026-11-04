"""Detector adapters (CONTRACTS §2). Every model sits behind the `Detector` protocol."""
from .cached import CachedDetector, dets_to_jsonl, jsonl_to_dets
from .oracle import OracleDetector

__all__ = ["CachedDetector", "OracleDetector", "dets_to_jsonl", "jsonl_to_dets", "make_detector"]


def make_detector(name: str, **kw):
    """Factory: 'yolo11s', 'yolo11m', 'rtdetr-l', ... (ultralytics) or a path to .pt/.onnx."""
    from .ultralytics_det import UltralyticsDetector

    return UltralyticsDetector(name, **kw)
