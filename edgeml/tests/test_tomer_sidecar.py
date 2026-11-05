"""Sidecar --prioritizer tomer|both: algorithm/ghost_priority scored through InferenceService."""
import base64
import json
import urllib.request

import numpy as np
import pytest

cv2 = pytest.importorskip("cv2")

from edgeml.sidecar import MockEngine  # noqa: E402
from edgeml.sidecar.server import start_in_thread  # noqa: E402
from edgeml.sidecar.service import InferenceService, NoopEngine  # noqa: E402

try:
    from edgeml.sidecar.tomer import TomerPrioritizer
    TomerPrioritizer  # importable; ghost_priority itself is imported lazily below
    import edgeml.sidecar.tomer as tomer_mod
    HAVE_GP = tomer_mod.find_algorithm_dir() is not None
except ImportError:
    HAVE_GP = False

needs_gp = pytest.mark.skipif(not HAVE_GP, reason="algorithm/ghost_priority not checked out")


def _jpeg(img):
    ok, buf = cv2.imencode(".jpg", img)
    assert ok
    return base64.b64encode(buf.tobytes()).decode()


def _frames():
    rng = np.random.default_rng(1)
    a = (rng.random((240, 320, 3)) * 255).astype(np.uint8)
    cv2.rectangle(a, (60, 60), (200, 160), (255, 255, 255), -1)
    b = np.full((240, 320, 3), 40, np.uint8)
    cv2.circle(b, (160, 120), 50, (200, 200, 200), -1)
    return a, b


@needs_gp
def test_tomer_mode_scores_and_flags_duplicates():
    svc = InferenceService(NoopEngine(), prioritizer=TomerPrioritizer(), mode="tomer")
    a, b = _frames()
    r1 = svc.Analyze({"image": _jpeg(a), "source": "cam"})
    assert r1["detections"] == [] and r1["modelId"] == "none"
    ip1 = r1["imagePriority"]
    assert ip1["engine"].startswith("ghost_priority/") and 0 < ip1["score"] <= 1
    assert ip1["score"] == ip1["baseScore"] and ip1["nearDuplicate"] is False and ip1["frameId"]
    r2 = svc.Analyze({"image": _jpeg(a), "source": "cam"})["imagePriority"]
    assert r2["nearDuplicate"] is True and r2["nearestPriorId"] == ip1["frameId"]
    assert r2["maxSimilarity"] > 0.9 and r2["score"] < ip1["score"] * 0.3  # 1 - 0.85 * sim
    r3 = svc.Analyze({"image": _jpeg(b), "source": "cam"})["imagePriority"]
    assert r3["nearDuplicate"] is False and r3["maxSimilarity"] < 0.5
    # a different source has its own window
    r4 = svc.Analyze({"image": _jpeg(a), "source": "other"})["imagePriority"]
    assert r4["nearDuplicate"] is False
    assert svc.Capabilities({})["prioritizer"] == "tomer"


@needs_gp
def test_both_mode_keeps_engine_output_and_adds_priority_over_http():
    srv, base = start_in_thread(MockEngine(), prioritizer=TomerPrioritizer(), mode="both")
    try:
        a, _ = _frames()
        body = json.dumps({"image": _jpeg(a), "source": "cam"}).encode()
        req = urllib.request.Request(f"{base}/ghostseeker.v1.InferenceService/Analyze", data=body,
                                     headers={"Content-Type": "application/json"})
        resp = json.load(urllib.request.urlopen(req))
        assert resp["modelId"] == MockEngine().model_id
        assert "imagePriority" in resp and resp["imagePriority"]["score"] > 0
    finally:
        srv.shutdown()


@needs_gp
def test_tomer_only_sidecar_reports_health_and_capabilities():
    srv, base = start_in_thread(NoopEngine(), prioritizer=TomerPrioritizer(), mode="tomer")
    try:
        health = json.load(urllib.request.urlopen(f"{base}/healthz"))
        assert health["ok"] is True and health["frames"] == 0
        req = urllib.request.Request(f"{base}/ghostseeker.v1.InferenceService/Capabilities", data=b"{}",
                                     headers={"Content-Type": "application/json"})
        caps = json.load(urllib.request.urlopen(req))
        assert caps["modelId"] == "none" and caps["prioritizer"] == "tomer"
    finally:
        srv.shutdown()


def test_edgeml_mode_is_unchanged():
    svc = InferenceService(MockEngine())
    a, _ = _frames()
    assert "imagePriority" not in svc.Analyze({"image": _jpeg(a), "source": "cam"})
    with pytest.raises(ValueError):
        InferenceService(MockEngine(), mode="tomer")
