"""Run interchangeable providers once and cache policy-independent evidence.

Providers receive original uint8 BGR pixels. This layer validates their outputs,
computes color and pHash descriptors, and records provider identities. It does
not interpret operator policies, compare image pairs, or consume annotations.
"""
from __future__ import annotations

import time

import cv2
import numpy as np

from .contracts import (
    ObjectProvider, QualityProvider, VisualProvider, unit_score,
    validate_object_result, validate_provider, validate_visual_result,
)
from .providers import (
    ClassicalVisualInterest, NanoDetObjects, NoObjects, SharpnessExposureQuality,
)
from .similarity_features import describe


class Analyzer:
    """Orchestrate default or injected providers with strict output validation.

    model_path enables built-in NanoDet and cannot accompany object_provider.
    width caps the longest side for default visual/quality providers and the
    fingerprint; injected providers control their own preprocessing.
    threads=0 disables OpenCV parallel regions. Positive limits depend on the
    build's parallel backend; custom providers can use other runtimes.
    """

    def __init__(self, model_path: str | None = None, width: int = 320, threads: int = 0,
                 *, object_provider: ObjectProvider | None = None,
                 visual_provider: VisualProvider | None = None,
                 quality_provider: QualityProvider | None = None):
        if isinstance(width, bool) or not isinstance(width, int) or width < 16:
            raise ValueError('width must be an integer >= 16')
        if isinstance(threads, bool) or not isinstance(threads, int) or threads < 0:
            raise ValueError('threads must be a nonnegative integer (0 disables parallel regions)')
        if model_path is not None and object_provider is not None:
            raise ValueError('model_path and object_provider are mutually exclusive')
        self.width = width
        cv2.setNumThreads(threads)
        cv2.ocl.setUseOpenCL(False)
        self.object_provider = object_provider if object_provider is not None else (
            NanoDetObjects(model_path) if model_path is not None else NoObjects())
        self.visual_provider = visual_provider if visual_provider is not None else ClassicalVisualInterest(width)
        self.quality_provider = quality_provider if quality_provider is not None else SharpnessExposureQuality(width)
        self.provider_names = {
            'object': validate_provider(self.object_provider, 'object provider'),
            'visual': validate_provider(self.visual_provider, 'visual provider'),
            'quality': validate_provider(self.quality_provider, 'quality provider'),
        }

    @staticmethod
    def _call(provider, bgr):
        try:
            return provider.analyze(bgr)
        except Exception as exc:
            raise ValueError(f'{provider.name}: analyze failed: {exc}') from exc

    def analyze(self, bgr: np.ndarray) -> dict:
        """Return JSON-safe cached evidence for one image; annotations are absent."""
        if not isinstance(bgr, np.ndarray) or bgr.dtype != np.uint8:
            raise ValueError('image must be a uint8 numpy array')
        if bgr.ndim != 3 or bgr.shape[2] != 3 or min(bgr.shape[:2]) < 1:
            raise ValueError('image must be a nonempty H x W x 3 BGR array')
        started = time.perf_counter()
        original_h, original_w = bgr.shape[:2]
        usable = bool(np.any(bgr != bgr[0, 0]))
        readonly = bgr.view()
        readonly.flags.writeable = False
        before = time.perf_counter()
        visual = validate_visual_result(self._call(self.visual_provider, readonly), original_w, original_h,
                                        self.provider_names['visual'])
        visual_ms = (time.perf_counter() - before) * 1000
        before = time.perf_counter()
        quality = unit_score(self._call(self.quality_provider, readonly), self.provider_names['quality'] + '.quality')
        quality_ms = (time.perf_counter() - before) * 1000
        before = time.perf_counter()
        fingerprint, phash = describe(bgr, self.width)
        similarity_ms = (time.perf_counter() - before) * 1000
        analysis_ms = (time.perf_counter() - started) * 1000
        before = time.perf_counter()
        objects = validate_object_result(self._call(self.object_provider, readonly), original_w, original_h,
                                         self.provider_names['object'])
        object_ms = (time.perf_counter() - before) * 1000
        if type(self.object_provider) is NoObjects:
            object_ms = 0.0
        return {
            'engine': 'provider-pipeline-v1',
            'providers': dict(self.provider_names),
            'usable': usable,
            'unusable_reason': None if usable else 'constant_image',
            'object_relevance': objects.relevance,
            'detections': objects.detections,
            'signals': {
                **visual.components,
                'visual_interest': visual.interest,
                'novelty': visual.interest,  # Compatibility alias for old caches.
                'quality': quality,
            },
            'regions': visual.regions,
            'fingerprint': fingerprint,
            'phash': phash,
            'timing_ms': {
                'analysis': round(analysis_ms, 4), 'detector': round(object_ms, 4),
                'visual': round(visual_ms, 4), 'quality': round(quality_ms, 4),
                'object': round(object_ms, 4),
                'similarity_features': round(similarity_ms, 4),
            },
        }
