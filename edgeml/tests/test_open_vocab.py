from concurrent.futures import ThreadPoolExecutor
from types import SimpleNamespace

import numpy as np
import pytest

from edgeml.sidecar.open_vocab import (MAX_CROPS, NEGATIVES, OpenVocabulary, TextCache, labels_from_wire,
                                      prompts, regions)


def test_wire_bounds_and_explicit_opt_in():
    assert labels_from_wire(["boat", "buoy"]) == []
    assert labels_from_wire(["open-vocab: Life   Raft", "open-vocab:life raft"]) == ["life raft"]
    with pytest.raises(ValueError):
        labels_from_wire([f"open-vocab:query {i}" for i in range(6)])
    with pytest.raises(ValueError):
        labels_from_wire(["open-vocab:" + "x" * 65])


def test_cache_reuse_eviction_and_concurrency():
    calls = []
    def encode(texts):
        calls.append(texts)
        return np.tile([1., 0.], (len(texts), 1))
    cache = TextCache(encode, capacity=3)
    assert cache([]) == []
    with ThreadPoolExecutor(8) as pool:
        list(pool.map(lambda _: cache(["raft", "raft"]), range(20)))
    assert calls == [["raft"]]
    cache(["a", "b", "c"])
    assert len(cache.cache) == 3 and "raft" not in cache.cache
    cache(["raft"])
    assert calls[-1] == ["raft"]
    for values in ([[0., 0.]], [[float("nan"), 0.]]):
        with pytest.raises(ValueError):
            TextCache(lambda _: values)(["bad"])


def fixture_matcher(features):
    def text(texts):
        return np.array([[1., 0.] if t not in NEGATIVES else [0., 1.] for t in texts])
    class Embedder:
        calls = 0
        def embed_crops(self, crops):
            self.calls += 1
            return np.tile(features, (len(crops), 1))
    embedder = Embedder()
    return OpenVocabulary(embedder, TextCache(text)), embedder


def test_empty_prompts_have_zero_image_or_text_cost_and_grid_can_match_without_yolo():
    model, embedder = fixture_matcher([1., 0.])
    image = np.zeros((100, 200, 3), np.uint8)
    assert model.detect(image, [], []) == [] and embedder.calls == 0
    got = model.detect(image, [], ["life raft"])
    assert len(got) == 1 and got[0]["label"] == "life raft" and got[0]["confidence"] >= .5
    assert got[0]["box"] == {"x": 0., "y": 0., "w": 1., "h": 1.}


def test_negative_margin_rejects_generic_water_even_above_absolute_threshold():
    model, _ = fixture_matcher([.6, .8])
    assert model.detect(np.zeros((30, 50, 3), np.uint8), [], ["oil slick"]) == []
    scores, margins = model.score([[.6, .8]], ["oil slick"])
    assert scores[0, 0] > model.similarity and margins[0, 0] < 0


def test_regions_bounded_clipped_and_keep_coarse_grid():
    image = np.zeros((100, 200, 3), np.uint8)
    detections = [SimpleNamespace(score=.9, box=[-1, -2, 20+i, 30]) for i in range(100)]
    boxes = regions(image, detections)
    assert len(boxes) <= MAX_CROPS + 5
    assert (0, 0, 200, 100) in boxes and (100, 50, 100, 50) in boxes
    assert all(0 <= x < 200 and 0 <= y < 100 and x+w <= 200 and y+h <= 100 for x, y, w, h in boxes)
