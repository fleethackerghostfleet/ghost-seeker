import struct
from types import SimpleNamespace

import numpy as np
import pytest

from edgeml.runtime import detectors
from edgeml.sidecar import service
from edgeml.types import Detection
import edgeml.tracking as tracking


@pytest.fixture
def detector(monkeypatch):
    value = SimpleNamespace(name="fixture", detect=lambda image: [Detection([0, 0, 2, 2], .95, "boat")])
    monkeypatch.setattr(detectors, "load_detector", lambda *a, **kw: value)
    monkeypatch.setattr(service, "frame_report", lambda *a, **kw: None)
    return value


def missing_boxmot(*args, **kw):
    raise ModuleNotFoundError("No module named 'boxmot'", name="boxmot")


def test_default_analyze_without_boxmot(monkeypatch, detector):
    monkeypatch.setattr(tracking, "make_tracker", missing_boxmot)
    engine = service.EdgemlEngine("fixture")
    result = engine.analyze("camera", np.zeros((8, 8, 3), np.uint8), None, [], False)
    assert result["detections"][0]["label"] == "boat"
    assert result["sightings"] == []
    assert engine.tracker_variant == "none"


def test_explicit_boxmot_tracker_fails_at_startup(monkeypatch, detector):
    monkeypatch.setattr(tracking, "make_tracker", missing_boxmot)
    with pytest.raises(RuntimeError, match="tracking dependencies"):
        service.EdgemlEngine("fixture", tracker="bytetrack")


def test_tracking_build_retains_bytetrack(monkeypatch, detector):
    variants = []
    monkeypatch.setattr(tracking, "make_tracker", lambda variant, **kw: variants.append(variant) or SimpleNamespace(update=lambda *a: []))
    engine = service.EdgemlEngine("fixture")
    engine.analyze("camera", np.zeros((8, 8, 3), np.uint8), None, [], False)
    assert variants == ["bytetrack"]


def test_tracking_none_has_no_tracker_dependency(monkeypatch, detector):
    def forbidden(*a, **kw):
        raise AssertionError("tracking must be disabled")
    monkeypatch.setattr(tracking, "make_tracker", forbidden)
    engine = service.EdgemlEngine("fixture", tracker="none")
    assert engine.analyze("camera", np.zeros((8, 8, 3), np.uint8), None, [], False)["detections"]


def sof(w, h):
    return b"\xff\xc0" + struct.pack(">HBHH", 11, 8, h, w) + b"\x01\x01\x11\x00"


def test_jpeg_fill_and_stuffed_marker_cap():
    huge = b"\xff\xd8\xff" + sof(10000, 10000)
    with pytest.raises(service.RpcError) as failure:
        service.check_image_dims(huge)
    assert failure.value.http_status == 413
    for prefix in (b"\xff\x00\x00\x02", b"garbage", b"\xff\xda\x00\x02"):
        with pytest.raises(service.RpcError):
            service.check_image_dims(b"\xff\xd8" + prefix + sof(1, 1))
    assert service.check_image_dims(b"\xff\xd8\xff" + sof(2, 3)) == (2, 3)
