import builtins
import importlib.util
import os
import sys
from pathlib import Path
from types import ModuleType, SimpleNamespace

import pytest

from edgeml.detectors import bakeoff as backends, hf_det, ultralytics_zoo, zoo


@pytest.fixture
def runner(monkeypatch):
    path = Path(__file__).parents[1] / "tools/bakeoff.py"
    spec = importlib.util.spec_from_file_location("p1_bakeoff_runner", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    monkeypatch.setattr(zoo, "_REG", dict(zoo._REG))
    monkeypatch.setattr(zoo, "TIER", dict(zoo.TIER))
    return module


@pytest.fixture
def fake_backends(monkeypatch):
    def factory(kind):
        def construct(*args, **kwargs):
            return SimpleNamespace(kind=kind, args=args, kwargs=kwargs, imgsz=kwargs.get("imgsz"),
                                   training_data="fixture", processor=SimpleNamespace(
                                       image_processor=SimpleNamespace(size={"height": 768, "width": 768})))
        return construct
    for module, name in ((hf_det, "HFClosedSetDetector"), (hf_det, "HFOpenVocabDetector"),
                         (ultralytics_zoo, "UltralyticsZooDetector"), (backends, "RFDetector"),
                         (backends, "FlorenceDetector"), (backends, "HostedDetector")):
        monkeypatch.setattr(module, name, factory(name))


@pytest.mark.parametrize("reverse", [False, True])
def test_registered_builders_bind_each_model_in_both_invocation_orders(runner, fake_backends, reverse):
    runner.register_detectors()
    expected = {
        **{name: ("UltralyticsZooDetector", f"{name}.pt") for name in
           ("yolov9t", "yolov9s", "yolov9c", "yolov10n", "yolov10s", "yolo12n", "yolo12s")},
        "detr-r50": ("HFClosedSetDetector", "facebook/detr-resnet-50"),
        "conditional-detr-r50": ("HFClosedSetDetector", "microsoft/conditional-detr-resnet-50"),
        "deformable-detr": ("HFClosedSetDetector", "SenseTime/deformable-detr"),
        "rtdetrv2-r34": ("HFClosedSetDetector", "PekingU/rtdetr_v2_r34vd"),
        "owlvit-b32": ("HFOpenVocabDetector", "google/owlvit-base-patch32"),
        "owlvit-b16": ("HFOpenVocabDetector", "google/owlvit-base-patch16"),
        "gdino-base": ("HFOpenVocabDetector", "IDEA-Research/grounding-dino-base"),
        "rfdetr-nano": ("RFDetector", "RFDETRNano"),
        "rfdetr-small": ("RFDetector", "RFDETRSmall"),
        "rfdetr-base": ("RFDetector", "RFDETRBase"),
        "florence2-base-ft": ("FlorenceDetector", None),
        **{name: ("HostedDetector", None) for name in ("gemini-2.5-flash", "gemini-2.5-pro", "cloud-vision")},
    }
    names = list(expected)
    if reverse:
        names.reverse()
    for name in names:
        result = zoo.build(name, imgsz=512, device="cpu", score_thr=.25)
        kind, model_id = expected[name]
        assert result.kind == kind
        assert result.args == (() if model_id is None else (model_id,))
        assert result.kwargs["device"] == "cpu" and result.kwargs["score_thr"] == .25
        assert zoo.TIER[name] == ("hosted" if kind == "HostedDetector" else "bakeoff")
        if kind != "FlorenceDetector":
            assert result.kwargs["name"] == name
        if kind in {"RFDetector", "HostedDetector", "FlorenceDetector"}:
            assert "imgsz" not in result.kwargs
        else:
            assert result.kwargs["imgsz"] == 512
        if name.startswith("owlvit"):
            assert result.imgsz == {"height": 768, "width": 768}
            assert "overlap not independently audited" in result.training_data
        elif name == "gdino-base":
            assert result.imgsz == 512 and result.training_data == "fixture"


def test_classifier_registration_preserves_unrelated_models(runner, monkeypatch):
    from edgeml.embed import clip_embedder
    models = {"existing": ("original", "weights", 32)}
    monkeypatch.setattr(clip_embedder, "MODELS", models)
    runner.register_classifiers()
    assert models == {
        "existing": ("original", "weights", 32),
        "clip-b32-openai": ("ViT-B-32-quickgelu", "openai", 224),
        "clip-b16-openai": ("ViT-B-16-quickgelu", "openai", 224),
        "openclip-b16-laion": ("ViT-B-16", "laion2b_s34b_b88k", 224),
        "openclip-b32-datacomp": ("ViT-B-32", "datacomp_xl_s13b_b90k", 224),
        "siglip2-b16": ("ViT-B-16-SigLIP2", "webli", 224),
        "mobileclip-s2": ("MobileCLIP-S2", "datacompdr", 256),
    }


def test_raw_output_environment_requires_run_id_and_preserves_override(runner, monkeypatch, tmp_path):
    monkeypatch.delenv("BAKEOFF_API_RAW", raising=False)
    runner.configure_raw_dir(["--out", str(tmp_path), "--unknown-flag"])
    assert "BAKEOFF_API_RAW" not in os.environ
    runner.configure_raw_dir(["--run-id", "run", "--out", str(tmp_path)])
    assert os.environ["BAKEOFF_API_RAW"] == str(tmp_path / "run/raw")
    monkeypatch.setenv("BAKEOFF_API_RAW", "preserved")
    runner.configure_raw_dir(["--run-id", "other"])
    assert os.environ["BAKEOFF_API_RAW"] == "preserved"


@pytest.mark.parametrize("mode", ["detectors", "classifiers", "invalid"])
def test_main_dispatches_real_registration_and_preserves_runner_arguments(runner, monkeypatch, tmp_path, mode):
    from edgeml.runtime import threads
    from edgeml.embed import clip_embedder
    pins = []
    monkeypatch.setattr(threads, "pin_threads", lambda count: pins.append(count))
    monkeypatch.setattr(clip_embedder, "MODELS", {})
    monkeypatch.delenv("BAKEOFF_API_RAW", raising=False)
    torch_utils = ModuleType("ultralytics.utils.torch_utils")
    torch_utils.NUM_THREADS = 99
    if mode == "detectors":
        package = ModuleType("ultralytics")
        utils = ModuleType("ultralytics.utils")
        package.utils = utils
        utils.torch_utils = torch_utils
        for name, module in (("ultralytics", package), ("ultralytics.utils", utils),
                             ("ultralytics.utils.torch_utils", torch_utils)):
            monkeypatch.setitem(sys.modules, name, module)
    else:
        original = builtins.__import__
        def import_without_ultralytics(name, *args, **kwargs):
            if name == "ultralytics.utils.torch_utils":
                raise ImportError("optional backend absent")
            return original(name, *args, **kwargs)
        monkeypatch.setattr(builtins, "__import__", import_without_ultralytics)
    argv = ["bakeoff.py", mode, "--run-id", "fixture", "--out", str(tmp_path), "--custom", "value"]
    monkeypatch.setattr(sys, "argv", argv.copy())
    dispatch = []
    def external_runner(path, run_name):
        dispatch.append((Path(path).name, run_name, sys.argv.copy()))
        if mode == "detectors":
            assert "owlvit-b32" in zoo._REG and zoo.TIER["cloud-vision"] == "hosted"
            assert torch_utils.NUM_THREADS == 4
            assert os.environ["BAKEOFF_API_RAW"] == str(tmp_path / "fixture/raw")
        else:
            assert clip_embedder.MODELS["mobileclip-s2"] == ("MobileCLIP-S2", "datacompdr", 256)
            assert "BAKEOFF_API_RAW" not in os.environ
    monkeypatch.setattr(runner.runpy, "run_path", external_runner)
    if mode == "invalid":
        with pytest.raises(SystemExit, match="mode must be detectors or classifiers"):
            runner.main()
        assert dispatch == []
    else:
        runner.main()
        expected = "bench_detectors.py" if mode == "detectors" else "d_eval_zeroshot.py"
        assert dispatch == [(expected, "__main__", [argv[0], *argv[2:]])]
    assert pins == [4, 4]
