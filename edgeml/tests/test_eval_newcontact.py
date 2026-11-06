"""Birth matching is evaluated independently of models or tracking."""
import importlib.util
from pathlib import Path
from types import SimpleNamespace

import pytest

spec = importlib.util.spec_from_file_location("eval_newcontact", Path(__file__).parents[1] / "tools/eval_newcontact.py")
evaluation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evaluation)


def opened(frame, sid="s1", new=True, box=None):
    return {"frame": frame, "id": sid, "score": .9, "box": box or [0, 0, 10, 10], "new_contact": new}


def test_matching_birth_window_and_unique_credit():
    gt = {frame: [(1, [0, 0, 10, 10])] for frame in range(1, 10)}
    result = evaluation.birth_metrics([opened(1), opened(2, "s2"), opened(9, "s3")], gt, {1: 1}, fps=1)
    assert (result["tp"], result["fp"], result["latencies_s"]) == (1, 2, [0.])
    assert evaluation.birth_metrics([opened(6)], gt, {1: 1}, fps=1)["tp"] == 1
    assert evaluation.birth_metrics([opened(7)], gt, {1: 1}, fps=1)["tp"] == 0
    assert evaluation.birth_metrics([opened(1)], gt, {1: 2}, fps=1)["tp"] == 0
    assert evaluation.best_match([0, 0, 10, 10], [(2, [0, 0, 10, 10]), (1, [0, 0, 10, 10])]) == 1
    assert evaluation.best_match([0, 0, 1, 1], [(1, [0, 0, 10, 10])]) is None


def test_midclip_excludes_early_startup_not_late_reopens():
    gt = {1: [(1, [0, 0, 10, 10])], 8: [(1, [0, 0, 10, 10]), (2, [30, 0, 10, 10])]}
    events = [opened(1), opened(8, "s2"), opened(8, "s3", box=[30, 0, 10, 10])]
    result = evaluation.birth_metrics(events, gt, {1: 1, 2: 7}, fps=1, mid_clip=True)
    assert (result["tp"], result["fp"], result["births"], result["ignored_startup_signals"]) == (1, 1, 1, 1)
    assert result["latencies_s"] == [1.]


def test_suppressions_and_empty_error_cases():
    gt = {1: [(1, [0, 0, 10, 10])], 2: [(1, [0, 0, 10, 10])], 3: [(2, [0, 0, 10, 10])]}
    events = [opened(1), opened(2, "s2", False), opened(3, "s3", False), opened(4, "s4", False)]
    assert evaluation.suppressions(events, gt) == {"suppressed": 3, "correct_repeat": 1, "matched_not_previously_reported": 1, "unmatched": 1}
    assert evaluation.birth_metrics([], {}, {}, fps=1)["births"] == 0
    assert evaluation.birth_metrics([opened(1)], {}, {}, fps=1)["fp"] == 1
    with pytest.raises(ValueError):
        evaluation.birth_metrics([], {}, {}, fps=0)


def test_cache_includes_empty_frames_and_rejects_stale_metadata(tmp_path):
    detections = [evaluation.Detection([0, 0, 10, 10], .9, "boat")]
    pending = iter([[], detections])
    base = SimpleNamespace(detect=lambda image: next(pending))
    path, meta = tmp_path / "cache.json", {"frames": 2}
    cache = evaluation.DetectionCache(base, path, meta)
    assert cache.detect(None) == []
    assert cache.detect(None) == detections
    cache.save()
    cached = evaluation.DetectionCache(None, path, meta)
    assert cached.detect(None) == []
    assert cached.detect(None) == detections
    with pytest.raises(ValueError, match="stale"):
        evaluation.DetectionCache(None, path, {"frames": 3})
    with pytest.raises(ValueError, match="incomplete"):
        evaluation.DetectionCache(base, tmp_path / "missing.json", meta).save()
