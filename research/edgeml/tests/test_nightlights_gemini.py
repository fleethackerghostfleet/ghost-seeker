import importlib.util
import json
from pathlib import Path

import cv2
import numpy as np
import pytest


@pytest.fixture
def comparison(monkeypatch):
    scripts = Path(__file__).resolve().parents[1]/"scripts"
    monkeypatch.syspath_prepend(str(scripts))
    spec = importlib.util.spec_from_file_location("comparison", scripts/"nightlights_gemini.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def response(value):
    return {"candidates": [{"content": {"parts": [{"text": json.dumps(value)}]}}]}


def test_parse_normal_empty_and_invalid(comparison):
    value = dict(lights=[dict(color="red", rel_y=.2)], rule_id="port")
    assert comparison.parse_prediction(response(value), {"port"}) == value
    assert comparison.parse_prediction(response(dict(lights=[], rule_id=None)), {"port"})["lights"] == []
    for bad in [dict(lights=[], rule_id="unknown"), dict(lights=[dict(color="orange", rel_y=.2)], rule_id=None),
                dict(lights=[dict(color="red", rel_y=2)], rule_id=None), dict(lights="red", rule_id=None)]:
        with pytest.raises(ValueError):
            comparison.parse_prediction(response(bad), {"port"})


def test_score_failure_remains_in_denominator(comparison):
    truth = dict(lights=[dict(colour="red")], rule_id="port", unresolvable=False)
    prediction = dict(lights=[dict(color="red", rel_y=.2)], rule_id="port")
    result = comparison.score([dict(truth=truth, gemini=prediction), dict(truth=truth, gemini=None)], "gemini")
    assert result["rule_accuracy"] == .5 and result["light_recall"] == .5
    assert result["light_precision"] == 1
    assert comparison.score([], "gemini")["contacts"] == 0


def test_cache_prevents_repeat_calls_and_cap(comparison, tmp_path, monkeypatch):
    for tier, split in [("hard", "dev"), ("hard", "test2"), ("easy", "test")]:
        directory = tmp_path/"data"/tier/split
        directory.mkdir(parents=True)
        image = np.zeros((80, 100, 3), np.uint8)
        cv2.circle(image, (50, 25), 2, (0, 0, 255), -1)
        cv2.imwrite(str(directory/"frame.jpg"), image)
        record = dict(seed=1, frames=["frame.jpg"]*4, box=[20, 10, 60, 60], lights=[dict(colour="red")], rule_id="port")
        (directory/"labels.json").write_text(json.dumps([record]))
    calls = []
    def fake_request(payload, token):
        calls.append(payload)
        return dict(response=response(dict(lights=[dict(color="red", rel_y=.2)], rule_id="port")), error=None, latency_seconds=.1)
    monkeypatch.setattr(comparison, "request", fake_request)
    monkeypatch.setattr(comparison.subprocess, "check_output", lambda *a, **k: "test-token")
    args = (tmp_path/"data", tmp_path/"cache", tmp_path/"report")
    with pytest.raises(ValueError, match="cap"):
        comparison.compare(*args, max_calls=0)
    comparison.compare(*args, max_calls=3)
    assert len(calls) == 1  # Identical requests across splits share a content-addressed cache.
    comparison.compare(*args, max_calls=0)
    assert len(calls) == 1
    for payload in calls:
        assert len(payload["contents"][0]["parts"]) == 5
        assert payload["generationConfig"]["thinkingConfig"]["thinkingBudget"] == 0
