"""The sidecar in --mock mode: Connect/JSON wire format and the per-source sighting lifecycle the Go edge consumes."""
import base64
import json
import urllib.error
import urllib.request

import cv2
import numpy as np
import pytest

from edgeml.sidecar import MockEngine
from edgeml.sidecar.server import start_in_thread
from edgeml.sidecar.service import InferenceService, MockSchedule, RpcError


def _jpeg(seed=0, w=160, h=120):
    img = np.random.default_rng(seed).integers(0, 255, (h, w, 3), dtype=np.uint8)
    ok, buf = cv2.imencode(".jpg", img)
    assert ok
    return buf.tobytes()


def _call(base, method, body, ctype="application/json"):
    req = urllib.request.Request(f"{base}/ghostseeker.v1.InferenceService/{method}", data=json.dumps(body).encode(),
                                 headers={"Content-Type": ctype}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            return r.status, json.loads(r.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read())


@pytest.fixture(scope="module")
def base():
    srv, url = start_in_thread(MockEngine(MockSchedule(open_at=2, key_at=4, close_at=6)))
    yield url
    srv.shutdown()


def test_capabilities_and_embed_text(base):
    st, cap = _call(base, "Capabilities", {})
    assert st == 200 and cap["modelId"] == "edgeml-mock-v1" and cap["embeddingDim"] == 64 and cap["openVocabulary"] is True
    st, emb = _call(base, "EmbedText", {"texts": ["sailboat", "red sailboat", "tanker"]})
    assert st == 200 and len(emb["embeddings"]) == 3
    a, b, c = (np.asarray(e["values"], np.float32) for e in emb["embeddings"])
    assert a.shape == (64,) and abs(np.linalg.norm(a) - 1) < 1e-3
    assert a @ b > a @ c  # shared word => closer, like the Go mock


def test_sighting_lifecycle_per_source(base):
    img = base64.b64encode(_jpeg()).decode()
    events = []
    for i in range(1, 8):
        st, resp = _call(base, "Analyze", {"image": img, "knownLabels": ["boat"], "wantEmbedding": True, "source": "cam-a",
                                           "capturedAtUnixMs": str(1_700_000_000_000 + i * 200)})
        assert st == 200 and resp["modelId"] == "edgeml-mock-v1"
        events.append([(s["event"], s["isKeyFrame"], s["id"]) for s in resp.get("sightings", [])])
    assert events[0] == []  # before open_at: stateless response, Go falls back to hash mode
    assert [e[0][0] for e in events[1:6]] == ["EVENT_OPEN", "EVENT_DUPLICATE", "EVENT_UPDATE", "EVENT_DUPLICATE", "EVENT_CLOSE"]
    assert events[1][0][1] is True and events[3][0][1] is True and events[2][0][1] is False
    assert len({e[0][2] for e in events[1:6]}) == 1  # one object, one id
    assert events[6][0] == ("EVENT_OPEN", True, "cam-a-mock-2")  # the cycle restarts with a new id
    # another source has its own state and starts from frame 1
    st, resp = _call(base, "Analyze", {"image": img, "source": "cam-b"})
    assert st == 200 and resp["sightings"] == []
    st, resp = _call(base, "Analyze", {"image": img, "source": "cam-b"})
    assert resp["sightings"][0]["event"] == "EVENT_OPEN" and resp["sightings"][0]["id"].startswith("cam-b-")
    s = resp["sightings"][0]
    assert {f["kind"] for f in s["forms"]} == {"FORM_KIND_FACT", "FORM_KIND_CROP", "FORM_KIND_THUMB", "FORM_KIND_FULL"}
    assert s["keyDetection"]["box"] == {"x": 0.3, "y": 0.3, "w": 0.4, "h": 0.4} and isinstance(s["firstSeenUnixMs"], str)


def test_snake_case_request_and_errors(base):
    st, resp = _call(base, "Analyze", {"image": base64.b64encode(_jpeg(1)).decode(), "source": "cam-c", "known_labels": ["x"], "want_embedding": True})
    assert st == 200 and "detections" in resp
    st, err = _call(base, "Analyze", {"source": "cam-c"})
    assert st == 400 and err["code"] == "invalid_argument"
    st, err = _call(base, "Analyze", {"image": base64.b64encode(b"not an image").decode()})
    assert st == 400 and err["code"] == "invalid_argument"
    st, err = _call(base, "Nope", {})
    assert st == 404 and err["code"] == "unimplemented"
    st, err = _call(base, "Analyze", {}, ctype="application/proto")
    assert st == 415


def test_body_limit_and_loopback_default(base):
    from edgeml.sidecar.server import MAX_BODY_BYTES, parse_listen

    assert parse_listen(":9090") == ("127.0.0.1", 9090) and parse_listen("0.0.0.0:1") == ("0.0.0.0", 1)
    req = urllib.request.Request(f"{base}/ghostseeker.v1.InferenceService/Analyze", data=b"{}",
                                 headers={"Content-Type": "application/json", "Content-Length": str(MAX_BODY_BYTES + 1)}, method="POST")
    with pytest.raises(urllib.error.HTTPError) as ei:
        urllib.request.urlopen(req, timeout=10)
    assert ei.value.code == 413 and json.loads(ei.value.read())["code"] == "resource_exhausted"


def test_open_carries_embedding(base):
    img = base64.b64encode(_jpeg(2)).decode()
    _call(base, "Analyze", {"image": img, "source": "cam-e"})
    st, resp = _call(base, "Analyze", {"image": img, "source": "cam-e", "wantEmbedding": True})
    s = resp["sightings"][0]
    assert s["event"] == "EVENT_OPEN" and len(s["embedding"]) == 64 and s["unknownness"] == 0.0


def test_service_dispatch_without_http():
    svc = InferenceService(MockEngine())
    with pytest.raises(RpcError) as ei:
        svc.dispatch("dispatch", {})
    assert ei.value.code == "unimplemented"
    assert svc.dispatch("Capabilities", {})["embeddingDim"] == 64
