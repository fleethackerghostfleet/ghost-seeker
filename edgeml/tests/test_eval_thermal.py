"""Model-free matching checks for the offline evaluation, not detector tuning."""
import importlib.util
from pathlib import Path

import pytest

from edgeml.types import Detection

spec = importlib.util.spec_from_file_location("eval_thermal", Path(__file__).parents[1] / "tools/eval_thermal.py")
evaluation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evaluation)


def test_center_hit_is_symmetric_and_differs_from_iou():
    big, small = [0, 0, 100, 100], [45, 45, 5, 5]
    assert evaluation.center_hit(big, small) and evaluation.center_hit(small, big)
    assert not evaluation.center_hit(big, [101, 101, 2, 2])
    pred = [Detection(small, .9, "thermal contact")]
    assert evaluation.greedy_matches(pred, [big]) == [(0, 0)]
    assert evaluation.greedy_matches(pred, [big], "iou30") == []


def test_score_order_and_one_to_one():
    gt = [[10, 10, 10, 10], [50, 50, 10, 10]]
    pred = [Detection(gt[0], .3, "boat"), Detection(gt[0], .9, "boat"), Detection(gt[1], .5, "boat")]
    assert evaluation.greedy_matches(pred, gt) == [(1, 0), (2, 1)]
    assert evaluation.greedy_matches(pred, gt, "iou50") == [(1, 0), (2, 1)]
    result = evaluation.summarize([(pred, gt)], "center")
    assert result["all"]["precision"] == pytest.approx(2 / 3)
    assert result["all"]["recall"] == 1 and result["all"]["f1"] == .8
    assert result["small"] == result["all"]


def test_empty_error_cases_and_size_boundaries():
    assert evaluation.greedy_matches([], []) == []
    assert evaluation.summarize([], "center")["all"]["f1"] == 0
    assert evaluation.summarize([([], [[0, 0, 10, 10]])], "center")["small"]["fn"] == 1
    assert evaluation.summarize([([Detection([0, 0, 10, 10], .9, "boat")], [])], "center")["small"]["fp"] == 1
    assert evaluation.size_bucket([0, 0, 32, 32]) == "medium"
    assert evaluation.size_bucket([0, 0, 96, 96]) == "large"
    with pytest.raises(ValueError):
        evaluation.greedy_matches([], [], "bad")


@pytest.mark.parametrize("box,target,criterion", [
    ([0, 0, 13, 10], [7, 0, 13, 10], "iou30"),
    ([0, 0, 3, 10], [1, 0, 3, 10], "iou50"),
])
def test_inclusive_iou_thresholds(box, target, criterion):
    pred = [Detection(box, .9, "boat")]
    assert evaluation.greedy_matches(pred, [target], criterion) == [(0, 0)]
    target[0] += .001
    assert evaluation.greedy_matches(pred, [target], criterion) == []
