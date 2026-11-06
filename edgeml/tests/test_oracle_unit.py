import copy

import numpy as np
import pytest

from edgeml.detectors.oracle import OracleDetector


def test_oracle_selects_frame_maps_labels_and_detaches_boxes():
    gt = {1: [(7, [10, 20, 30, 40], 2), (8, [1, 2, 3, 4], 99)]}
    before = copy.deepcopy(gt)
    detector = OracleDetector(gt, labels={"2": "tanker", "invalid": "ignored"}, score=.8)
    image = np.zeros((100, 100, 3), dtype=np.uint8)
    assert detector.name == "oracle"
    assert detector.detect(image) == []
    detector.set_frame(1)
    detections = detector.detect(image)
    assert [(d.label, d.label_id, d.score) for d in detections] == [("tanker", 2, .8), ("vessel", 99, .8)]
    assert detections[0].box == [10., 20., 30., 40.]
    assert all(type(value) is float for d in detections for value in d.box)
    detections[0].box[0] = 999
    assert gt == before
    detector.set_frame(9)
    assert detector.detect(image) == []


@pytest.mark.parametrize("dropout,expected", [(0, 20), (1, 0)])
def test_oracle_dropout_endpoints(dropout, expected):
    detector = OracleDetector({1: [(i, [0, 0, 10, 10], 1) for i in range(20)]}, dropout=dropout)
    detector.set_frame(1)
    assert len(detector.detect(np.zeros((20, 20, 3)))) == expected


def test_oracle_randomization_is_seeded_and_does_not_mutate_ground_truth():
    gt = {1: [(i, [10, 20, 30, 40], 1) for i in range(50)]}
    before = copy.deepcopy(gt)
    detectors = [OracleDetector(gt, dropout=.5, jitter=.1, seed=19) for _ in range(2)]
    for detector in detectors:
        detector.set_frame(1)
    left, right = [d.detect(np.zeros((100, 100, 3))) for d in detectors]
    assert 0 < len(left) < 50
    assert [(d.box, d.score) for d in left] == [(d.box, d.score) for d in right]
    assert gt == before
    assert detectors[0].name == "oracle-d0.5-j0.1"


def test_oracle_jitter_clamps_size_and_scales_position_noise():
    detector = OracleDetector({1: [(1, [10, 20, 30, 40], 1)]}, jitter=.1)
    values = iter([2, -3, 100, -100])
    calls = []

    class RNG:
        def normal(self, mean, sigma):
            calls.append((mean, sigma))
            return next(values)

    detector.rng = RNG()
    detector.set_frame(1)
    assert detector.detect(np.zeros((100, 100, 3)))[0].box == [12., 17., 45., 20.]
    assert calls == [(0, 3), (0, 4), (0, .1), (0, .1)]
