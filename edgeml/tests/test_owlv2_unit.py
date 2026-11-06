import importlib.util
import itertools
import json
import sys
from contextlib import contextmanager
from pathlib import Path
from types import SimpleNamespace as NS

import numpy as np
import pytest
from PIL import Image


@pytest.fixture
def owl():
    path = Path(__file__).parents[1] / "scripts" / "autolabel_owlv2.py"
    spec = importlib.util.spec_from_file_location("coverage_owlv2", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def sequence(root, name, count):
    directory = root / name
    directory.mkdir()
    (directory / "seqinfo.ini").write_text(f"[Sequence]\nseqLength={count}\n")
    return directory


def test_model_setup_uses_cpu_eval_and_matching_processor(owl, monkeypatch):
    calls = []

    class Model:
        def to(self, device):
            calls.append(("device", device))
            return self

        def eval(self):
            calls.append("eval")
            return self

    model = Model()
    processor = object()
    torch = NS(set_num_threads=lambda count: calls.append(("threads", count)))
    monkeypatch.setitem(sys.modules, "torch", torch)
    monkeypatch.setitem(sys.modules, "transformers", NS(
        Owlv2Processor=NS(from_pretrained=lambda name: calls.append(("processor", name)) or processor),
        Owlv2ForObjectDetection=NS(from_pretrained=lambda name: calls.append(("model", name)) or model)))
    assert owl.load_model() == (processor, model, torch)
    assert calls == [("threads", 8), ("processor", owl.MODEL), ("model", owl.MODEL), ("device", "cpu"), "eval"]


@pytest.mark.parametrize("empty", [False, True])
def test_detect_rgb_preprocessing_clipping_and_empty_output(owl, tmp_path, empty):
    path = tmp_path / "frame.png"
    Image.new("L", (20, 10), color=64).save(path)
    prompts = ["boat", "buoy"]
    calls = []

    def tensor(values):
        return NS(cpu=lambda: NS(numpy=lambda: np.asarray(values)))

    @contextmanager
    def inference():
        calls.append("inference")
        yield
        calls.append("finished")

    class Processor:
        def __call__(self, text, images, return_tensors):
            assert text is prompts and return_tensors == "pt"
            assert images.mode == "RGB" and images.getpixel((0, 0)) == (64, 64, 64)
            return {"pixels": "fake-input"}

        def post_process_object_detection(self, outputs, target_sizes, threshold):
            assert outputs == "fake-output" and target_sizes == [(10, 20)] and threshold == 0.2
            return [{"boxes": tensor([] if empty else [[-2, -3, 25, 12], [3, 2, 7, 6], [9, 7, 8, 9]]), "scores": tensor([] if empty else [0.9, 0.3, 0.8]), "labels": tensor([] if empty else [0, 1, 0])}]

    def model(**inputs):
        assert inputs == {"pixels": "fake-input"} and calls[-1] == "inference"
        return "fake-output"

    detections = owl.detect(path, Processor(), model, NS(inference_mode=inference), prompts)
    assert calls == ["inference", "finished"]
    assert detections == ([] if empty else [{"label": "boat", "score": 0.9, "box": [0, 0, 20, 10]}, {"label": "buoy", "score": 0.3, "box": [3, 2, 4, 4]}])
    with pytest.raises(FileNotFoundError):
        owl.detect(tmp_path / "missing.png", Processor(), model, NS(inference_mode=inference), prompts)


def test_comparison_counts_only_sampled_frames_and_review_score_floor(owl):
    assert owl.box_iou([0, 0, 2, 2], [1, 1, 2, 2]) == pytest.approx(1 / 7)
    assert owl.box_iou([0, 0, 0, 0], [0, 0, 0, 0]) == 0
    result = owl.compare({1: [[0, 0, 4, 4], [20, 20, 4, 4]], 2: [[1, 1, 2, 2]]}, {
        1: [{"box": [0, 0, 4, 4], "score": 0.2}, {"box": [100, 100, 4, 4], "score": 0.9}], 3: []})
    assert result == {"frames_compared": 2, "gemini_boxes": 2, "gemini_box_agreement_fraction": 0.5, "owlv2_boxes_score_ge_0_3": 1, "owlv2_boxes_without_gemini_match_fraction": 1}
    empty = owl.compare({}, {})
    assert empty["frames_compared"] == 0 and empty["gemini_box_agreement_fraction"] is None and empty["owlv2_boxes_without_gemini_match_fraction"] is None


@pytest.mark.parametrize("override,seconds,stride", [(None, 1, 1), (None, 600, 2), (3, 600, 3)])
def test_run_reuses_warmups_selects_stride_and_persists_provenance(owl, tmp_path, monkeypatch, override, seconds, stride):
    first = sequence(tmp_path, "a-sequence", 7)
    second = sequence(tmp_path, "b-sequence", 3)
    (tmp_path / "not-a-sequence").mkdir()
    (first / "gt").mkdir()
    (first / "gt/gt.txt").write_text("1,7,0,0,4,4,1,1,1\n\n")
    backend = (object(), object(), object())
    monkeypatch.setattr(owl, "load_model", lambda: backend)
    ticks = itertools.count(step=seconds)
    monkeypatch.setattr(owl, "time", NS(monotonic=lambda: next(ticks)))
    calls = []

    def detect(path, processor, model, torch, prompts):
        assert (processor, model, torch) == backend
        assert "small boat" in prompts and "small_boat" not in prompts and len(prompts) == len(set(prompts))
        key = (path.parent.parent.name, int(path.stem))
        calls.append(key)
        return [{"box": [0, 0, 4, 4], "score": 0.9, "label": "boat"}]

    monkeypatch.setattr(owl, "detect", detect)
    summary = owl.run(tmp_path, override)
    assert summary["stride"] == stride and summary["stride_override"] == override
    assert len(calls) == len(set(calls))
    expected = [(first.name, i) for i in range(1, 6)] + [(first.name, i) for i in range(1, 8, stride) if i > 5] + [(second.name, i) for i in range(1, 4, stride)]
    assert calls == expected
    for directory, count in [(first, 7), (second, 3)]:
        rows = [json.loads(line) for line in (directory / "teacher/owlv2_dets.jsonl").read_text().splitlines()]
        assert [row["frame"] for row in rows] == list(range(1, count + 1, stride))
        assert all(row["stride"] == stride and row["detections"][0]["label"] == "boat" for row in rows)
        meta = json.loads((directory / "teacher/owlv2_meta.json").read_text())
        assert meta["model"] == owl.MODEL and meta["device"] == "cpu" and meta["score_threshold"] == 0.2
        assert meta["full_run_eta_minutes"] == seconds * 10 / 60
    assert summary["sequences"][first.name]["gemini_box_agreement_fraction"] == 1
    assert json.loads((tmp_path / "owlv2_summary.json").read_text()) == summary


@pytest.mark.parametrize("case", ["empty", "zero-first"])
def test_invalid_sequences_fail_before_model_loading(owl, tmp_path, monkeypatch, case):
    if case == "zero-first":
        sequence(tmp_path, "a-empty", 0)
        sequence(tmp_path, "b-valid", 3)
    loads = []
    monkeypatch.setattr(owl, "load_model", lambda: loads.append(True) or (None, None, None))
    with pytest.raises(ValueError, match="sequence|frame"):
        owl.run(tmp_path)
    assert loads == []
    assert not (tmp_path / "owlv2_summary.json").exists()


def test_inference_errors_do_not_publish_completed_summary(owl, tmp_path, monkeypatch):
    sequence(tmp_path, "clip", 1)
    monkeypatch.setattr(owl, "load_model", lambda: (None, None, None))

    def fail(*args):
        raise RuntimeError("fake inference failure")

    monkeypatch.setattr(owl, "detect", fail)
    with pytest.raises(RuntimeError, match="fake inference failure"):
        owl.run(tmp_path)
    assert not (tmp_path / "owlv2_summary.json").exists()


def test_cli_dispatch_and_invalid_stride(owl, tmp_path, monkeypatch):
    calls = []
    monkeypatch.setattr(owl, "run", lambda *args: calls.append(args))
    monkeypatch.setattr(sys, "argv", ["owl", "--root", str(tmp_path), "--stride", "3"])
    owl.main()
    assert calls == [(tmp_path, 3)]
    monkeypatch.setattr(sys, "argv", ["owl", "--stride", "0"])
    with pytest.raises(SystemExit) as error:
        owl.main()
    assert error.value.code == 2 and len(calls) == 1
