import base64
import copy
import hashlib
import importlib.util
import json
import sys
import threading
from pathlib import Path
from types import SimpleNamespace

import cv2
import numpy as np
import pytest
import requests


def load():
    spec = importlib.util.spec_from_file_location("gemini_unit", Path(__file__).parents[1] / "scripts/autolabel_gemini.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.fixture
def gemini(monkeypatch):
    module = load()
    monkeypatch.setattr(module.requests.Session, "request", lambda *a, **k: pytest.fail("unexpected network"))
    monkeypatch.setattr(module.subprocess, "run", lambda *a, **k: pytest.fail("unexpected subprocess"))
    module.time = SimpleNamespace(monotonic=lambda: 0., sleep=lambda seconds: pytest.fail("unexpected sleep"))
    return module


def obj(**overrides):
    return {"label": "small_boat", "box_2d": [100, 100, 400, 400], "visibility": "partial",
            "nav_lights_visible": False, "lights": [], "approaching": None, "wake": False, "confidence": .9, **overrides}


def parsed(objects=None):
    return {"objects": [obj()] if objects is None else objects, "night": False, "scene": "open_sea"}


def verdict(**overrides):
    return {"verdict": "real_object", "label": "tanker", "box_ok": True, "reason": "local fake verifier", **overrides}


class Response:
    def __init__(self, status=200, value=None):
        self.status_code = status
        self.text = "synthetic service response"
        self.value = value

    def raise_for_status(self):
        if self.status_code >= 400:
            raise requests.HTTPError(f"HTTP {self.status_code}")

    def json(self):
        return {"candidates": [{"content": {"parts": [{"text": json.dumps(self.value)}, {"ignored": True}]}}]}


@pytest.mark.parametrize("kind", ["frame", "verification"])
@pytest.mark.parametrize("scenario", ["success", "unauthorized", "busy", "server-error", "http-error", "exception", "parse-error"])
def test_requests_cache_and_retry_without_io(gemini, tmp_path, monkeypatch, kind, scenario):
    value = parsed() if kind == "frame" else verdict()
    cache = tmp_path / "cache" / "result.json"
    cache.parent.mkdir(); cache.write_text("invalid cache JSON")
    outcomes = {
        "success": [Response(value=value)],
        "unauthorized": [Response(401), Response(value=value)],
        "busy": [Response(429), Response(429), Response(429)],
        "server-error": [Response(503), Response(503), Response(503)],
        "http-error": [Response(400), Response(400), Response(400)],
        "exception": [requests.ConnectionError("offline fake")] * 3,
        "parse-error": [Response(value={})] * 3,
    }[scenario]
    calls, tokens, delays = [], [], []

    def post(url, **kwargs):
        calls.append((url, kwargs))
        outcome = outcomes[len(calls)-1]
        if isinstance(outcome, Exception):
            raise outcome
        return outcome

    monkeypatch.setattr(gemini, "session", lambda: SimpleNamespace(post=post))
    monkeypatch.setattr(gemini, "token", lambda force=False: tokens.append(force) or "synthetic-token")
    gemini.time = SimpleNamespace(sleep=delays.append)
    def request():
        if kind == "frame":
            return gemini.request_frame(7, "gs://fake/image", "local prompt", cache)
        return gemini.request_verification(7, 2, "gs://fake/image", b"local crop", "local prompt", cache)

    result, error = request()
    assert len(calls) == len(outcomes)
    if scenario in {"success", "unauthorized"}:
        assert result == value and error is None
        assert json.loads(cache.read_text()) == value
        assert request() == (value, None)
        assert len(calls) == len(outcomes)
    else:
        assert result is None and error.startswith("frame 7")
        assert cache.read_text() == "invalid cache JSON"
        assert delays == [1.5, 3.]
    if scenario == "unauthorized":
        assert tokens == [False, True, False] and delays == [.5]
    body = calls[0][1]["json"]
    assert calls[0][1]["timeout"] == 180
    assert body["generationConfig"]["temperature"] == 0
    assert body["generationConfig"]["thinkingConfig"] == {"thinkingBudget": 0}
    parts = body["contents"][0]["parts"]
    assert parts[0]["fileData"]["fileUri"] == "gs://fake/image"
    assert parts[-1]["text"] == "local prompt"
    if kind == "verification":
        assert base64.b64decode(parts[1]["inlineData"]["data"]) == b"local crop"


def test_token_refresh_boundary_and_thread_local_session(gemini, monkeypatch):
    now = [100.]
    calls = []
    gemini.time = SimpleNamespace(monotonic=lambda: now[0])
    monkeypatch.setattr(gemini.subprocess, "run", lambda *a, **kw: calls.append((a, kw)) or SimpleNamespace(stdout=" synthetic-token \n"))
    assert gemini.token() == "synthetic-token"
    now[0] += 3000
    assert gemini.token() == "synthetic-token" and len(calls) == 1
    now[0] += .01
    gemini.token(); gemini.token(force=True)
    assert len(calls) == 3
    assert calls[0][0][0] == ["gcloud", "auth", "print-access-token"]
    assert calls[0][1] == {"check": True, "capture_output": True, "text": True}
    first = gemini.session()
    assert gemini.session() is first
    other = []
    worker = threading.Thread(target=lambda: other.extend([gemini.session(), gemini.session()]))
    worker.start(); worker.join()
    assert other[0] is other[1] and other[0] is not first
    assert gemini.session() is first


@pytest.mark.parametrize("field,bad", [("label", "invalid"), ("box_2d", [0, 1, 2, True]),
    ("visibility", "invisible"), ("nav_lights_visible", 1), ("lights", ["invalid"]),
    ("approaching", "yes"), ("wake", 1), ("confidence", "high")])
def test_frame_parser_rejects_invalid_object_fields(gemini, field, bad):
    value = parsed([obj(**{field: bad})])
    with pytest.raises(ValueError):
        gemini.parse_json_text(json.dumps(value))


@pytest.mark.parametrize("value", [{}, {"objects": [], "night": 1, "scene": "open_sea"},
    {"objects": [], "night": False, "scene": "invalid"}, {"objects": [], "night": False, "scene": "open_sea", "frame_notes": 1}])
def test_frame_parser_rejects_invalid_envelopes(gemini, value):
    with pytest.raises(ValueError):
        gemini.parse_json_text(json.dumps(value))


@pytest.mark.parametrize("field,bad", [("verdict", "invalid"), ("label", "invalid"), ("box_ok", 1), ("reason", 1)])
def test_verifier_parser_rejects_invalid_fields(gemini, field, bad):
    with pytest.raises(ValueError):
        gemini.parse_verifier_text(json.dumps(verdict(**{field: bad})))


def test_parsers_accept_fences_prose_and_empty_detections(gemini):
    assert gemini.parse_json_text("```json\n" + json.dumps(parsed([])) + "\n```") == parsed([])
    assert gemini.parse_verifier_text("explanation: " + json.dumps(verdict()) + " trailing prose") == verdict()
    for parser in [gemini.parse_json_text, gemini.parse_verifier_text]:
        with pytest.raises(ValueError, match="no JSON object"):
            parser("no response")
    assert gemini.normalize_frame(None, 100, 100) == ([], 0)
    assert gemini.normalize_frame(parsed([obj(label="invalid"), obj(box_2d=[0, 0, 1, 1])]), 100, 100) == ([], 2)
    assert gemini.iou([0, 0, 0, 0], [0, 0, 0, 0]) == 0
    assert gemini.nms([]) == [] and gemini.link_frames({}) == ([], [])


def test_linking_is_one_to_one_survives_short_gaps_and_relabels_by_track_majority(gemini):
    template = gemini.normalize_frame(parsed(), 100, 100)[0][0]
    def detection(x, label):
        return {**copy.deepcopy(template), "box": [x, 10., 10., 10.], "label": label}
    frames = {
        1: [detection(10., "small_boat"), detection(13., "buoy")],
        2: [detection(11., "tanker"), detection(12., "buoy")],
        3: [],
        4: [{**detection(11., "tanker"), "label_v1": "small_boat", "verifier": "real_object",
             "verifier_box_ok": True, "verifier_reason": "verified locally"}],
        8: [detection(11., "small_boat")],
    }
    before = copy.deepcopy(frames)
    rows, attrs = gemini.link_frames(frames)
    assert [(row[0], row[1]) for row in rows] == [(1, 1), (1, 2), (2, 1), (2, 2), (4, 1), (8, 3)]
    assert all(row[7] == gemini.LABELS.index("tanker") + 1 for row in rows if row[1] == 1)
    assert all(row[7] == gemini.LABELS.index("buoy") + 1 for row in rows if row[1] == 2)
    verified = next(row for row in attrs if row["frame"] == 4)
    assert verified["label"] == "tanker" and verified["label_v1"] == "small_boat"
    assert verified["verifier"] == "real_object" and verified["verifier_box_ok"] is True
    assert verified["verifier_reason"] == "verified locally"
    assert frames == before


def sequence(tmp_path, count=3):
    root = tmp_path / "sequence"; (root / "img1").mkdir(parents=True)
    (root / "seqinfo.ini").write_text(f"[Sequence]\nname=sequence\nseqLength={count}\nframeRate=1\nimWidth=100\nimHeight=100\nsource_dataset=local\n")
    for frame in range(1, count+1):
        assert cv2.imwrite(str(root / "img1" / f"{frame:06d}.jpg"), np.zeros((100, 100, 3), dtype=np.uint8))
    (root / "frame_map.json").write_text(json.dumps({str(i): {"gs_uri": f"gs://fake/{i}.jpg"} for i in range(1, count+1)}))
    (root / "frame_luma.jsonl").write_text("".join(json.dumps({"frame": i, "mean_luma": 50 if i == 1 else 100}) + "\n" for i in range(1, count+1)))
    prompt = tmp_path / "prompt.txt"; prompt.write_text("local prompt")
    verify = tmp_path / "verify.txt"; verify.write_text("local verification prompt")
    return root, prompt, verify


@pytest.mark.parametrize("mode", ["verified", "reject", "error", "image-error", "encode-error", "empty"])
def test_annotation_writes_auditable_mot_outputs(gemini, tmp_path, monkeypatch, mode):
    root, prompt, verify = sequence(tmp_path)
    responses = {1: parsed([] if mode == "empty" else [obj()]), 2: parsed([]), 3: None}
    monkeypatch.setattr(gemini, "load_v1_responses", lambda *a: (copy.deepcopy(responses), [{"frame": 3, "error": "offline fake"}]))
    calls = []
    def verification(*args):
        calls.append(args)
        if mode == "error":
            return None, "offline verifier"
        return verdict(verdict="reject" if mode == "reject" else "real_object"), None
    monkeypatch.setattr(gemini, "request_verification", verification)
    if mode == "image-error":
        monkeypatch.setattr(gemini, "read_frame_info", lambda *a: (100, 100))
        monkeypatch.setattr(gemini.cv2, "imread", lambda *a: None)
    if mode == "encode-error":
        monkeypatch.setattr(gemini.cv2, "imencode", lambda *a: (False, None))
    report = gemini.annotate_sequence(root, prompt, 1, verify)
    assert report["frames"] == 3 and report["v1_errors"] == 1
    assert report["night_luma_lt_60"] == 1
    expected = 0 if mode in {"reject", "empty"} else 1
    assert report["boxes"] == expected and report["tracks"] == expected
    labels = json.loads((root / "gt/labels.json").read_text())
    assert labels["method"]["prompt_sha256"] == hashlib.sha256(prompt.read_bytes()).hexdigest()
    assert labels["method"]["stages"]["V"]["on_error"] == "keep_box"
    rows = (root / "gt/gt.txt").read_text().splitlines()
    assert len(rows) == expected
    if mode == "verified":
        assert int(rows[0].split(",")[7]) == gemini.LABELS.index("tanker") + 1
        assert calls[0][2] == "gs://fake/1.jpg" and calls[0][3].startswith(b"\xff\xd8")
    if mode in {"error", "image-error", "encode-error"}:
        assert int(rows[0].split(",")[7]) == gemini.LABELS.index("small_boat") + 1
    errors = [json.loads(line) for line in (root / "teacher/verifier_errors.jsonl").read_text().splitlines()]
    assert len(errors) == (1 if mode in {"error", "image-error", "encode-error"} else 0)


def test_annotation_cli_indexes_local_outputs(gemini, tmp_path, monkeypatch):
    root, prompt, verify = sequence(tmp_path, 1)
    monkeypatch.setattr(gemini, "load_v1_responses", lambda *a: ({1: parsed([])}, []))
    monkeypatch.setattr(sys, "argv", ["autolabel_gemini", "--root", str(tmp_path), "--prompt", str(prompt), "--verify-prompt", str(verify), "--workers", "1"])
    gemini.main()
    summary = json.loads((tmp_path / "gemini_summary.json").read_text())
    index = json.loads((tmp_path / "index.jsonl").read_text())
    assert summary[0]["seq_id"] == root.name and summary[0]["boxes"] == 0
    assert index["seq_id"] == root.name and index["source_dataset"] == "local" and index["n_frames"] == 1
    assert index["n_tracks"] == index["n_boxes"] == 0


def test_read_frame_reports_missing_local_image(gemini, tmp_path):
    with pytest.raises(FileNotFoundError):
        gemini.read_frame_info(tmp_path, 1)
