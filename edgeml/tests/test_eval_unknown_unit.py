import importlib.util
import json
import sys
from pathlib import Path
from types import SimpleNamespace

import numpy as np
import pytest

import edgeml.embed.clip_embedder as clip
import edgeml.embed.zeroshot as zero
from edgeml.data.seqs import MotSequence
from edgeml.types import Detection


@pytest.fixture
def evaluation(monkeypatch):
    tools = Path(__file__).parents[1] / "tools"
    monkeypatch.syspath_prepend(str(tools))
    spec = importlib.util.spec_from_file_location("eval_unknown_unit", tools / "eval_unknown.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    monkeypatch.setattr(clip, "ClipEmbedder", lambda *a, **k: pytest.fail("real model requested"))
    monkeypatch.setattr(zero, "ZeroShotClassifier", lambda *a, **k: pytest.fail("real classifier requested"))
    monkeypatch.setattr(module, "make_tracker", lambda *a, **k: pytest.fail("real tracker requested"))
    return module


def sequence(root, frames=2):
    return MotSequence("A_ship_in_the_distance", root / "ghost_fleet_5fps/A_ship_in_the_distance", 2, frames, 100, 80,
                       gt=[(1, 1, [1, 1, 3, 3], 1, 1, 1)], meta={"gt_status": "complete"})


def test_embedding_cache_uses_content_and_shape(evaluation):
    calls = []
    def embed(crops):
        calls.append(crops)
        return np.array([[len(calls), crops[0].size]])
    cached = evaluation.CachedEmbedder(SimpleNamespace(embed_crops=embed))
    image = np.ones((2, 2, 3), dtype=np.uint8)
    result = cached.embed_crops([image, image.copy(), image.reshape(4, 1, 3)])
    np.testing.assert_array_equal(result, [[1, 12], [1, 12], [2, 12]])
    assert len(calls) == 2
    np.testing.assert_array_equal(cached.embed_crops([image]), [[1, 12]])
    assert len(calls) == 2 and cached.embed_crops([]).size == 0


def test_calibration_caps_identities_and_batches_dev_crops(evaluation, tmp_path, monkeypatch):
    seq = sequence(tmp_path, 5)
    seq.gt = [(frame, oid, [1, 1, 3, 3], 1, 1, 1) for oid in range(65) for frame in range(1, 6)]
    monkeypatch.setattr(evaluation.cv2, "imread", lambda path: np.zeros((10, 10, 3), dtype=np.uint8))
    batches = []
    def embed(crops):
        batches.append(len(crops))
        assert all(c.size > 0 for c in crops)
        return np.ones((len(crops), 4))
    feats, sources = evaluation.calibrate(SimpleNamespace(embed_crops=embed), [seq, sequence(tmp_path)])
    assert feats.shape == (256, 4) and batches == [16] * 16 and len(sources) == 256
    assert [s["frame"] for s in sources[:4]] == [1, 2, 3, 5]
    assert {s["object"] for s in sources} == set(range(64))
    assert all(s["set"] == "ghost_fleet_5fps" and s["seq"] == seq.seq_id for s in sources)


@pytest.mark.parametrize("mode", ["empty", "unreadable"])
def test_calibration_rejects_absent_training_crops(evaluation, tmp_path, monkeypatch, mode):
    monkeypatch.setattr(evaluation.cv2, "imread", lambda path: None)
    seqs = [] if mode == "empty" else [sequence(tmp_path)]
    with pytest.raises(ValueError, match="No dev GT crops|Unreadable calibration frame"):
        evaluation.calibrate(None, seqs)


@pytest.mark.parametrize("frozen", [False, True])
def test_model_configuration_calibrates_dev_but_only_restores_frozen_values(evaluation, monkeypatch, frozen):
    embedder = object()
    classifier = SimpleNamespace(calibrate=lambda feats: {"sim_ref": .8, "sim_width": .2})
    monkeypatch.setattr(clip, "ClipEmbedder", lambda *a, **k: embedder)
    monkeypatch.setattr(zero, "ZeroShotClassifier", lambda e: classifier if e is embedder else pytest.fail("wrong embedder"))
    calls = []
    monkeypatch.setattr(evaluation, "calibrate", lambda e, seqs: (calls.append(seqs) or np.ones((2, 4)), [{"frame": 1}]))
    config = {"calibration": {"sim_ref": .7, "sim_width": .1}} if frozen else {}
    wrapped, actual = evaluation.load_model(config, None if frozen else [])
    assert wrapped.embedder is embedder and actual is classifier
    if frozen:
        assert calls == [] and classifier.sim_ref == .7 and classifier.sim_width == .1
    else:
        assert calls == [[]] and config["unknownness_source"] == "mobileclip-s1-mcm"
        assert config["calibration"] == {"sim_ref": .8, "sim_width": .2}
        assert config["calibration_crops"] == [{"frame": 1}]


@pytest.mark.parametrize("error", [RuntimeError, OSError])
def test_missing_model_falls_back_only_on_dev(evaluation, monkeypatch, error):
    def unavailable(*a, **k):
        raise error("offline fake model")
    monkeypatch.setattr(clip, "ClipEmbedder", unavailable)
    config = {}
    assert evaluation.load_model(config, []) == (None, None)
    assert config == {"unknownness_source": "label-score", "fallback_reason": "offline fake model"}
    assert evaluation.load_model(config) == (None, None)
    with pytest.raises(RuntimeError, match="refusing test fallback") as exc:
        evaluation.load_model({})
    assert isinstance(exc.value.__cause__, error)


@pytest.mark.parametrize("mode", ["success", "missing-cache", "unreadable"])
def test_tracked_frames_replays_cached_detections_with_local_fakes(evaluation, tmp_path, monkeypatch, mode):
    path = tmp_path / "cache.jsonl"
    if mode != "missing-cache":
        path.write_text("local fake cache")
    monkeypatch.setattr(evaluation, "det_cache_path", lambda *a: path)
    selected, calls = [], []
    dets = [Detection([1, 2, 3, 4], .8, "boat")]
    monkeypatch.setattr(evaluation, "CachedDetector", lambda p: SimpleNamespace(set_frame=selected.append, detect=lambda image: dets))
    def update(actual, image, fi, timestamp):
        assert actual is dets and image.shape == (80, 100, 3)
        calls.append((fi, timestamp))
        return [f"track-{fi}"]
    monkeypatch.setattr(evaluation, "make_tracker", lambda name, fps: SimpleNamespace(update=update) if (name, fps) == ("bytetrack", 2) else pytest.fail("bad tracker config"))
    monkeypatch.setattr(evaluation.cv2, "imread", lambda p: None if mode == "unreadable" else np.zeros((80, 100, 3)))
    if mode != "success":
        with pytest.raises(FileNotFoundError if mode == "missing-cache" else ValueError):
            evaluation.tracked_frames(sequence(tmp_path), tmp_path, "fake-cache")
        assert calls == []
    else:
        assert evaluation.tracked_frames(sequence(tmp_path), tmp_path, "fake-cache") == [(1, 0., ["track-1"]), (2, .5, ["track-2"])]
        assert selected == [1, 2] and calls == [(1, 0.), (2, .5)]


@pytest.mark.parametrize("zero_shot", [False, True])
def test_replay_dispatches_aggregator_and_flushes_past_close_deadline(evaluation, tmp_path, monkeypatch, zero_shot):
    calls = []
    class Aggregator:
        def __init__(self, seq_id, **kwargs):
            calls.append((seq_id, kwargs))
            self.p = kwargs["params"]
        def update(self, tracks, image, fi, timestamp):
            assert image.shape == (80, 100, 3)
            return [{"frame": fi}]
        def flush(self, timestamp):
            calls.append(timestamp)
            return [{"closed": True}]
        def trajectories(self):
            return ["trajectory"]
    monkeypatch.setattr(evaluation, "_ZeroShotAggregator" if zero_shot else "SightingAggregator", Aggregator)
    monkeypatch.setattr(evaluation.cv2, "imread", lambda p: np.zeros((80, 100, 3)))
    events, trajectories = evaluation.replay(sequence(tmp_path), [(1, 0., []), (2, .5, [])], {"close_after_s": 2}, "fake-embedder", "fake-classifier" if zero_shot else None)
    assert events == [{"frame": 1}, {"frame": 2}, {"closed": True}] and trajectories == ["trajectory"]
    assert calls[-1] == 4 and calls[0][1]["frame_size"] == (100, 80)
    assert ("classifier" in calls[0][1]) is zero_shot


def test_canonical_dev_split_accepts_member_and_rejects_held_out_source(evaluation, tmp_path):
    seq = sequence(tmp_path)
    evaluation.verify_split([seq], "dev")
    with pytest.raises(ValueError, match="canonical test"):
        evaluation.verify_split([seq], "test")


def cli(evaluation, tmp_path, monkeypatch, split="dev", frozen=None):
    calls = []
    monkeypatch.setitem(sys.modules, "torch", SimpleNamespace(set_num_threads=lambda n: calls.append(("threads", n))))
    monkeypatch.setattr(evaluation.cv2, "setNumThreads", lambda n: calls.append(("opencv-threads", n)))
    monkeypatch.setattr(evaluation, "load_sets", lambda *a: [sequence(tmp_path)])
    monkeypatch.setattr(evaluation, "verify_split", lambda seqs, s: calls.append(("verify", s)))
    real_load_model = evaluation.load_model
    monkeypatch.setattr(evaluation, "load_model", lambda config, seqs: (calls.append(("model", seqs)), real_load_model(config, seqs))[1]
                        if seqs is None else (calls.append(("model", seqs)) or None, None))
    monkeypatch.setattr(evaluation, "tracked_frames", lambda *a: calls.append(("tracked", a[0].seq_id)) or [])
    monkeypatch.setattr(evaluation, "replay", lambda seq, rows, params, *a: ([(params["unknown_min_hits"], params["realness_threshold"])], []))
    def measure(seq, events, *a):
        n, rho = events[0]
        poor = n == 8 and rho == .3
        counts = dict(gt=4, surfaced_gt=2 if poor else (4 if n == 3 else 3), unknown_gt=2,
                      matched_unknown_candidates=4, matched_unknown_flagged=2 if rho >= .5 else 1,
                      sea_frames=10, false_unknown_sea=0 if poor else (1 if n >= 5 else 3), unmatched_noncomplete=0)
        return {"gt_status": "complete", "before": counts, "after": counts}
    monkeypatch.setattr(evaluation, "measure", measure)
    args = ["eval_unknown", "--data", str(tmp_path), "--out", str(tmp_path / "out"), "--split", split]
    if frozen is not None:
        args += ["--frozen", str(frozen)]
    monkeypatch.setattr(sys, "argv", args)
    return calls


def frozen_file(tmp_path, **overrides):
    path = tmp_path / "frozen.json"
    path.write_text(json.dumps({"cache": "yolo11n-c05", "calibration_split": "dev", "known_score": .5, "unknownness_source": "label-score",
        "params": {"unknown_min_hits": 5, "realness_threshold": .5, "unknown_threshold": .5}, **overrides}))
    return path


def test_dev_cli_selects_with_recall_tolerance_and_publishes_frozen_config(evaluation, tmp_path, monkeypatch, capsys):
    calls = cli(evaluation, tmp_path, monkeypatch)
    evaluation.main()
    result = json.loads((tmp_path / "out/dev.json").read_text())
    frozen = json.loads((tmp_path / "out/frozen.json").read_text())
    assert len(result["sweep"]) == 9 and result["chosen"]["N"] == 8 and result["chosen"]["rho"] == .7
    assert frozen == result["config"] and frozen["params"]["unknown_min_hits"] == 8
    assert sum(c[0] == "tracked" for c in calls) == 1
    assert next(c[1] for c in calls if c[0] == "model") is not None
    assert "Chosen: 8 0.7" in capsys.readouterr().out


def test_frozen_cli_uses_one_variant_without_recalibration_or_mutating_input(evaluation, tmp_path, monkeypatch):
    path = frozen_file(tmp_path)
    before = path.read_bytes()
    calls = cli(evaluation, tmp_path, monkeypatch, "test", path)
    evaluation.main()
    result = json.loads((tmp_path / "out/test.json").read_text())
    assert len(result["sweep"]) == 1 and result["chosen"]["N"] == 5 and result["chosen"]["rho"] == .5
    assert next(c[1] for c in calls if c[0] == "model") is None
    assert path.read_bytes() == before and not (tmp_path / "out/frozen.json").exists()


@pytest.mark.parametrize("mode", ["missing-frozen", "existing", "empty", "cache", "calibration"])
def test_cli_rejects_unsafe_or_empty_evaluations_before_loading_models(evaluation, tmp_path, monkeypatch, mode):
    path = frozen_file(tmp_path, **({"cache": "other"} if mode == "cache" else {"calibration_split": "test"} if mode == "calibration" else {}))
    calls = cli(evaluation, tmp_path, monkeypatch, "test", None if mode == "missing-frozen" else path)
    result = tmp_path / "out/test.json"
    if mode == "existing":
        result.parent.mkdir(); result.write_text("previous evaluation")
    if mode == "empty":
        monkeypatch.setattr(evaluation, "load_sets", lambda *a: [])
    with pytest.raises(SystemExit) as exc:
        evaluation.main()
    assert exc.value.code == 2 and not any(c[0] == "model" for c in calls)
    if mode == "existing":
        assert result.read_text() == "previous evaluation"
    else:
        assert not result.exists()
