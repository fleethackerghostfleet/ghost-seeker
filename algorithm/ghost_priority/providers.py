"""Default CPU providers and explicit ``module:factory`` plugin loading.

Importing a requested module executes its Python code. Only load trusted modules
explicitly chosen by the caller; there is no automatic plugin discovery.
"""
from __future__ import annotations

import importlib
from pathlib import Path

import cv2
import numpy as np

from .contracts import ObjectResult, VisualResult, validate_provider

COCO_CLASSES = (
    'person', 'bicycle', 'car', 'motorcycle', 'airplane', 'bus', 'train', 'truck',
    'boat', 'traffic light', 'fire hydrant', 'stop sign', 'parking meter', 'bench',
    'bird', 'cat', 'dog', 'horse', 'sheep', 'cow', 'elephant', 'bear', 'zebra',
    'giraffe', 'backpack', 'umbrella', 'handbag', 'tie', 'suitcase', 'frisbee',
    'skis', 'snowboard', 'sports ball', 'kite', 'baseball bat', 'baseball glove',
    'skateboard', 'surfboard', 'tennis racket', 'bottle', 'wine glass', 'cup',
    'fork', 'knife', 'spoon', 'bowl', 'banana', 'apple', 'sandwich', 'orange',
    'broccoli', 'carrot', 'hot dog', 'pizza', 'donut', 'cake', 'chair', 'couch',
    'potted plant', 'bed', 'dining table', 'toilet', 'tv', 'laptop', 'mouse',
    'remote', 'keyboard', 'cell phone', 'microwave', 'oven', 'toaster', 'sink',
    'refrigerator', 'book', 'clock', 'vase', 'scissors', 'teddy bear',
    'hair drier', 'toothbrush',
)


def load_provider(spec: str):
    """Import an explicit module and invoke its no-argument provider factory."""
    if not isinstance(spec, str) or spec.count(':') != 1:
        raise ValueError('Provider spec must be module:factory')
    module_name, factory_name = spec.split(':')
    if not module_name or not all(part.isidentifier() for part in module_name.split('.')) or not factory_name.isidentifier():
        raise ValueError('Provider spec must be a dotted module name and a factory identifier')
    try:
        module = importlib.import_module(module_name)
        factory = getattr(module, factory_name)
    except (ImportError, AttributeError) as exc:
        raise ValueError(f'Cannot load provider {spec!r}: {exc}') from exc
    if not callable(factory):
        raise ValueError(f'Provider factory {spec!r} is not callable')
    try:
        provider = factory()
    except Exception as exc:
        raise ValueError(f'Provider factory {spec!r} failed: {exc}') from exc
    validate_provider(provider, spec)
    return provider


def resized_bgr(bgr: np.ndarray, width: int) -> np.ndarray:
    """Longest-side cap without upscaling; shared formula across defaults."""
    if isinstance(width, bool) or not isinstance(width, int) or width < 16:
        raise ValueError('width must be an integer >= 16')
    h, w = bgr.shape[:2]
    scale = min(1.0, width / max(h, w))
    return cv2.resize(bgr, (max(1, round(w * scale)), max(1, round(h * scale))), interpolation=cv2.INTER_AREA)


def _unit(value):
    # Numeric guard for built-in mathematical operations, never plugin outputs.
    return float(np.clip(value, 0.0, 1.0))


def _spectral_map(gray):
    small = cv2.resize(gray, (64, 64), interpolation=cv2.INTER_AREA)
    spectrum = np.fft.fft2(small)
    log_amp = np.log(np.maximum(np.abs(spectrum), 1e-8))
    residual = log_amp - cv2.blur(log_amp, (3, 3), borderType=cv2.BORDER_REFLECT)
    reconstructed = np.fft.ifft2(np.exp(np.clip(residual, -20.0, 20.0) + 1j * np.angle(spectrum)))
    energy = cv2.GaussianBlur(np.abs(reconstructed).astype(np.float32) ** 2, (5, 5), 1.0)
    relative = np.maximum(energy / max(float(energy.mean()), 1e-8) - 1.0, 0.0)
    bounded = relative / (relative + 6.0)
    return cv2.resize(bounded, (gray.shape[1], gray.shape[0]), interpolation=cv2.INTER_LINEAR)


def _regions(combined, original_w, original_h):
    mask = (combined >= 0.30).astype(np.uint8)
    count, labels, stats, _ = cv2.connectedComponentsWithStats(mask, connectivity=8)
    h, w = combined.shape
    regions = []
    for label in range(1, count):
        x, y, bw, bh, area = (int(v) for v in stats[label])
        if area < 3:
            continue
        x1, y1 = int(np.floor(x * original_w / w)), int(np.floor(y * original_h / h))
        x2 = min(original_w, int(np.ceil((x + bw) * original_w / w)))
        y2 = min(original_h, int(np.ceil((y + bh) * original_h / h)))
        regions.append({'bbox_xywh': [x1, y1, max(1, x2 - x1), max(1, y2 - y1)],
                        'score': _unit(float(combined[labels == label].max())),
                        'kind': 'visual_candidate'})
    return sorted(regions, key=lambda item: item['score'], reverse=True)[:5]


class ClassicalVisualInterest:
    """Spatial distinctiveness only, not learned unknown-category recognition."""
    name = 'classical-visual-v1'

    def __init__(self, width: int = 320):
        if isinstance(width, bool) or not isinstance(width, int) or width < 16:
            raise ValueError('width must be an integer >= 16')
        self.width = width

    def analyze(self, bgr) -> VisualResult:
        original_h, original_w = bgr.shape[:2]
        small = resized_bgr(bgr, self.width)
        h, w = small.shape[:2]
        values = small.astype(np.float32) / 255.0
        gray = cv2.cvtColor(small, cv2.COLOR_BGR2GRAY).astype(np.float32) / 255.0
        background = cv2.GaussianBlur(values, (0, 0), max(1.0, min(w, h) / 50.0))
        residual = np.sqrt(np.mean((values - background) ** 2, axis=2))
        local = cv2.blur(residual, (min(w, 5), min(h, 5)))
        local_map = local / (local + 0.12)
        local_score = _unit(float(local_map.max()))
        spectral = _spectral_map(gray)
        spectral_score = _unit(float(spectral.max()) * local_score)
        combined = np.maximum(local_map * 0.6, spectral * local_score)
        return VisualResult(
            interest=_unit(max(local_score * 0.6, spectral_score)),
            components={'local_contrast': local_score, 'spectral_saliency': spectral_score},
            regions=_regions(combined, original_w, original_h),
        )


class SharpnessExposureQuality:
    name = 'sharpness-exposure-v1'

    def __init__(self, width: int = 320):
        if isinstance(width, bool) or not isinstance(width, int) or width < 16:
            raise ValueError('width must be an integer >= 16')
        self.width = width

    def analyze(self, bgr) -> float:
        gray_u8 = cv2.cvtColor(resized_bgr(bgr, self.width), cv2.COLOR_BGR2GRAY)
        gray = gray_u8.astype(np.float32) / 255.0
        lap_var = float(cv2.Laplacian(gray_u8, cv2.CV_32F).var())
        sharpness = lap_var / (lap_var + 300.0)
        exposure = float(np.mean((gray > 0.02) & (gray < 0.98)))
        return _unit(0.7 * sharpness + 0.3 * exposure)


class NoObjects:
    name = 'objects-disabled'

    def analyze(self, bgr) -> ObjectResult:
        return ObjectResult()


class NanoDetObjects:
    """Adapter for the exact OpenCV Zoo 2022nov FP32 416-pixel ONNX model."""
    name = 'nanodet-zoo-2022nov-416'

    def __init__(self, model_path: str):
        if not Path(model_path).is_file():
            raise FileNotFoundError(model_path)
        from .third_party.nanodet import NanoDet
        self.detector = NanoDet(str(model_path), prob_threshold=0.35,
                               backend_id=cv2.dnn.DNN_BACKEND_OPENCV,
                               target_id=cv2.dnn.DNN_TARGET_CPU)

    def analyze(self, bgr) -> ObjectResult:
        h, w = bgr.shape[:2]
        scale = min(416.0 / w, 416.0 / h)
        nw, nh = max(1, round(w * scale)), max(1, round(h * scale))
        resized = cv2.resize(bgr, (nw, nh), interpolation=cv2.INTER_LINEAR)
        left, top = (416 - nw) // 2, (416 - nh) // 2
        letterbox = np.zeros((416, 416, 3), dtype=np.uint8)
        letterbox[top:top + nh, left:left + nw] = resized
        detections = []
        for row in self.detector.infer(letterbox):
            if len(row) != 6 or not np.isfinite(row).all():
                raise ValueError('NanoDet returned a malformed or nonfinite detection')
            x1, y1, x2, y2, confidence, cls = (float(v) for v in row)
            if not cls.is_integer() or not 0 <= cls < len(COCO_CLASSES):
                raise ValueError('NanoDet returned an invalid COCO class index')
            # Clip model-space padding to the original image as part of the
            # adapter's coordinate conversion, before validating its result.
            x1, x2 = np.clip([(x1 - left) * w / nw, (x2 - left) * w / nw], 0, w)
            y1, y2 = np.clip([(y1 - top) * h / nh, (y2 - top) * h / nh], 0, h)
            if x2 <= x1 or y2 <= y1:
                continue
            detections.append({'label': COCO_CLASSES[int(cls)], 'confidence': confidence,
                               'bbox_xywh': [float(v) for v in (x1, y1, x2 - x1, y2 - y1)]})
        return ObjectResult(detections=detections)
