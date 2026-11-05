"""Bake-off wiring and hosted-coordinate regression tests; no weight downloads/API calls."""
import importlib.util
import json
from pathlib import Path

import numpy as np
import pytest

from edgeml.detectors.bakeoff import HostedDetector
from edgeml.detectors.base import BaseDetector


def runner():
    path = Path(__file__).parents[1] / "tools" / "bakeoff.py"
    spec = importlib.util.spec_from_file_location("bakeoff_runner", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_optional_models_register_without_loading_weights():
    from edgeml.detectors.zoo import names

    runner().register_detectors()
    assert {"yolov9t", "yolov10n", "yolo12n", "rfdetr-nano", "owlvit-b32",
            "florence2-base-ft", "gemini-2.5-flash", "cloud-vision"} <= set(names())


def test_openai_architecture_matches_quickgelu_weights():
    from edgeml.embed.clip_embedder import MODELS

    runner().register_classifiers()
    assert MODELS["clip-b16-openai"] == ("ViT-B-16-quickgelu", "openai", 224)


def test_hosted_raw_defaults_to_run_output(tmp_path, monkeypatch):
    import os

    monkeypatch.delenv("BAKEOFF_API_RAW", raising=False)
    runner().configure_raw_dir(["--run-id", "hosted-smoke", "--out", str(tmp_path)])
    assert Path(os.environ["BAKEOFF_API_RAW"]) == tmp_path / "hosted-smoke" / "raw"


def test_hosted_raw_explicit_directory_is_preserved(tmp_path, monkeypatch):
    import os

    monkeypatch.setenv("BAKEOFF_API_RAW", str(tmp_path / "custom"))
    runner().configure_raw_dir(["--run-id", "hosted-smoke"])
    assert Path(os.environ["BAKEOFF_API_RAW"]) == tmp_path / "custom"


class Response:
    status_code = 200

    def __init__(self, payload):
        self.payload = payload

    def json(self):
        return self.payload

    def raise_for_status(self):
        pass


def hosted(name, payload, tmp_path):
    class Session:
        def post(self, url, json, timeout):
            self.payload = json
            return Response(payload)

    det = HostedDetector.__new__(HostedDetector)
    BaseDetector.__init__(det)
    det.name, det.project, det.raw_dir = name, "test-project", tmp_path
    det.session = Session()
    return det


def test_gemini_normalized_yxyx_and_schema(tmp_path):
    payload = {"candidates": [{"finishReason": "STOP", "content": {"parts": [
        {"text": json.dumps([{"label": "boat", "box": [100, 200, 500, 600], "score": 0.9}])}]}}]}
    det = hosted("gemini-2.5-flash", payload, tmp_path)
    ds = det.detect(np.zeros((100, 200, 3), np.uint8))
    assert ds[0].box == [40, 10, 80, 40]
    assert ds[0].label == "boat"
    schema = det.session.payload["generationConfig"]["responseSchema"]
    assert schema["items"]["required"] == ["label", "box", "score"]
    assert len(list(tmp_path.glob("*.json"))) == 1


def test_cloud_vision_normalized_xy_and_native_label(tmp_path):
    payload = {"responses": [{"localizedObjectAnnotations": [{"name": "Boat", "score": 0.8,
        "boundingPoly": {"normalizedVertices": [{"x": 0.2, "y": 0.1}, {"x": 0.6, "y": 0.5}]}}]}]}
    det = hosted("cloud-vision", payload, tmp_path)
    assert det.detect(np.zeros((100, 200, 3), np.uint8))[0].box == [40, 10, 80, 40]


@pytest.mark.parametrize("candidate", [
    {"finishReason": "MAX_TOKENS"},
    {"finishReason": "STOP", "content": {"parts": [{"text": "{}"}]}},
])
def test_invalid_api_output_is_not_scored_as_an_empty_frame(candidate, tmp_path):
    det = hosted("gemini-2.5-pro", {"candidates": [candidate]}, tmp_path)
    with pytest.raises((RuntimeError, ValueError)):
        det.detect(np.zeros((100, 200, 3), np.uint8))


def test_transient_api_status_is_retried_without_changing_request(tmp_path, monkeypatch):
    from unittest.mock import Mock

    limited = Response({"error": "quota"})
    limited.status_code = 429
    det = hosted("cloud-vision", {}, tmp_path)
    det.session.post = Mock(side_effect=[limited, Response({"responses": [{}]})])
    delays = []
    monkeypatch.setattr("time.sleep", delays.append)
    assert det.detect(np.zeros((100, 200, 3), np.uint8)) == []
    assert delays == [2]
    calls = det.session.post.call_args_list
    assert calls[0] == calls[1]
    assert json.loads(next(tmp_path.glob("*.json")).read_text())["attempts"] == 2


def test_expired_access_token_is_refreshed_without_changing_request(tmp_path, monkeypatch):
    from unittest.mock import Mock

    expired = Response({"error": "expired"})
    expired.status_code = 401
    det = hosted("cloud-vision", {}, tmp_path)
    det.session.headers = {"Authorization": "Bearer expired"}
    det.session.post = Mock(side_effect=[expired, Response({"responses": [{}]})])
    token = Mock(return_value="renewed\n")
    monkeypatch.setattr("subprocess.check_output", token)
    assert det.detect(np.zeros((100, 200, 3), np.uint8)) == []
    assert det.session.headers["Authorization"] == "Bearer renewed"
    token.assert_called_once_with(["gcloud", "auth", "print-access-token"], text=True)
    calls = det.session.post.call_args_list
    assert len(calls) == 2 and calls[0] == calls[1]
    assert "renewed" not in next(tmp_path.glob("*.json")).read_text()
