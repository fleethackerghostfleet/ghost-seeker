"""Compare cached appearance descriptors without importing an image runtime.

Scores express heuristic redundancy, not semantic identity or probabilities.
The hybrid requires agreement: its score is the smaller component similarity.
"""
from __future__ import annotations

import math
import re

PHASH_VERSION = 'phash64-dct-v1'
METHODS = ('color-phash', 'color', 'phash')


def fingerprint(record):
    values = record.get('fingerprint')
    if values is None:
        return []
    if not isinstance(values, list):
        raise ValueError('fingerprint must be a flat list')
    if any(isinstance(v, bool) or not isinstance(v, (int, float)) or not math.isfinite(v) for v in values):
        raise ValueError('fingerprint must contain finite numbers')
    return values


def decay(distance, scale):
    scaled = distance / scale
    return 0.0 if not math.isfinite(scaled) or scaled > 30 else math.exp(-(scaled ** 2))


def color_similarity(a, b, scale):
    if not a or len(a) != len(b):
        return 0.0
    return decay(sum(abs(x-y) / len(a) for x, y in zip(a, b)), scale)


class CachedSimilarity:
    """Built-in color, pHash or conservative color+pHash comparison.

    prepare(record) validates and decodes each descriptor once; compare(a, b)
    consumes prepared values. No image reads, model calls, labels or bandwidth.
    """
    def __init__(self, method='color-phash', color_scale=.08, phash_scale=8.0):
        if method not in METHODS:
            raise ValueError(f'similarity_method must be one of {METHODS}')
        for value, label, limit in ((color_scale, 'duplicate_distance', 1),
                                    (phash_scale, 'phash_distance_scale', 64)):
            if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or not 0 < value <= limit:
                raise ValueError(f'{label} must be finite, greater than zero and at most {limit}')
        self.method, self.color_scale, self.phash_scale = method, color_scale, phash_scale
        self.name = method + '-v1'

    def prepare(self, record):
        color = fingerprint(record) if self.method != 'phash' else []
        if self.method == 'color':
            return color, None, None
        value = record.get('phash')
        if value is None:
            raise ValueError(f"{record.get('id', 'record')}: missing pHash; run cache-similarity on this evidence or explicitly use similarity_method='color'")
        if not isinstance(value, dict) or value.get('version') != PHASH_VERSION:
            raise ValueError('Unsupported pHash descriptor version; rebuild with cache-similarity')
        encoded, width = value.get('hex'), value.get('analysis_size')
        if not isinstance(encoded, str) or re.fullmatch(r'[0-9a-fA-F]{16}', encoded) is None:
            raise ValueError('pHash hex must contain exactly 16 hexadecimal digits')
        if isinstance(width, bool) or not isinstance(width, int) or width < 16:
            raise ValueError('pHash analysis_size must be an integer >= 16')
        return color, int(encoded, 16), width

    def compare(self, a, b):
        color_valid = bool(a[0]) and len(a[0]) == len(b[0])
        cs = color_similarity(a[0], b[0], self.color_scale) if self.method != 'phash' else None
        distance = ps = None
        if self.method != 'color':
            if a[2] != b[2]:
                raise ValueError('Incompatible pHash analysis sizes; cache descriptors at the same analysis_size')
            distance = (a[1] ^ b[1]).bit_count()
            ps = decay(distance, self.phash_scale)
        score = cs if self.method == 'color' else ps if self.method == 'phash' else min(cs, ps)
        return {'score': score, 'color_similarity': cs, 'phash_similarity': ps,
                'phash_distance_bits': distance,
                'available': self.method == 'phash' or color_valid}


def engine_from_policy(policy):
    return CachedSimilarity(policy['similarity_method'], policy['duplicate_distance'], policy['phash_distance_scale'])
