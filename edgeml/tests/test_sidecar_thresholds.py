"""Tracker low-stage overrides must also reach the detector's earlier score filter."""
import json
from types import SimpleNamespace

import numpy as np
import pytest

from edgeml.runtime import detectors, threads
from edgeml.sidecar import __main__ as cli, server, service
import edgeml.tracking as tracking


@pytest.mark.parametrize(
    "floor,overrides,expected_scores",
    [(0.05, None, []), (0.01, None, [0.03]), (0.05, {"low_thresh": 0.01}, [0.03])],
    ids=["default-floor", "explicit-detector-floor", "tracker-floor-override"],
)
def test_cli_low_stage_threshold_reaches_detector(monkeypatch, tmp_path, floor, overrides, expected_scores):
    (tmp_path / "meta.json").write_text(json.dumps({
        "model": "fixture", "family": "yolo", "input_size": 64, "class_names": ["boat"],
    }))
    # Only the weight session is fake; metadata loading, preprocessing and score filtering are real.
    session = SimpleNamespace(name="fixture", model_bytes=0, run=lambda x: [
        np.array([[[10, 10, 30, 30, 0.03, 0]]], dtype=np.float32),
    ])
    monkeypatch.setattr(detectors, "open_session", lambda *args, **kw: session)
    monkeypatch.setattr(threads, "pin_threads", lambda n: n)
    monkeypatch.setattr(cli.logging, "basicConfig", lambda **kw: None)
    monkeypatch.setattr(service, "frame_report", lambda *args, **kw: None)
    tracked, tracker_options, engines = [], [], []

    def make_tracker(variant, **kw):
        tracker_options.append(kw)

        def update(detections, image, frame_idx, timestamp):
            tracked.append([d.score for d in detections])
            return []

        return SimpleNamespace(update=update)

    monkeypatch.setattr(tracking, "make_tracker", make_tracker)
    monkeypatch.setattr(server, "serve", lambda engine, *args, **kw: engines.append(engine))
    argv = ["--detector", str(tmp_path / "model.onnx"), "--det-low-thr", str(floor)]
    if overrides is not None:
        argv += ["--trk", json.dumps(overrides)]
    cli.main(argv)
    engine = engines[0]
    response = engine.analyze("cam", np.zeros((64, 64, 3), np.uint8), 1000, [], False)
    effective_floor = overrides["low_thresh"] if overrides else floor
    assert engine.det_low_thr == effective_floor
    assert tracker_options[0]["low_thresh"] == effective_floor
    assert response["detections"] == []  # Low-stage evidence is for tracking, not high-confidence reports.
    assert tracked[0] == pytest.approx(expected_scores), (
        f"tracker low_thresh={effective_floor}, detector score_thr={engine.detector.score_thr}, "
        f"scores reaching tracker={tracked[0]}"
    )
