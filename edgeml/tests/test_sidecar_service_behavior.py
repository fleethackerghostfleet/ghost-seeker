"""Model-free service initialization, embedding failures and sighting wire semantics."""
import json
from threading import Lock
from types import SimpleNamespace
from unittest.mock import Mock

import numpy as np
import pytest

from edgeml.embed import clip_embedder, zeroshot
from edgeml.runtime import detectors
from edgeml.sidecar import service
from edgeml.tracking import AggregatorParams
from edgeml.types import Track


@pytest.fixture
def model_fakes(monkeypatch):
    detector = SimpleNamespace(name="fixture-detector")
    monkeypatch.setattr(detectors, "load_detector", Mock(return_value=detector))
    clip = SimpleNamespace(dim=2, name="fixture-clip", short="tiny", embed_text=Mock(return_value=np.array([[0.123456, 0.5]])),
                           embed_crops=Mock(return_value=np.array([[1., 0.]])), logit_scale=lambda: 10.)
    constructor = Mock(return_value=clip)
    monkeypatch.setattr(clip_embedder, "ClipEmbedder", constructor)
    classifier = SimpleNamespace(classes=["tanker"], calibration_path="fixture-calibration")
    monkeypatch.setattr(zeroshot, "load_classifier", Mock(return_value=classifier))
    return detector, clip, constructor


def test_embedding_metadata_initialization_and_wrapper_forwarding(tmp_path, model_fakes):
    detector, clip, constructor = model_fakes
    (tmp_path / "meta.json").write_text(json.dumps({"model": "tiny", "precision": "fp16"}))
    engine = service.EdgemlEngine("fixture.onnx", tmp_path, threads=2, agg={"min_hits": 3}, thermal_fallback=None)
    constructor.assert_called_once_with("tiny", backend="ort", onnx_dir=tmp_path, threads=2, precision="fp16")
    assert engine.detector.base is detector and engine.agg_params.min_hits == 3
    assert engine.unknown_calib == "fixture-calibration"
    assert engine.capabilities() == {"modelId": "fixture-detector+live-tiles2-mob+fixture-clip", "openVocabulary": False,
                                     "fixedLabels": ["tanker", "person in water"], "embeddingDim": 2}
    assert engine.embed_text(["tanker"]) == [[0.12346, 0.5]]
    clip.embed_text.assert_called_once_with(["tanker"])
    crops = [np.zeros((8, 8, 3), np.uint8)]
    assert np.array_equal(engine.embedder.embed_crops(crops), [[1., 0.]])
    assert engine.embedder.calls == 1 and engine.embedder.ms >= 0
    assert engine.embedder.logit_scale() == 10.
    caps = engine.capabilities()
    caps["fixedLabels"].append("not-a-model-class")
    assert engine.capabilities()["fixedLabels"] == ["tanker", "person in water"]


def test_text_cache_failure_is_precondition_until_torch_is_explicitly_enabled(model_fakes):
    _, clip, constructor = model_fakes
    engine = service.EdgemlEngine("fixture.onnx", embed="none")
    with pytest.raises(service.RpcError) as error:
        engine.embed_text(["tanker"])
    assert error.value.code == "unimplemented"
    engine.embedder = service._TextEmbedder(clip)
    clip.embed_text.side_effect = RuntimeError("uncached phrase")
    with pytest.raises(service.RpcError) as error:
        engine.embed_text(["unseen"])
    assert error.value.code == "failed_precondition" and "uncached phrase" in str(error.value)
    constructor.assert_not_called()
    tower = SimpleNamespace(embed_text=Mock(return_value=np.array([[0.25, 0.75]])))
    constructor.return_value = tower
    engine._text_tower = "torch"
    assert engine.embed_text(["unseen"]) == [[0.25, 0.75]]
    assert engine.embed_text(["another"]) == [[0.25, 0.75]]
    constructor.assert_called_once_with("tiny", backend="torch")
    assert tower.embed_text.call_args_list[0].args == (["unseen"],)
    assert tower.embed_text.call_args_list[1].args == (["another"],)


def test_open_vocabulary_asset_negotiation_and_analysis_path(tmp_path, model_fakes, monkeypatch):
    from edgeml.sidecar import open_vocab
    from edgeml.types import Detection

    (tmp_path / "meta.json").write_text(json.dumps({"model": "tiny"}))
    engine = service.EdgemlEngine("fixture.onnx", tmp_path)
    assert engine.capabilities()["openVocabulary"] is False
    with pytest.raises(service.RpcError) as error:
        engine.analyze("cam", np.zeros((32, 48, 3), np.uint8), None, ["open-vocab:life raft"], False)
    assert error.value.code == "failed_precondition"
    for name in ("text_model.onnx", "tokenizer.json"):
        (tmp_path / name).touch()
    tower = Mock(return_value=np.array([[1., 0.]]))
    monkeypatch.setattr(open_vocab, "ONNXTextTower", Mock(return_value=tower))
    engine = service.EdgemlEngine("fixture.onnx", tmp_path)
    assert engine.capabilities()["openVocabulary"] is True
    assert engine.embed_text(["arbitrary noun"]) == [[1., 0.]]
    assert engine.embed_text(["arbitrary noun"]) == [[1., 0.]]
    tower.assert_called_once_with(["arbitrary noun"])
    model_fakes[0].detect = Mock(return_value=[])
    src = SimpleNamespace(lock=__import__("threading").Lock(), frame_idx=0, t0_ms=None,
                          tracker=SimpleNamespace(update=Mock(return_value=[])),
                          agg=SimpleNamespace(update=Mock(return_value=[]), states={}), new_contact=service.NewContactDetector())
    engine._source = Mock(return_value=src)
    expected = {"label": "life raft", "confidence": .7, "box": {"x": 0., "y": 0., "w": .5, "h": .5}}
    engine.open_vocab.detect = Mock(return_value=[expected])
    result = engine.analyze("cam", np.zeros((32, 48, 3), np.uint8), None, ["open-vocab:life raft"], False)
    assert expected in result["detections"]
    assert engine.open_vocab.detect.call_args.args[2] == ["life raft"]
    with pytest.raises(service.RpcError) as error:
        engine.analyze("cam", np.zeros((32, 48, 3), np.uint8), None, [f"open-vocab:phrase {i}" for i in range(6)], False)
    assert error.value.code == "invalid_argument"


def test_frame_clock_is_per_source_clamps_early_timestamps_and_has_fps_fallback(model_fakes):
    engine = service.EdgemlEngine("fixture.onnx", fps=5.)
    a, b = service._Source(None, None), service._Source(None, None)
    assert engine._frame_time(a, 10000) == 0.
    assert engine._frame_time(a, 10250) == .25
    assert engine._frame_time(a, 9000) == 0.
    assert engine._frame_time(b, 20000) == 0.
    a.frame_idx = 4
    assert engine._frame_time(a, None) == .6
    assert a.t0_ms == 10000 and b.t0_ms == 20000


@pytest.fixture
def frame_unknownness_engine(model_fakes, monkeypatch):
    from edgeml.detectors.thermal import thermal_unknown_first

    def build(thermal=False, label="boat"):
        model_fakes[0].detect = Mock(return_value=[])
        monkeypatch.setattr(service, "frame_report", lambda *a, **kw: None)
        engine = service.EdgemlEngine("fixture.onnx", thermal_fallback=thermal, live_tiling={"enabled": False})
        box = [4, 4, 10, 10]
        states = {sid: SimpleNamespace(status="open", emb=None, last_seen_t=0., traj=[],
                                      s=SimpleNamespace(sighting_id=sid, label=label, unknownness=.99,
                                                        subtype=None, box=box, best_box=box, best_score=.9,
                                                        first_t_s=0., last_t_s=0., n_frames=1))
                  for sid in ("one", "two")}
        agg = SimpleNamespace(states=states, track_to_sid={1: "one", 2: "two"},
                              update=Mock(return_value=[]),
                              _apply=lambda st: thermal_unknown_first(st, engine.thermal_unknownness_floor))
        src = SimpleNamespace(lock=Lock(), frame_idx=0, t0_ms=None, agg=agg,
                              tracker=SimpleNamespace(update=Mock(return_value=[])),
                              new_contact=service.NewContactDetector())
        monkeypatch.setattr(engine, "_source", lambda source: src)
        image = np.zeros((32, 48, 3), np.uint8)
        image[..., 0] = 255
        return engine, src, image, box

    return build


@pytest.mark.parametrize("thermal", [False, {}])
def test_rgb_frame_unknownness_matches_raw_events_for_open_update_close_duplicate_and_empty(frame_unknownness_engine, thermal):
    engine, src, image, box = frame_unknownness_engine(thermal)

    def event(kind, unknownness, sid="one"):
        return {"event": kind, "sighting_id": sid, "label": "boat", "unknownness": unknownness,
                "box": box, "best_box": box, "best_frame_idx": 1, "first_t_s": 0.}

    track = Track(2, box, .9, "boat", 1, 0.)
    frames = [([event("open", .211111)], []),
              ([event("update", .722222)], [track]),
              ([event("close", .355555)], []),
              ([], [track]), ([], []),
              ([event("update", float("nan")), event("close", .8, "two")], [])]
    for events, tracks in frames:
        src.agg.update.return_value = events
        src.tracker.update.return_value = tracks
        response = engine.analyze("rgb", image, None, [], False)
        expected = service._f(max((float(ev.get("unknownness") or 0.) for ev in events), default=0.))
        assert response["unknownness"] == expected
        assert len(response["sightings"]) == len(events) + (1 if tracks else 0)
        if events and events[0]["event"] == "update":
            assert response["sightings"][0]["event"] == "EVENT_UPDATE"
            assert not response["sightings"][0]["isKeyFrame"]
        if tracks:
            assert response["sightings"][-1]["event"] == "EVENT_DUPLICATE"
            assert "unknownness" not in response["sightings"][-1]


@pytest.mark.parametrize("thermal,expected", [(False, 0.), ({"unknownness_floor": .95}, .95)])
def test_thermal_duplicate_frame_unknownness_floor_requires_opt_in(frame_unknownness_engine, thermal, expected):
    engine, src, image, box = frame_unknownness_engine(thermal, "thermal contact")
    src.agg.states["one"].s.unknownness = .1
    src.tracker.update.return_value = [Track(1, box, .9, "thermal contact", 1, 0.)]
    response = engine.analyze("cam", image, None, [], False)
    assert src.agg.update.return_value == []
    assert response["sightings"][0]["event"] == "EVENT_DUPLICATE"
    assert response["unknownness"] == expected


@pytest.mark.parametrize("event,key,box,has_embedding", [
    ("open", True, {"x": .1, "y": .2, "w": .3, "h": .4}, True),
    ("update", False, {"x": .5, "y": .6, "w": .2, "h": .2}, True),
    ("close", False, {"x": .5, "y": .6, "w": .2, "h": .2}, False),
])
def test_sighting_wire_uses_current_or_key_box_and_proto_json_types(event, key, box, has_embedding):
    engine = service.EdgemlEngine.__new__(service.EdgemlEngine)
    ev = {"event": event, "sighting_id": "cam-1", "label": "boat", "best_frame_idx": 3 if key else 1,
          "best_box": [10, 20, 30, 40], "box": [50, 60, 20, 20], "best_score": .9123456,
          "unknownness": float("nan"), "value": None, "first_t_s": .25, "last_t_s": 1.5, "n_frames": 4,
          "reason": "confirmed", "forms": [{"kind": "crop", "bytes": 123, "quality": 75},
                                            SimpleNamespace(kind="future", bytes=None, quality=None)]}
    result = engine._sighting(ev, 3, 100, 100, 1700000000000, SimpleNamespace(emb=np.array([[.123456, .5]])))
    assert result["event"] == {"open": "EVENT_OPEN", "update": "EVENT_UPDATE", "close": "EVENT_CLOSE"}[event]
    assert result["keyDetection"] == {"label": "boat", "confidence": .91235, "box": box}
    assert result["isKeyFrame"] is key and result["subtype"] == "boat"
    assert result["unknownness"] == 0. and result["value"] == 0.
    assert result["firstSeenUnixMs"] == "1700000000250" and result["lastSeenUnixMs"] == "1700000001500"
    assert result["nFrames"] == 4
    assert result["forms"] == [{"kind": "FORM_KIND_CROP", "bytes": "123", "jpegQuality": 75},
                               {"kind": "FORM_KIND_UNSPECIFIED", "bytes": "0", "jpegQuality": 0}]
    assert ("embedding" in result) is has_embedding
    if has_embedding:
        assert result["embedding"] == [.12346, .5]
    json.dumps(result, allow_nan=False)


def test_zero_shot_cache_reuses_embedding_identity_and_is_cleared_on_close():
    classify = Mock(return_value=[{"subtype": "aircraft", "unknownness": .2}])
    embedder = SimpleNamespace(embed_crops=lambda crops: np.tile([1., 0.], (len(crops), 1)))
    agg = service._ZeroShotAggregator("cam", AggregatorParams(min_hits=1, close_after_s=.5),
                                     embedder=embedder, classifier=SimpleNamespace(classify=classify), coarse=service.EdgemlEngine.COARSE)
    image = np.random.default_rng(1).integers(0, 255, (100, 100, 3), dtype=np.uint8)
    events = agg.update([Track(1, [20, 20, 30, 30], .9, "boat", 1, 0.)], image, 1, 0.)
    assert events[0]["event"] == "open" and events[0]["label"] == "aircraft"
    sid = events[0]["sighting_id"]
    state = agg.states[sid]
    agg._apply(state)
    assert classify.call_count == 1 and state.s.unknownness == .2
    state.emb = np.array([0., 1.])
    classify.return_value = [{"subtype": "tanker", "unknownness": .7}]
    agg._apply(state)
    assert classify.call_count == 2 and state.s.label == "boat" and state.s.subtype == "tanker"
    events = agg.update([], image, 2, 1.)
    assert events[0]["event"] == "close" and events[0]["sighting_id"] == sid
    assert sid not in agg.zs and classify.call_count == 2


def test_live_engine_reports_mob_with_tiling_disabled_and_preserves_sighting_class(model_fakes, monkeypatch):
    from edgeml.types import Detection
    import edgeml.tracking as tracking

    detector, _, _ = model_fakes
    detector.detect = Mock(return_value=[Detection([100, 800, 20, 20], .8, "person")])
    monkeypatch.setattr(tracking, "make_tracker", lambda *a, **kw: SimpleNamespace(update=lambda *a: []))
    engine = service.EdgemlEngine("fixture.onnx", embed="none", live_tiling={"enabled": False})
    image = np.full((1080, 1920, 3), 60, np.uint8)
    image[:400] = 220
    result = engine.analyze("cam", image, 1000, [], False)
    assert result["detections"][0]["label"] == "person in water"
    assert result["detections"][0]["confidence"] == .8
    detector.detect.assert_called_once()
    classifier = SimpleNamespace(classify=Mock(side_effect=AssertionError("must not overwrite a person")))
    agg = service._ZeroShotAggregator("cam", AggregatorParams(min_hits=1), classifier=classifier)
    state = SimpleNamespace(s=SimpleNamespace(label="person in water"), emb=np.array([1., 0.]))
    agg._apply(state)
    assert state.s.label == "person in water"
