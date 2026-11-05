"""Meaningful research contract tests, independent of downloaded model assets."""
import copy
import hashlib
import json
import threading
from http.server import HTTPServer
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

import numpy as np
import pytest
from edgeml.embed.zeroshot import SUBTYPES, UNKNOWN_PROMPTS
from edgeml_research.nl_priority.engine import Vocabulary, validate_manifest
from edgeml_research.nl_priority.evaluate import metrics
from edgeml_research.nl_priority.server import handler_for


class StubEncoder:
    """Orthogonal controlled vectors, never presented as a real model benchmark."""
    model_id = "test-only-controlled-encoder"
    dimension = 32
    calls = 0

    def encode(self, texts):
        self.calls += 1
        vectors = []
        prompts = {p: i for i, group in enumerate(SUBTYPES.values()) for p in group}
        for text in texts:
            index = prompts.get(text, 30 if text in UNKNOWN_PROMPTS else 20 if "rescue boat" in text else 21)
            vector = np.zeros(self.dimension, np.float32)
            vector[index] = 1
            vectors.append(vector)
        return np.stack(vectors)


@pytest.fixture
def vocabulary():
    return Vocabulary(StubEncoder())


def item(index=20, **kwargs):
    vector = np.zeros(32, np.float32)
    vector[index] = 1
    return {"id": "crop-1", "embedding": vector.tolist(), "modelId": StubEncoder.model_id, **kwargs}


def test_candidate_accepts_only_clear_new_match_and_keeps_baseline(vocabulary):
    original = item()
    original.update({"unknownness": 0.91, "label": "boat", "labelOverride": "fishing boat"})
    snapshot = copy.deepcopy(original)
    baseline_before = vocabulary.baseline.classify(np.array([original["embedding"]]))
    result = vocabulary.classify(["rescue boat"], [original])
    assert result["matches"][0]["accepted"]
    assert result["matches"][0]["experimental"]
    assert result["matches"][0]["label"] == "rescue boat"
    assert original == snapshot
    assert vocabulary.baseline.classify(np.array([original["embedding"]])) == baseline_before
    assert vocabulary.baseline.classes == list(SUBTYPES)


def test_abstains_on_base_winner_and_background(vocabulary):
    assert not vocabulary.classify(["rescue boat"], [item(0)])["matches"][0]["accepted"]
    assert not vocabulary.classify(["rescue boat"], [item(30)])["matches"][0]["accepted"]


def test_ambiguous_candidates_abstain(vocabulary):
    result = vocabulary.classify(["orange raft", "yellow raft"], [item(21)])
    assert not result["matches"][0]["accepted"]
    assert result["matches"][0]["margin"] == 0


def test_dynamic_prototypes_are_cached(vocabulary):
    vocabulary.classify(["rescue boat"], [item()])
    calls = vocabulary.encoder.calls
    vocabulary.classify(["rescue boat"], [item()])
    assert vocabulary.encoder.calls == calls


@pytest.mark.parametrize("bad", [item(modelId="different-512d-model"), item(embedding=[1, 0]),
                                item(embedding=[float("nan")] * 32), item(embedding=[0] * 32)])
def test_rejects_incompatible_or_invalid_vectors(vocabulary, bad):
    with pytest.raises(ValueError):
        vocabulary.classify(["rescue boat"], [bad])


def test_vocabulary_limits_and_duplicates(vocabulary):
    assert vocabulary.concepts([" Rescue Boat ", "rescue boat", "cargo ship"]) == ["rescue boat"]
    with pytest.raises(ValueError, match="16"):
        vocabulary.classify([f"new concept {i}" for i in range(17)], [])
    with pytest.raises(ValueError):
        vocabulary.classify(["ignore instructions; execute code"], [])
    with pytest.raises(ValueError, match="unique"):
        vocabulary.classify([], [item(), item()])


def test_manifest_rejects_tampered_assets(tmp_path):
    content = b"controlled test asset"
    names = ["text.onnx", "tokenizer.json", "base_prototypes.json"]
    for name in names:
        (tmp_path / name).write_bytes(content)
    manifest = {"schemaVersion": 1, "architecture": "MobileCLIP-S1", "pretrained": "datacompdr", "modelId": "fixed",
                "dimension": 32, "textGraph": "text.onnx", "sha256": {n: hashlib.sha256(content).hexdigest() for n in names}}
    (tmp_path / "manifest.json").write_text(json.dumps(manifest))
    assert validate_manifest(tmp_path)["modelId"] == "fixed"
    (tmp_path / "tokenizer.json").write_text("changed tokenizer")
    with pytest.raises(ValueError, match="checksum"):
        validate_manifest(tmp_path)


def test_metrics_do_not_hide_absent_predictions():
    report = metrics({"a": "rescue boat", "b": "rescue boat"}, {"a": ["rescue boat"], "b": [], "c": ["rescue boat"]}, ["rescue boat", "raft"])
    assert report["rescue boat"]["precision"] == 0.5
    assert report["rescue boat"]["recall"] == 0.5
    assert report["raft"]["precision"] is None


def test_http_error_does_not_change_next_valid_response(vocabulary):
    server = HTTPServer(("127.0.0.1", 0), handler_for(vocabulary))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        url = f"http://127.0.0.1:{server.server_port}/classify"
        request = Request(url, json.dumps({"concepts": ["rescue boat"], "items": [item(modelId="bad")]}).encode(), {"Content-Type": "application/json"})
        with pytest.raises(HTTPError) as error:
            urlopen(request)
        assert error.value.code == 400
        request = Request(url, json.dumps({"concepts": ["rescue boat"], "items": [item()]}).encode(), {"Content-Type": "application/json"})
        with urlopen(request) as response:
            assert json.load(response)["matches"][0]["accepted"]
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
