import importlib.metadata
import importlib.util
import io
import json
import sys
from argparse import Namespace
from pathlib import Path
from types import SimpleNamespace as NS

import cv2
import numpy as np
import pytest

from edgeml.data.seqs import MotSequence
from edgeml.types import Detection, Track


@pytest.fixture
def runner(monkeypatch):
    path = Path(__file__).parents[1] / "tools" / "run_c.py"
    monkeypatch.syspath_prepend(str(path.parents[1] / "src"))
    spec = importlib.util.spec_from_file_location("coverage_run_c", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    monkeypatch.setattr(module, "_run_meta", lambda: {"git_sha": "fake-revision"})
    return module


@pytest.fixture
def sequence(tmp_path):
    root = tmp_path / "data" / "sample" / "clip"
    (root / "img1").mkdir(parents=True)
    for i in range(1, 4):
        assert cv2.imwrite(str(root / "img1" / f"{i:06d}.jpg"), np.full((24, 32, 3), i * 40, np.uint8))
    return MotSequence("clip", root, 2, 3, 32, 24, gt=[(i, 7, [1, 2, 5, 6], 1, 8, 1.0) for i in range(1, 4)], labels={"8": "boat"}, meta={"split": "dev"})


class FakeDetector:
    def set_frame(self, frame):
        self.frame = frame

    def detect(self, image):
        assert image.shape == (24, 32, 3)
        return [Detection([1, 2, 5, 6], 0.9, "boat", label_id=8)]


def test_load_sets_filters_incomplete_and_forwards_split(runner, sequence, monkeypatch):
    incomplete = NS(meta={"gt_status": "incomplete"})
    calls = []
    monkeypatch.setattr(runner, "list_sequences", lambda path, split: calls.append((path, split)) or [sequence, incomplete])
    assert runner.load_sets(Path("data"), ["sample"], "dev") == [sequence]
    assert calls == [(Path("data/sample"), "dev")]
    assert runner.load_sets(Path("data"), ["sample"], None, include_incomplete=True) == [sequence, incomplete]
    assert runner.load_sets(Path("data"), [], None) == []


def test_cache_round_trip_skip_conflict_and_force(runner, sequence, tmp_path, monkeypatch, capsys):
    import edgeml.detectors

    monkeypatch.setattr(runner, "load_sets", lambda *a: [sequence])
    monkeypatch.setattr(edgeml.detectors, "make_detector", lambda *a, **k: FakeDetector())
    args = Namespace(data=tmp_path / "data", sets=["sample"], split="dev", detector="fake.pt", imgsz=64, conf=0.1, device="cpu", name=None, force=False, min_score=0.5)
    runner.cmd_cache(args)
    cache = runner.det_cache_path(args.data, "fake", sequence)
    rows = [json.loads(line) for line in cache.read_text().splitlines()]
    assert [row["frame_idx"] for row in rows] == [1, 2, 3]
    assert all(row["box"] == [1, 2, 5, 6] and row["label"] == "boat" for row in rows)
    before = cache.read_bytes()
    runner.cmd_cache(args)
    assert "skip clip" in capsys.readouterr().out and cache.read_bytes() == before
    args.conf = 0.4
    with pytest.raises(SystemExit, match="pass --force"):
        runner.cmd_cache(args)
    assert cache.read_bytes() == before
    args.force = True
    runner.cmd_cache(args)
    meta = json.loads((args.data / "_det_cache/fake/_meta.json").read_text())
    assert meta["conf"] == 0.4 and meta["git_sha"] == "fake-revision"
    cached = runner.make_det("fake", sequence, args.data, args)
    cached.set_frame(2)
    assert cached.detect(None)[0].box == [1, 2, 5, 6]
    with pytest.raises(SystemExit, match="no detection cache"):
        runner.make_det("missing", sequence, args.data, args)


def test_oracle_options_and_frame_iteration(runner, sequence, monkeypatch):
    calls = []
    monkeypatch.setattr(runner, "OracleDetector", lambda gt, labels, **kw: calls.append((gt, labels, kw)) or "oracle")
    assert runner.make_det("oracle-d0.2-j0.05", sequence, Path("data"), None) == "oracle"
    assert calls[0][2] == {"dropout": 0.2, "jitter": 0.05}
    assert calls[0][0][1] == [(7, [1, 2, 5, 6], 8)]
    detector = FakeDetector()
    rows = list(runner.per_frame_dets(sequence, detector, every=2))
    assert [(fi, t) for fi, t, _, _ in rows] == [(1, 0), (3, 1)]
    assert detector.frame == 3 and all(len(dets) == 1 for _, _, _, dets in rows)


def matrix_args(tmp_path, **overrides):
    values = dict(data=tmp_path / "data", out=tmp_path / "run", sets=["sample"], split="dev", detectors=["fake"], trackers=["good", "bad"], every=[2], agg=None, trk='{"match_thresh":0.75}', selector='{"max_bytes_per_object":49152}', no_baselines=False, no_trackeval=False, stabilize=True)
    values.update(overrides)
    return Namespace(**values)


@pytest.mark.parametrize("minimal", [False, True])
def test_matrix_writes_replayable_results_and_handles_trackeval_failure(runner, sequence, tmp_path, monkeypatch, minimal):
    args = matrix_args(tmp_path, no_baselines=minimal, no_trackeval=minimal, stabilize=not minimal)
    monkeypatch.setattr(runner, "load_sets", lambda *a: [sequence])
    monkeypatch.setattr(runner, "make_det", lambda *a: FakeDetector())
    tracker_calls = []
    monkeypatch.setattr(runner, "make_tracker", lambda name, **kw: tracker_calls.append((name, kw)) or NS(name=name))
    monkeypatch.setattr(runner, "HorizonStabilizer", lambda: NS(stats=lambda: {"frames": 3}))
    event = {"event": "open", "sighting_id": "contact", "frame_idx": 1, "t_s": 0, "forms": []}

    def run_clip(frames, det, tracker, agg, **kw):
        assert [fi for fi, _, _ in frames] == [1, 2, 3]
        assert kw["detect_every"] == 2 and kw["fps"] == 2 and not kw["record_dets"]
        assert (kw["stabilizer"] is None) == minimal
        return {"tracks": [(1, 0, [Track(9, [1, 2, 5, 6], 0.9, "boat", 1, 0)])], "events": [event], "sighting_tracks": [("contact", 1, [1, 2, 5, 6], 0.9)], "timing": {"infer": 1}}

    def trackeval(roots, files, **kw):
        assert roots == [sequence.root] and kw["frame_stride"] == 2
        assert Path(files["clip"]).read_text().startswith("1,9,")
        if "bad" in str(kw["workdir"]):
            raise RuntimeError("fake evaluator failure")
        return {"COMBINED_SEQ": {"HOTA": 0.5, "IDF1": 0.6}}

    monkeypatch.setattr(runner, "run_clip", run_clip)
    monkeypatch.setitem(sys.modules, "edgeml.eval.trackeval_wrapper", NS(run_trackeval=trackeval))
    runner.cmd_matrix(args)
    assert tracker_calls == [("good", {"fps": 1, "match_thresh": 0.75}), ("bad", {"fps": 1, "match_thresh": 0.75})]
    rows = [json.loads(line) for line in (args.out / "results.jsonl").read_text().splitlines()]
    assert len(rows) == (2 if minimal else 4)
    tracked = next(row for row in rows if row["tracker"] == "good")
    assert tracked["sighting"]["object_recall"] == 1 and tracked["seq"] == "clip"
    assert tracked["timing"] == ({"infer": 1} if minimal else {"infer": 1, "stabilizer": {"frames": 3}})
    assert json.loads((args.out / "events/fake|good|every2/clip.jsonl").read_text()) == event
    summary = json.loads((args.out / "summary.json").read_text())
    assert summary["fake|good|every2"]["trackeval"] == (None if minimal else {"HOTA": 0.5, "IDF1": 0.6})
    if not minimal:
        assert "fake evaluator failure" in json.loads((args.out / "trackeval.json").read_text())["fake|bad|every2"]["error"]
        runner.cmd_trackeval(Namespace(data=args.data, sets=args.sets, split="dev", run=args.out))
        assert json.loads((args.out / "summary.json").read_text())["fake|good|every2"]["trackeval"]["HOTA"] == 0.5


def test_empty_matrix_and_invalid_overrides(runner, tmp_path, monkeypatch):
    monkeypatch.setattr(runner, "load_sets", lambda *a: [])
    args = matrix_args(tmp_path, no_trackeval=True)
    runner.cmd_matrix(args)
    assert (args.out / "results.jsonl").read_text() == ""
    assert json.loads((args.out / "summary.json").read_text()) == {}
    args.agg = "{bad json"
    with pytest.raises(json.JSONDecodeError):
        runner.cmd_matrix(args)


def test_delivery_replays_filters_and_preserves_false_sightings(runner, sequence, tmp_path, monkeypatch):
    monkeypatch.setattr(runner, "load_sets", lambda *a: [sequence])
    run = tmp_path / "saved"
    run.mkdir()
    rows = [{"cfg": cfg, "seq": seq, "sighting": {"matched_sightings": {"matched": 7}, "false_sighting_ids": ["false"]}} for cfg, seq in [("selected", "clip"), ("other", "clip"), ("selected", "missing")]]
    (run / "results.jsonl").write_text("\n".join(json.dumps(row) for row in rows) + "\n\n")
    seen = []
    original = runner.delivered_vs_budget

    def delivered(events, matches, *a, **kw):
        seen.append((events, matches, kw["selector_kw"]))
        return original(events, matches, *a, **kw)

    monkeypatch.setattr(runner, "delivered_vs_budget", delivered)
    args = Namespace(data=tmp_path, run=run, sets=["sample"], split="dev", cfgs=["selected"], variants='{"plain":{},"capped":{"max_bytes_per_object":10}}', tag="test")
    runner.cmd_delivery(args)
    result = json.loads((run / "delivery_test.json").read_text())
    assert list(result["plain"]["cfgs"]) == ["selected"]
    assert result["plain"]["cfgs"]["selected"]["n_gt_objects"] == 1
    assert seen == [([], {"matched": 7, "false": None}, {}), ([], {"matched": 7, "false": None}, {"max_bytes_per_object": 10})]


@pytest.mark.parametrize("command", ["cache", "matrix", "delivery", "trackeval"])
def test_cli_dispatches_defaults(runner, monkeypatch, command):
    argv = ["run_c", command, "--data", "data", "--sets", "sample"]
    if command == "matrix":
        argv += ["--out", "out"]
    elif command in ("delivery", "trackeval"):
        argv += ["--run", "out"]
    monkeypatch.setattr(sys, "argv", argv)
    seen = []
    monkeypatch.setattr(runner, "cmd_" + command, lambda args: seen.append(args))
    runner.main()
    assert seen[0].cmd == command and seen[0].sets == ["sample"]
    assert runner._json(np.int64(3)) == 3 and runner._json(np.float32(0.5)) == 0.5
    assert runner._json(Path("file")) == "file"
    with pytest.raises(TypeError):
        runner._json(object())
