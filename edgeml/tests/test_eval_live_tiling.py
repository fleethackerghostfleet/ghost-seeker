import importlib.util
from pathlib import Path

import numpy as np


spec = importlib.util.spec_from_file_location("eval_live_tiling", Path(__file__).parents[1] / "tools/eval_live_tiling.py")
evaluation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evaluation)


def test_size_slices_match_once_and_ignore_large_objects():
    gt = [{"label": "swimmer", "box": [0, 0, 10, 10]}, {"label": "boat", "box": [100, 100, 60, 60]}]
    predictions = [{"label": "person in water", "score": .8, "box": [0, 0, 10, 10]},
                   {"label": "person in water", "score": .7, "box": [0, 0, 10, 10]},
                   {"label": "boat", "score": .9, "box": [100, 100, 60, 60]},
                   {"label": "boat", "score": .4, "box": [200, 200, 60, 60]}]
    assert np.array_equal(evaluation.counts(gt, predictions), [2, 2, 2])
    assert np.array_equal(evaluation.counts(gt, predictions, size="small"), [1, 1, 1])
    assert np.array_equal(evaluation.counts(gt, predictions, mob=True), [1, 1, 1])
    predictions[0]["label"] = predictions[1]["label"] = "person"
    assert np.array_equal(evaluation.counts(gt, predictions, mob=True), [0, 0, 1])


def test_area_and_strict_width_height_slices_differ():
    gt = [{"label": "boat", "box": [0, 0, 60, 10]}]
    assert np.array_equal(evaluation.counts(gt, [], size="small"), [0, 0, 1])
    assert np.array_equal(evaluation.counts(gt, [], size="lt32"), [0, 0, 0])
    assert evaluation.metric([0, 0, 1]) == {"tp": 0, "fp": 0, "gt": 1, "precision": None, "recall": 0.}
