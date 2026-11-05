"""Standard-library-only interfaces for interchangeable image providers.

Providers receive the original uint8 BGR image and must not modify it. Boxes use
original-image pixels in [x, y, width, height] order. Scores must be finite in
[0, 1]; validation never clips them.
"""
from __future__ import annotations

from dataclasses import dataclass, field
import math
from numbers import Real
from typing import Any, Protocol


@dataclass
class ObjectResult:
    """Direct relevance or detections for the prioritizer to weight.

    Both None means not evaluated; detections=[] means evaluated with no hits.
    Direct relevance, including 0, takes precedence in prioritization. Detections
    supplied alongside direct relevance are retained for display only.
    """
    relevance: float | None = None
    detections: list[dict] | None = None


@dataclass
class VisualResult:
    """Visual interest with optional explanatory components and regions."""
    interest: float
    components: dict = field(default_factory=dict)
    regions: list = field(default_factory=list)


class ObjectProvider(Protocol):
    name: str
    def analyze(self, bgr: Any) -> ObjectResult: ...


class VisualProvider(Protocol):
    name: str
    def analyze(self, bgr: Any) -> VisualResult: ...


class QualityProvider(Protocol):
    name: str
    def analyze(self, bgr: Any) -> float: ...


def unit_score(value: Any, field_name: str) -> float:
    """Validate without clipping, then convert a real scalar to a Python float."""
    if isinstance(value, bool) or not isinstance(value, Real):
        raise ValueError(f'{field_name} must be a finite number in [0, 1]')
    if not 0 <= value <= 1:
        raise ValueError(f'{field_name} must be a finite number in [0, 1]')
    value = float(value)
    if not math.isfinite(value) or not 0 <= value <= 1:
        raise ValueError(f'{field_name} must be a finite number in [0, 1]')
    return value


def validate_provider(provider: Any, role: str = 'provider') -> str:
    name = getattr(provider, 'name', None)
    if not isinstance(name, str) or not name.strip():
        raise ValueError(f'{role} must have a nonempty string name')
    if not callable(getattr(provider, 'analyze', None)):
        raise ValueError(f'{role} {name!r} must expose callable analyze(bgr)')
    return name


def _bbox(box: Any, width: int, height: int, field_name: str) -> list[float]:
    if not isinstance(box, (list, tuple)) or len(box) != 4:
        raise ValueError(f'{field_name} must contain [x, y, width, height] in original pixels')
    values = []
    for value in box:
        if isinstance(value, bool) or not isinstance(value, Real):
            raise ValueError(f'{field_name} coordinates must be finite numbers')
        try:
            converted = float(value)
        except OverflowError as exc:
            raise ValueError(f'{field_name} coordinates must be finite numbers') from exc
        if not math.isfinite(converted):
            raise ValueError(f'{field_name} coordinates must be finite numbers')
        values.append(converted)
    x, y, w, h = values
    if x < 0 or y < 0 or w <= 0 or h <= 0 or x + w > width + 1e-9 or y + h > height + 1e-9:
        raise ValueError(f'{field_name} must be positive-area and within original image {width}x{height}')
    return values


def validate_object_result(result: Any, width: int, height: int, name: str) -> ObjectResult:
    if not isinstance(result, ObjectResult):
        raise ValueError(f'{name}: object provider must return ObjectResult')
    relevance = None if result.relevance is None else unit_score(result.relevance, f'{name}.relevance')
    detections = None
    if result.detections is not None:
        if not isinstance(result.detections, list):
            raise ValueError(f'{name}.detections must be a list or None')
        detections = []
        for i, detection in enumerate(result.detections):
            prefix = f'{name}.detections[{i}]'
            if not isinstance(detection, dict):
                raise ValueError(f'{prefix} must be a dictionary')
            label = detection.get('label')
            if not isinstance(label, str) or not label.strip():
                raise ValueError(f'{prefix}.label must be a nonempty string')
            detections.append({
                'label': label,
                'confidence': unit_score(detection.get('confidence'), prefix + '.confidence'),
                'bbox_xywh': _bbox(detection.get('bbox_xywh'), width, height, prefix + '.bbox_xywh'),
            })
    return ObjectResult(relevance=relevance, detections=detections)


def validate_visual_result(result: Any, width: int, height: int, name: str) -> VisualResult:
    if not isinstance(result, VisualResult):
        raise ValueError(f'{name}: visual provider must return VisualResult')
    interest = unit_score(result.interest, f'{name}.interest')
    if not isinstance(result.components, dict):
        raise ValueError(f'{name}.components must be a dictionary')
    components = {}
    reserved = {'visual_interest', 'novelty', 'quality', 'object_relevance'}
    for key, value in result.components.items():
        if not isinstance(key, str) or not key.strip() or key in reserved:
            raise ValueError(f'{name}.components keys must be nonempty, non-reserved signal names')
        components[key] = unit_score(value, f'{name}.components.{key}')
    if not isinstance(result.regions, list):
        raise ValueError(f'{name}.regions must be a list')
    regions = []
    for i, region in enumerate(result.regions):
        prefix = f'{name}.regions[{i}]'
        if not isinstance(region, dict):
            raise ValueError(f'{prefix} must be a dictionary')
        kind = region.get('kind', 'visual_candidate')
        if not isinstance(kind, str) or not kind.strip():
            raise ValueError(f'{prefix}.kind must be a nonempty string')
        regions.append({
            'bbox_xywh': _bbox(region.get('bbox_xywh'), width, height, prefix + '.bbox_xywh'),
            'score': unit_score(region.get('score', interest), prefix + '.score'),
            'kind': kind,
        })
    return VisualResult(interest=interest, components=components, regions=regions)
