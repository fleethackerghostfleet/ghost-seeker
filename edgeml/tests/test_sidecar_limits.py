"""Resource caps on the sidecar: pixel cap before decode, bounded EmbedText, loopback default."""
import base64
import json
import struct
import urllib.error
import urllib.request

import cv2
import numpy as np
import pytest

from edgeml.sidecar import MockEngine
from edgeml.sidecar.server import start_in_thread
from edgeml.sidecar.service import MAX_PIXELS, MAX_TEXTS, InferenceService, MockSchedule, RpcError, check_image_dims, image_dims


def _png_header(w, h):
    return b"\x89PNG\r\n\x1a\n" + struct.pack(">I", 13) + b"IHDR" + struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)


def _jpeg(w=64, h=48):
    ok, buf = cv2.imencode(".jpg", np.zeros((h, w, 3), dtype=np.uint8))
    assert ok
    return buf.tobytes()


def _call(base, method, body):
    req = urllib.request.Request(f"{base}/ghostseeker.v1.InferenceService/{method}", data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"}, method="POST")
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


def test_image_dims_from_headers():
    assert image_dims(_png_header(640, 480)) == (640, 480)
    assert image_dims(_jpeg(64, 48)) == (64, 48)
    assert image_dims(b"GIF89a" + struct.pack("<HH", 3, 4)) == (3, 4)
    ok, bmp = cv2.imencode(".bmp", np.zeros((5, 7, 3), dtype=np.uint8))
    assert ok and image_dims(bmp.tobytes()) == (7, 5)
    ok, webp = cv2.imencode(".webp", np.zeros((9, 11, 3), dtype=np.uint8))
    if ok:
        assert image_dims(webp.tobytes()) == (11, 9)
    assert image_dims(b"not an image") is None


def test_check_image_dims_rejects_bombs_and_unknown_formats():
    assert check_image_dims(_jpeg()) == (64, 48)
    with pytest.raises(RpcError) as ei:
        check_image_dims(_png_header(100_000, 100_000))
    assert ei.value.code == "resource_exhausted" and ei.value.http_status == 413
    with pytest.raises(RpcError) as ei:
        check_image_dims(_png_header(0, 10))
    assert ei.value.code == "resource_exhausted"
    with pytest.raises(RpcError) as ei:
        check_image_dims(b"II*\x00" + b"\x00" * 64)  # TIFF: not accepted
    assert ei.value.code == "invalid_argument"


def test_analyze_oversize_image_is_413(base):
    bomb = base64.b64encode(_png_header(MAX_PIXELS, 2)).decode()  # 2 * MAX_PIXELS px, ~60 header bytes
    code, body = _call(base, "Analyze", {"source": "cam", "image": bomb})
    assert code == 413 and body["code"] == "resource_exhausted"
    code, body = _call(base, "Analyze", {"source": "cam", "image": "%%%not-base64"})
    assert code == 400 and body["code"] == "invalid_argument"
    code, body = _call(base, "Analyze", {"source": "cam", "image": base64.b64encode(_jpeg()).decode()})
    assert code == 200


def test_embed_text_bounds(base):
    code, body = _call(base, "EmbedText", {"texts": ["a"] * (MAX_TEXTS + 1)})
    assert code == 400 and body["code"] == "invalid_argument"
    code, body = _call(base, "EmbedText", {"texts": ["x" * 513]})
    assert code == 400
    code, body = _call(base, "EmbedText", {"texts": "not-a-list"})
    assert code == 400
    code, body = _call(base, "EmbedText", {"texts": ["submarine", "cargo ship"]})
    assert code == 200 and len(body["embeddings"]) == 2


def test_service_dispatch_bounds_without_http():
    svc = InferenceService(MockEngine(MockSchedule()))
    with pytest.raises(RpcError):
        svc.dispatch("EmbedText", {"texts": list(map(str, range(MAX_TEXTS + 1)))})
