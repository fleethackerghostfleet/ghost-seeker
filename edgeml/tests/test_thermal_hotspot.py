from types import SimpleNamespace
from unittest.mock import Mock

import cv2
import numpy as np
import pytest

from edgeml.detectors.thermal import ThermalFusionDetector, ThermalHotspotProposer, configure_thermal, is_thermal
from edgeml.types import Detection, Track


def sea(black=False):
    gray = np.full((240, 320), 90, np.uint8)
    gray[:80] = 150
    gray[140:156, 150:180] = 15 if black else 230
    return cv2.cvtColor(gray, cv2.COLOR_GRAY2BGR)


def test_modality():
    assert is_thermal(sea())
    assert is_thermal(sea()[..., 0])
    assert is_thermal(sea()[..., :1])
    color = sea()
    color[..., 0] = 0
    assert not is_thermal(color)
    assert not is_thermal(np.zeros((0, 0, 3), np.uint8))


@pytest.mark.parametrize("black", [False, True])
def test_blob_and_sky(black):
    image = sea(black)
    image[15:35, 200:220] = 15 if black else 230
    props = ThermalHotspotProposer().detect(image)
    assert props and all(p.box[1] > 70 for p in props)
    assert any(p.box[0] <= 165 <= p.box[0] + p.box[2] and p.box[1] <= 148 <= p.box[1] + p.box[3] for p in props)
    assert all(p.label == "thermal contact" and .3 <= p.score <= .9 for p in props)


def test_noise_empty_and_filters():
    noise = np.clip(100 + np.random.default_rng(2).normal(0, 5, (240, 320)), 0, 255).astype(np.uint8)
    assert ThermalHotspotProposer().detect(noise) == []
    assert ThermalHotspotProposer().horizon(np.full((100, 100), 100, np.float32)) is None
    assert ThermalHotspotProposer().detect(np.zeros((0, 0), np.uint8)) == []
    assert ThermalHotspotProposer(min_area=2000).detect(sea()) == []
    assert ThermalHotspotProposer(max_area_frac=.0001).detect(sea()) == []
    assert ThermalHotspotProposer(aspect=(4, 5)).detect(sea()) == []
    assert ThermalHotspotProposer(polarity="white").detect(sea(True)) == []


def test_fusion_modes_and_timing():
    base = SimpleNamespace(name="fake", detect=Mock(return_value=[]), last_timing_ms={"base": 1})
    proposer = SimpleNamespace(name="proposal", detect=Mock(return_value=[Detection([150, 140, 30, 16], .8, "thermal contact")]))
    detector = ThermalFusionDetector(base, proposer)
    assert len(detector.detect(sea())) == 1
    assert detector.last_timing_ms["base"] == 1 and detector.last_timing_ms["thermal"] >= 0
    base.detect.return_value = [Detection([0, 0, 10, 10], .8, "boat")]
    proposer.detect.reset_mock()
    assert len(detector.detect(sea())) == 1
    proposer.detect.assert_not_called()
    detector.mode = "uncovered"
    assert len(detector.detect(sea())) == 2
    base.detect.return_value = [Detection([145, 135, 45, 30], .8, "boat")]
    assert len(detector.detect(sea())) == 1
    color = sea(); color[..., 0] = 0
    proposer.detect.reset_mock()
    assert detector.detect(color) == base.detect.return_value
    proposer.detect.assert_not_called()


@pytest.mark.parametrize("config", [None, False])
def test_disable(config):
    base = object()
    assert configure_thermal(base, config)[0] is base


@pytest.mark.parametrize("kwargs", [{"k": 0}, {"polarity": "bad"}, {"min_area": 0}, {"aspect": (2, 1)}])
def test_invalid(kwargs):
    with pytest.raises(ValueError):
        ThermalHotspotProposer(**kwargs)


@pytest.mark.parametrize("zs,floor", [(None, .9), (.1, .9), (.98, .95)])
def test_engine_unknown_first(monkeypatch, zs, floor):
    from edgeml.runtime import detectors
    from edgeml.sidecar import service
    import edgeml.tracking as tracking

    monkeypatch.setattr(detectors, "load_detector", lambda *a, **kw: SimpleNamespace(name="fake", detect=lambda im: []))
    class Tracker:
        def update(self, dets, image, fi, ts):
            return [Track(i + 1, d.box, d.score, d.label, fi, ts) for i, d in enumerate(dets)]
    monkeypatch.setattr(tracking, "make_tracker", lambda *a, **kw: Tracker())
    monkeypatch.setattr(service, "frame_report", lambda *a, **kw: None)
    engine = service.EdgemlEngine("fake", agg={"min_hits": 2}, thermal_fallback={"unknownness_floor": floor})
    if zs is not None:
        engine.classifier = SimpleNamespace(classify=lambda x: [{"subtype": "tanker", "unknownness": zs}])
        engine.embedder = SimpleNamespace(embed_crops=lambda crops: np.tile([1., 0.], (len(crops), 1)))
    responses = [engine.analyze("ir", sea(), None, [], False) for _ in range(5)]
    sightings = [s for r in responses for s in r["sightings"]]
    assert sightings and any(s["event"] == "EVENT_OPEN" for s in sightings)
    assert all(s["keyDetection"]["label"] == "thermal contact" and s["unknownness"] >= max(zs or 0, floor) for s in sightings)
    assert all(r["unknownness"] >= max(zs or 0, floor) for r in responses if r["sightings"])
    color = sea(); color[..., 0] = 0
    rgb = engine.analyze("rgb", color, None, [], False)
    assert rgb["detections"] == rgb["sightings"] == [] and rgb["unknownness"] == 0


def test_cli_thermal_options(monkeypatch):
    from edgeml.sidecar import __main__ as cli, service, server
    from edgeml.runtime import threads
    constructor = Mock()
    monkeypatch.setattr(service, "EdgemlEngine", constructor)
    monkeypatch.setattr(server, "serve", Mock())
    monkeypatch.setattr(threads, "pin_threads", Mock())
    cli.main(["--det", "fake", "--thermal-fallback", '{"k":6,"unknownness_floor":0.95}'])
    assert constructor.call_args.kwargs["thermal_fallback"] == {"k": 6, "unknownness_floor": .95}
    cli.main(["--det", "fake", "--thermal-fallback", "false"])
    assert constructor.call_args.kwargs["thermal_fallback"] is False


@pytest.mark.parametrize("threshold", [.35, .6])
def test_engine_report_threshold_controls_thermal_fusion(monkeypatch, threshold):
    from edgeml.runtime import detectors
    from edgeml.sidecar import service
    import edgeml.tracking as tracking
    from edgeml.detectors.live_tiled import LiveTiledDetector

    base = SimpleNamespace(name="fake", detect=Mock(return_value=[Detection([150, 140, 30, 16], threshold - .01, "boat")]))
    monkeypatch.setattr(detectors, "load_detector", lambda *a, **kw: base)
    monkeypatch.setattr(tracking, "make_tracker", lambda *a, **kw: SimpleNamespace(update=lambda *a: []))
    monkeypatch.setattr(service, "frame_report", lambda *a, **kw: None)
    engine = service.EdgemlEngine("fake", score_thr=threshold, thermal_fallback={"cover_thr": .25})
    assert isinstance(engine.detector.base, LiveTiledDetector)
    assert engine.detector.cover_thr == threshold
    proposal = Detection([150, 140, 30, 16], .31, "thermal contact")
    engine.detector.proposer = SimpleNamespace(name="fake-proposals", detect=Mock(return_value=[proposal]))
    response = engine.analyze("ir", sea(), None, [], False)
    assert [d["label"] for d in response["detections"]] == ["thermal contact"]
    assert response["detections"][0]["confidence"] == threshold
    assert proposal.score == .31
    base.detect.return_value = [Detection([150, 140, 30, 16], threshold, "boat")]
    engine.detector.proposer.detect.reset_mock()
    assert [d["label"] for d in engine.analyze("ir", sea(), None, [], False)["detections"]] == ["boat"]
    engine.detector.proposer.detect.assert_not_called()
    standalone = ThermalFusionDetector(SimpleNamespace(name="fake", detect=lambda im: []), engine.detector.proposer)
    assert standalone.detect(sea())[0].score == .31


def test_synthetic_is_deterministic_and_has_empty_frames():
    import importlib.util
    from pathlib import Path

    spec = importlib.util.spec_from_file_location("synth_thermal", Path(__file__).parents[1] / "tools/synth_thermal.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    frames = list(module.generate(count=10))
    again = list(module.generate(count=10))
    assert sum(not r["boxes"] for _, r in frames) == 3
    assert {r["polarity"] for _, r in frames} == {-1, 1}
    assert all(np.array_equal(a, b) and ra == rb for (a, ra), (b, rb) in zip(frames, again))


def test_proposal_limit_and_invalid_fusion():
    im = sea()
    im[190:210, 40:60] = 230
    assert len(ThermalHotspotProposer(max_props=1).detect(im)) == 1
    with pytest.raises(ValueError):
        ThermalFusionDetector(object(), mode="bad")
    with pytest.raises(ValueError):
        configure_thermal(object(), {"unknownness_floor": 1.1})
