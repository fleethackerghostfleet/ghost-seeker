import importlib.util
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parents[1] / "tools"))
spec = importlib.util.spec_from_file_location("shore_predict", Path(__file__).parents[1] / "tools/shore_predict.py")
predict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(predict)


def test_locked_authorization_rejects_any_changed_weights_or_threshold():
    selection = {"selected": {"threshold_sha256": "t", "weights_sha256": "w"}}
    predict.authorize(selection, "selected", "t", "w")
    with pytest.raises(ValueError):
        predict.authorize(selection, "selected", "different", "w")
    with pytest.raises(ValueError):
        predict.authorize(selection, "selected", "t", "different")


def test_valid_selection_uses_worst_gated_cp_bound_then_sum_then_latency():
    def metrics(a, b):
        return {"strata": {"day_small": {"recall_cp95_lower": a}, "day_medium_large": {"recall_cp95_lower": b}}}
    latency = {"p95_ms": 100}
    assert predict.candidate_key(metrics(.8, .9), latency) > predict.candidate_key(metrics(.7, .99), latency)
    assert predict.candidate_key(metrics(.8, .95), latency) > predict.candidate_key(metrics(.8, .9), latency)
    assert predict.candidate_key(metrics(.8, .9), latency) > predict.candidate_key(metrics(.8, .9), {"p95_ms": 200})
    with pytest.raises(ValueError):
        predict.candidate_key(metrics(None, .9), latency)
