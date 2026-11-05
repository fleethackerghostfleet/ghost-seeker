import json
import logging
from pathlib import Path
from types import SimpleNamespace

import cv2
import numpy as np
import pytest

from edgeml.nightlights import (BurstController, analyze, dark_contact, detect_lights, frame_report, load_config)
from edgeml.sidecar import service


def _scene(lights=(), day=False):
    image = np.zeros((160, 280, 3), np.uint8)
    if day:
        image[:] = 180
    for x, y, colour in lights:
        cv2.circle(image, (x, y), 3, colour, -1)
    return image


def _light(colour, x, y, flashing=False):
    return {"colour": colour, "x": float(x), "y": float(y), "flashing": flashing,
            "relative": [x / 280, y / 160], "contrast": 255.0}


def test_burst_controller_request_expire_and_duty_cap():
    burst = BurstController(burst_fps=8, duration_s=3, max_duty=0.2, window_s=60)
    assert burst.fps(0) == 1
    assert burst.request(0, "possible flashing light")
    assert burst.active(1) and burst.fps(1) == 8 and burst.frame_interval(1) == pytest.approx(0.125)
    assert not burst.active(3.5) and burst.fps(3.5) == 1
    assert burst.request(10) and burst.request(20) and burst.request(30)
    assert not burst.request(40)
    assert burst.denied == 1
    assert not burst.request(31, "extend")
    assert burst.duty(40) <= 0.2 + 1e-9
    fresh = BurstController(duration_s=3)
    assert fresh.request(0) and fresh.request(2, "extend")
    assert fresh.active(4.5) and fresh.granted == 1 and fresh.extensions == 1
    no_op = BurstController(duration_s=3)
    assert no_op.request(0) and no_op.request(0)
    assert no_op.granted == 1 and no_op.extensions == 0


def test_burst_degrades_when_source_cannot_go_faster(caplog):
    burst = BurstController()
    burst.request(0)
    with caplog.at_level(logging.WARNING):
        assert burst.fps(0.5, source_max_fps=1.0) == 1.0
    assert burst.degraded == 1 and "limited to 1.00 fps" in caplog.text
    assert burst.fps(0.5, source_max_fps=0.5) == 1.0
    assert caplog.text.count("burst requested") == 1
    assert burst.fps(0.5, source_max_fps=4.0) == 4.0


def test_config_defaults():
    config = load_config()
    assert config["burst"] == {
        "base_fps": 1.0, "burst_fps": 8.0, "duration_s": 3.0, "max_duty": 0.2, "window_s": 60.0,
        "triggers": {"possible_flashing_yellow": True, "flashing_rate_unresolved": True,
                     "unresolved_light_stack": True, "low_confidence_pattern": True},
    }
    assert BurstController.from_config().burst_fps == 8.0


def test_burst_reasons_by_observation():
    box = (20, 15, 240, 125)
    flashing_yellow = [_scene([(100, 45, (0, 255, 255))] if frame in (0, 2) else [])
                       for frame in range(4)]
    assert analyze(flashing_yellow, box)["burst"]["requested"]
    assert analyze(_scene([(100, 45, (0, 255, 255))]), box)["burst"]["requested"]
    nuc = _scene([(100, 40, (0, 0, 255)), (100, 60, (0, 0, 255))])
    assert not analyze(nuc, box)["burst"]["requested"]
    assert not analyze(_scene([(100, 45, (0, 255, 255))], day=True), box)["burst"]["requested"]
    assert not analyze(_scene([(100, 45, (0, 255, 255))]), box, "IR")["burst"]["requested"]


def test_burst_duty_cap_for_continuous_triggers():
    burst = BurstController()
    for second in range(60):
        burst.request(float(second), "continuous trigger")
    assert burst.duty(59) <= 0.2 + 1e-9
    assert burst.denied > 0


def test_dark_contact_and_masthead_above_hull_box():
    hull_box = (70, 70, 100, 55)
    dark = _scene()
    result = dark_contact(dark, hull_box, "cargo vessel", 0.8, gate_decision={"night": True, "confidence": 0.95})
    assert result["flagged"] and result["confidence"] == 0.784

    lit = _scene([(120, 42, (0, 0, 255))])
    result = dark_contact(lit, hull_box, "boat", gate_decision={"night": True, "confidence": 0.95})
    assert not result["flagged"] and result["basis"] == "lights_present" and result["n_lights"] == 1


def test_dark_contact_uses_unclassified_candidates_without_association():
    box = (70, 70, 100, 55)
    image = _scene([(100, 90, (0, 90, 140))])
    assert detect_lights(image, box) == []
    assert detect_lights(image, box, associate=False)
    result = dark_contact(image, box, 'boat', gate_decision={'night': True})
    assert not result['flagged'] and result['basis'] == 'lights_present'
    assert result['n_lights'] > 0


def test_distress_demo_track_is_not_dark():
    root = Path(__file__).resolve().parents[2]
    catalog = json.loads((root/'internal/catalog/nightlights.json').read_text())
    for entry in catalog['nightLibrary']:
        for detection in entry['dets']:
            if 'track_observation' not in detection:
                continue
            frames = [cv2.imread(str(root/'web/static'/name))
                      for name in detection['sequence_files']]
            assert len(frames) == 4 and all(frame is not None for frame in frames)
            box = [2*detection['b'][key] for key in ('x', 'y', 'w', 'h')]
            result = analyze(frames, box, signal=detection['signal'], label=detection['cls'])
            assert [light['colour'] for light in result['lights']] == ['red']
            assert result['lights'][0]['flashing']
            assert result['interpretation']['boost'] == 1
            dark = result['dark_contact']
            assert not dark['flagged'] and dark['basis'] == 'lights_present'
            assert dark['n_lights'] == 2
            assert detection['track_observation']['dark_contact'] == dark


def test_permissive_candidates_empty_and_invalid_boxes():
    assert detect_lights(_scene(), (70, 70, 100, 55), associate=False) == []
    assert detect_lights(_scene(), (500, 500, 100, 55), associate=False) == []
    with pytest.raises(ValueError):
        detect_lights(_scene(), (0, 0, 0, 55), associate=False)


def test_permissive_candidates_reject_low_contrast_glow():
    image = _scene()
    cv2.rectangle(image, (70, 70), (170, 125), (85, 85, 85), -1)
    assert detect_lights(image, (70, 70, 100, 55), associate=False) == []


@pytest.mark.parametrize("modality", ["IR", "SAR", "THERMAL", "RADAR"])
def test_dark_contact_non_optical(modality):
    result = dark_contact(_scene(), (20, 20, 80, 40), "boat",
                          gate_decision={"night": True, "confidence": 0.9}, modality=modality)
    assert result["basis"] == "not_optical" and not result["flagged"]


def test_dark_contact_day_and_non_vessel():
    image = _scene(day=True)
    assert dark_contact(image, (20, 20, 80, 40), "boat")["basis"] == "day"
    assert dark_contact(_scene(), (20, 20, 80, 40), "buoy",
                        gate_decision={"night": True, "confidence": 1})["basis"] == "not_vessel"


def test_frame_report_returns_dark_and_burst():
    image = _scene([(220, 35, (0, 255, 255))])
    controller = BurstController()
    report = frame_report(image, [("boat", 0.8, (25, 75, 75, 45)),
                                  ("boat", 0.9, (195, 15, 50, 45))], controller, t=1.0)
    assert report["night"] is True
    assert report["dark"] == [(0, 0.784)]
    assert report["burst"]["fps"] == 8


def test_sidecar_engine_emits_dark_detection_and_burst(monkeypatch):
    class StubTracker:
        def update(self, *_):
            return []

    class StubAggregator:
        states = {}
        track_to_sid = {}

        def update(self, *_):
            return []

    class StubDetector:
        def detect(self, _):
            return [SimpleNamespace(label="boat", score=0.8, box=(25, 75, 75, 45)),
                    SimpleNamespace(label="boat", score=0.9, box=(195, 15, 50, 45))]

    engine = object.__new__(service.EdgemlEngine)
    engine.fps, engine.score_thr, engine.det_low_thr = 5.0, 0.25, 0.05
    engine.detector, engine.model_id = StubDetector(), "stub"
    engine.frames, engine.ms_total = 0, 0.0
    engine.sources = {}
    engine._lock = service.threading.Lock()
    src = service._Source(StubTracker(), StubAggregator())
    monkeypatch.setattr(engine, "_source", lambda _: src)
    image = _scene([(220, 35, (0, 255, 255))])
    response = engine.analyze("test", image, None, [], False)
    assert any(d["label"] == "dark contact" and d["box"] == response["detections"][0]["box"]
               for d in response["detections"])
    assert response["burst"]["fps"] == 8 and response["burst"]["durationS"] == 3
