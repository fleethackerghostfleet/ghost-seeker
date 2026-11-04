"""Policy-independent CPU image features; scores are heuristics, not probabilities.

No labels or reference annotations are accepted by this module. Novelty here
means spatial visual distinctiveness, not recognition of an unseen category.
Glare, horizons, wakes and camera artifacts can score highly. The compact
fingerprint is only a duplicate-selection hint; it must not be a hard gate for
small objects. ``width`` caps the longest analysis dimension, without upscaling.
"""
from __future__ import annotations

import time
from pathlib import Path

import cv2
import numpy as np

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


def _unit(value: float) -> float:
    return float(np.clip(value, 0.0, 1.0))


def _spectral_map(gray: np.ndarray) -> np.ndarray:
    """Spectral residual map with mean-relative, not per-image min/max scaling.

    The absolute contrast gate in Analyzer prevents almost-flat images from
    being promoted just because this transform finds a relative maximum.
    """
    small = cv2.resize(gray, (64, 64), interpolation=cv2.INTER_AREA)
    spectrum = np.fft.fft2(small)
    log_amp = np.log(np.maximum(np.abs(spectrum), 1e-8))
    residual = log_amp - cv2.blur(log_amp, (3, 3), borderType=cv2.BORDER_REFLECT)
    residual = np.clip(residual, -20.0, 20.0)
    reconstructed = np.fft.ifft2(np.exp(residual + 1j * np.angle(spectrum)))
    energy = cv2.GaussianBlur(np.abs(reconstructed).astype(np.float32) ** 2, (5, 5), 1.0)
    relative = np.maximum(energy / max(float(energy.mean()), 1e-8) - 1.0, 0.0)
    bounded = relative / (relative + 6.0)
    return cv2.resize(bounded, (gray.shape[1], gray.shape[0]), interpolation=cv2.INTER_LINEAR)


class Analyzer:
    """Extract generic image features, optionally adding a fixed COCO detector.

    ``model_path`` must point to OpenCV Zoo's 2022nov FP32 NanoDet ONNX.
    Initial model loading is separate from per-image timing. ``threads=0`` disables
    OpenCV parallel regions. Positive limits depend on the build's parallel
    backend (macOS GCD ignores positive limits). BLAS/FFT settings are separate.
    """

    def __init__(self, model_path: str | None = None, width: int = 320, threads: int = 0):
        if isinstance(width, bool) or not isinstance(width, int) or width < 16:
            raise ValueError('width must be an integer >= 16')
        if isinstance(threads, bool) or not isinstance(threads, int) or threads < 0:
            raise ValueError('threads must be a nonnegative integer (0 disables parallel regions)')
        self.width = width
        cv2.setNumThreads(threads)
        cv2.ocl.setUseOpenCL(False)
        self.detector = None
        if model_path is not None:
            if not Path(model_path).is_file():
                raise FileNotFoundError(model_path)
            from .third_party.nanodet import NanoDet
            self.detector = NanoDet(
                str(model_path), prob_threshold=0.35,
                backend_id=cv2.dnn.DNN_BACKEND_OPENCV,
                target_id=cv2.dnn.DNN_TARGET_CPU,
            )

    def analyze(self, bgr: np.ndarray) -> dict:
        """Return JSON-safe spatial features for one uint8 BGR image."""
        if not isinstance(bgr, np.ndarray) or bgr.dtype != np.uint8:
            raise ValueError('image must be a uint8 numpy array')
        if bgr.ndim != 3 or bgr.shape[2] != 3 or min(bgr.shape[:2]) < 1:
            raise ValueError('image must be a nonempty H x W x 3 BGR array')
        started = time.perf_counter()
        usable = bool(np.any(bgr != bgr[0, 0]))
        original_h, original_w = bgr.shape[:2]
        scale = min(1.0, self.width / max(original_h, original_w))
        w, h = max(1, round(original_w * scale)), max(1, round(original_h * scale))
        small = cv2.resize(bgr, (w, h), interpolation=cv2.INTER_AREA)
        values = small.astype(np.float32) / 255.0
        gray_u8 = cv2.cvtColor(small, cv2.COLOR_BGR2GRAY)
        gray = gray_u8.astype(np.float32) / 255.0
        sigma = max(1.0, min(w, h) / 50.0)
        background = cv2.GaussianBlur(values, (0, 0), sigma)
        # Keep absolute magnitudes: independent images share the same scale.
        residual = np.sqrt(np.mean((values - background) ** 2, axis=2))
        local = cv2.blur(residual, (min(w, 5), min(h, 5)))
        # Smooth bounded response avoids tying textured scenes at a hard 1.0.
        local_map = local / (local + 0.12)
        local_score = _unit(float(local_map.max()))
        spectral = _spectral_map(gray)
        spectral_score = _unit(float(spectral.max()) * local_score)
        combined = np.maximum(local_map * 0.6, spectral * local_score)
        novelty = _unit(max(local_score * 0.6, spectral_score))
        lap_var = float(cv2.Laplacian(gray_u8, cv2.CV_32F).var())
        sharpness = lap_var / (lap_var + 300.0)
        exposure = float(np.mean((gray > 0.02) & (gray < 0.98)))
        quality = _unit(0.7 * sharpness + 0.3 * exposure)
        regions = self._regions(combined, original_w, original_h)
        # Absolute normalized colors retained; not zero-mean or unit-normalized.
        fp = cv2.resize(values, (8, 8), interpolation=cv2.INTER_AREA).reshape(-1)
        analysis_ms = (time.perf_counter() - started) * 1000.0
        detector_started = time.perf_counter()
        detections = self._detect(bgr) if self.detector is not None else None
        detector_ms = (time.perf_counter() - detector_started) * 1000.0 if self.detector is not None else 0.0
        return {
            'engine': 'classical-v1' if self.detector is None else 'classical-v1+nanodet-zoo-416',
            'usable': usable,
            'unusable_reason': None if usable else 'constant_image',
            'signals': {
                'local_contrast': local_score,
                'spectral_saliency': spectral_score,
                'quality': quality,
                'novelty': novelty,
            },
            'regions': regions,
            'detections': detections,
            'fingerprint': [round(float(x), 5) for x in fp],
            'timing_ms': {'analysis': round(analysis_ms, 4), 'detector': round(detector_ms, 4)},
        }

    @staticmethod
    def _regions(combined: np.ndarray, original_w: int, original_h: int) -> list[dict]:
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
            regions.append({
                'bbox_xywh': [x1, y1, max(1, x2 - x1), max(1, y2 - y1)],
                'score': _unit(float(combined[labels == label].max())),
                'kind': 'visual_candidate',
            })
        return sorted(regions, key=lambda item: item['score'], reverse=True)[:5]

    def _detect(self, bgr: np.ndarray) -> list[dict]:
        h, w = bgr.shape[:2]
        scale = min(416.0 / w, 416.0 / h)
        nw, nh = max(1, round(w * scale)), max(1, round(h * scale))
        resized = cv2.resize(bgr, (nw, nh), interpolation=cv2.INTER_LINEAR)
        left, top = (416 - nw) // 2, (416 - nh) // 2
        letterbox = np.zeros((416, 416, 3), dtype=np.uint8)
        letterbox[top:top + nh, left:left + nw] = resized
        raw = self.detector.infer(letterbox)
        detections = []
        for row in raw:
            if not np.isfinite(row).all():
                continue
            x1, y1, x2, y2, confidence, cls = (float(v) for v in row)
            x1, x2 = np.clip([(x1 - left) * w / nw, (x2 - left) * w / nw], 0, w)
            y1, y2 = np.clip([(y1 - top) * h / nh, (y2 - top) * h / nh], 0, h)
            cls = int(cls)
            if x2 <= x1 or y2 <= y1 or not 0 <= cls < len(COCO_CLASSES):
                continue
            detections.append({
                'label': COCO_CLASSES[cls],
                'confidence': _unit(confidence),
                'bbox_xywh': [round(float(v), 3) for v in (x1, y1, x2 - x1, y2 - y1)],
            })
        return detections
