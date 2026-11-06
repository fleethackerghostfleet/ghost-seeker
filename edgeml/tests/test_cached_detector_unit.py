import json

import numpy as np
import pytest

from edgeml.detectors.cached import CachedDetector, dets_to_jsonl, jsonl_to_dets
from edgeml.types import Detection


def test_cache_round_trip_and_inclusive_score_filter(tmp_path):
    path = tmp_path / "dets.jsonl"
    dets = [Detection([1, 2, 3, 4], .5, "boat", 7), Detection([5, 6, 7, 8], .49, "bird")]
    rows = iter([(np.int64(1), np.float32(.25), dets), (2, .5, []), (3, 1., [dets[1]])])
    dets_to_jsonl(path, rows)
    wire = [json.loads(line) for line in path.read_text().splitlines()]
    assert len(wire) == 3 and wire[0]["frame_idx"] == 1 and wire[0]["t_s"] == .25
    assert all(type(v) is float for v in wire[0]["box"]) and type(wire[0]["score"]) is float
    assert wire[1]["label_id"] is None
    with path.open("a") as stream:
        stream.write("\n")
    assert jsonl_to_dets(path)[1] == dets
    detector = CachedDetector(path, name="local-cache", min_score=.5)
    assert detector.name == "local-cache" and detector.detect(None) == []
    detector.set_frame(1)
    assert detector.detect(None) == [dets[0]]
    detector.set_frame(2)
    assert detector.detect(None) == []
    detector.set_frame(3)
    assert detector.detect(None) == []
    detector.set_frame(1)
    assert detector.detect(np.zeros((2, 2, 3))) == [dets[0]]


def test_legacy_cache_without_label_id_and_empty_cache(tmp_path):
    path = tmp_path / "cache.jsonl"
    path.write_text(json.dumps({"frame_idx": "2", "box": [0, 0, 1, 1], "score": .7, "label": "boat"}))
    detector = CachedDetector(path)
    detector.set_frame(2)
    assert detector.detect(None)[0].label_id is None
    dets_to_jsonl(path, [(1, 0., [])])
    assert path.read_text() == "" and dict(jsonl_to_dets(path)) == {}
    assert CachedDetector(path).detect(None) == []


@pytest.mark.parametrize("payload,error", [("not JSON", json.JSONDecodeError), ('{"frame_idx":1}', KeyError)])
def test_corrupt_cache_is_not_treated_as_empty(tmp_path, payload, error):
    path = tmp_path / "cache.jsonl"
    path.write_text(payload)
    with pytest.raises(error):
        CachedDetector(path)


def test_missing_cache_is_not_treated_as_empty(tmp_path):
    with pytest.raises(FileNotFoundError):
        CachedDetector(tmp_path / "missing.jsonl")
