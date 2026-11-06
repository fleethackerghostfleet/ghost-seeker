"""Frozen inference workflows with fake models, clocks and split verification."""
import importlib.util
import json
import sys
from pathlib import Path
from types import ModuleType, SimpleNamespace

import cv2
import numpy as np
import pytest

from edgeml.detectors import bakeoff
from edgeml.types import Detection


@pytest.fixture
def prediction(tmp_path, monkeypatch):
    tools = Path(__file__).parents[1] / "tools"
    monkeypatch.syspath_prepend(str(tools))
    spec = importlib.util.spec_from_file_location("shore_predict_unit", tools / "shore_predict.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    data = tmp_path / "data"
    data.mkdir()
    images = []
    for iid, shape in enumerate([(1080, 1920), (32, 64)], 1):
        name = f"{iid}.png"
        assert cv2.imwrite(str(data / name), np.full((*shape, 3), [1, 2, 3], dtype=np.uint8))
        images.append({"id": iid, "file_name": name})
    coco = {"info": {"split": "valid"}, "images": images, "annotations": []}
    (data / "_annotations.coco.json").write_text(json.dumps(coco))
    weights = tmp_path / "fine.pth"
    weights.write_bytes(b"fake-checkpoint")
    output = tmp_path / "output"
    argv = ["shore_predict", "--data", str(data), "--manifests", str(tmp_path / "manifests"),
            "--weights", str(weights), "--output", str(output)]
    monkeypatch.setattr(sys, "argv", argv)
    calls = SimpleNamespace(verified=[], model=[], detected=[], sync=[], threads=[], evaluated=[], fitted=[])
    monkeypatch.setattr(mod, "verify_split", lambda *args: calls.verified.append(args))
    monkeypatch.setattr(mod, "time", SimpleNamespace(perf_counter=iter([0., .003, 1., 1.005]).__next__))
    torch = ModuleType("torch")
    torch.set_num_threads = calls.threads.append
    torch.cuda = SimpleNamespace(synchronize=lambda: calls.sync.append(True), get_device_name=lambda _: "fake GPU")
    monkeypatch.setitem(sys.modules, "torch", torch)

    class Model:
        def __init__(self, **kwargs):
            calls.model.append(kwargs)
            self.model = SimpleNamespace(model=SimpleNamespace(class_embed=SimpleNamespace(bias=np.zeros(1))))

        def predict(self, image, **kwargs):
            calls.detected.append((np.asarray(image)[0, 0].tolist(), kwargs))
            return SimpleNamespace(xyxy=np.array([[1, 2, 11, 14]]), confidence=np.array([.9]), class_id=np.array([0]))

    rfdetr = ModuleType("rfdetr")
    rfdetr.RFDETRSmall = Model
    monkeypatch.setitem(sys.modules, "rfdetr", rfdetr)

    def fit(c, ds):
        calls.fitted.append((c, ds))
        return {"tau": .7, "feasible": True}

    def evaluate(c, ds, tau):
        calls.evaluated.append((c, ds, tau))
        return {"strata": {"all": {"images": len(c["images"])}}, "tau": tau}

    monkeypatch.setattr(mod, "fit_threshold", fit)
    monkeypatch.setattr(mod, "evaluate", evaluate)
    return SimpleNamespace(mod=mod, data=data, output=output, weights=weights, argv=argv, coco=coco, calls=calls)


@pytest.mark.parametrize("resolution", [512, 640])
def test_fine_valid_run_freezes_outputs_and_excludes_warmups(prediction, resolution):
    p = prediction
    p.argv.append("--fit-valid")
    p.argv.extend(["--resolution", str(resolution)])
    p.mod.main()
    assert len(p.calls.verified) == 1
    assert p.calls.verified[0][0] == p.data and p.calls.verified[0][2] == "valid"
    assert p.calls.threads == [4]
    assert p.calls.model == [{"device": "cuda", "pretrain_weights": str(p.weights), "resolution": resolution}]
    assert len(p.calls.detected) == 7
    assert p.calls.detected[-1] == ([3, 2, 1], {"threshold": .001})
    predictions = json.loads((p.output / "predictions.json").read_text())
    assert predictions["detections"] == [
        {"image_id": i, "category_id": 1, "bbox": [1., 2., 10., 12.], "score": .9} for i in (1, 2)]
    assert p.calls.fitted == [(p.coco, predictions["detections"])]
    assert p.calls.evaluated == [(p.coco, predictions["detections"], .7)]
    journal = [json.loads(line) for line in (p.output / "frames.jsonl").read_text().splitlines()]
    assert [r["image_id"] for r in journal] == [1, 2]
    assert [r["is_1080p"] for r in journal] == [True, False]
    latency = json.loads((p.output / "latency.json").read_text())
    assert latency["n_1080p"] == 1 and latency["p95_ms"] == pytest.approx(3)
    frozen = json.loads((p.output / "threshold.json").read_text())
    assert frozen["valid_coco_sha256"] == p.mod.digest(p.data / "_annotations.coco.json")
    assert frozen["configuration"] == predictions["metadata"]
    assert frozen["configuration"]["mode"] == ("native" if resolution == 512 else "high_res")
    complete = json.loads((p.output / "COMPLETE.json").read_text())
    assert complete["images"] == 2 and complete["split"] == "valid"
    assert complete["threshold_sha256"] == p.mod.digest(p.output / "threshold.json")


@pytest.mark.parametrize("empty", [False, True])
def test_reference_locked_run_authorizes_frozen_configuration(prediction, monkeypatch, empty):
    p = prediction
    monkeypatch.setattr(p.mod, "COCO_WEIGHTS_SHA256", p.mod.digest(p.weights))
    p.coco["info"]["split"] = "test_sg"
    if empty:
        p.coco["images"] = []
    (p.data / "_annotations.coco.json").write_text(json.dumps(p.coco))
    config = {"model": "RFDETRSmall", "rfdetr": "1.3.0", "weights": "COCO pretrained", "resolution": 512,
              "mode": "native", "candidate_score_floor": .001, "tile": None, "overlap": None,
              "include_full": True, "merge_nms_iou": None, "max_det": 300}
    threshold = p.data / "threshold.json"
    threshold.write_text(json.dumps({"tau": .8, "configuration": config}))
    selection = p.data / "selection.json"
    selection.write_text(json.dumps({"reference": {"threshold_sha256": p.mod.digest(threshold),
                                                 "weights_sha256": p.mod.digest(p.weights)}}))
    p.argv.extend(["--selection", str(selection), "--role", "reference", "--threshold", str(threshold)])

    class Reference:
        def __init__(self, *args, **kwargs):
            p.calls.model.append((args, kwargs))

        def detect(self, image):
            p.calls.detected.append(image.shape)
            return [Detection(box=[0, 0, 3, 4], score=.9, label="boat"),
                    Detection(box=[0, 0, 2, 2], score=.8, label="person")]

    monkeypatch.setattr(bakeoff, "RFDetector", Reference)
    p.mod.main()
    assert p.calls.model == [(("RFDETRSmall", "rfdetr-small"),
                              {"device": "cuda", "score_thr": .001, "pretrain_weights": str(p.weights)})]
    assert len(p.calls.detected) == (5 if empty else 7) and p.calls.fitted == []
    expected = [] if empty else [
        {"image_id": i, "category_id": 1, "bbox": [0., 0., 3., 4.], "score": .9} for i in (1, 2)]
    assert p.calls.evaluated == [(p.coco, expected, .8)]
    latency = json.loads((p.output / "latency.json").read_text())
    if empty:
        assert latency["n_1080p"] == 0 and latency["p50_ms"] is None and latency["p95_ms"] is None
    else:
        assert latency["n_1080p"] == 1 and latency["p50_ms"] == pytest.approx(3)
    started = json.loads((p.output / "STARTED.json").read_text())
    assert started["selection_sha256"] == p.mod.digest(selection)


@pytest.mark.parametrize("split,fit", [("valid", False), ("train", False), ("test_sg", False), ("test_sg", True)])
def test_invalid_partition_or_missing_authorization_never_starts(prediction, split, fit):
    p = prediction
    p.coco["info"]["split"] = split
    (p.data / "_annotations.coco.json").write_text(json.dumps(p.coco))
    if fit:
        p.argv.append("--fit-valid")
    with pytest.raises(SystemExit) as exc:
        p.mod.main()
    assert exc.value.code == 2 and not p.output.exists()
    assert p.calls.verified == [] and p.calls.model == []


def test_reference_rejects_non_native_resolution_before_loading_model(prediction, monkeypatch):
    p = prediction
    monkeypatch.setattr(p.mod, "COCO_WEIGHTS_SHA256", p.mod.digest(p.weights))
    p.argv.extend(["--fit-valid", "--resolution", "640"])
    with pytest.raises(SystemExit) as exc:
        p.mod.main()
    assert exc.value.code == 2 and p.calls.model == []
    assert not (p.output / "COMPLETE.json").exists()


@pytest.mark.parametrize("failure", ["configuration", "head", "class", "decode", "existing-output", "split-hash"])
def test_failed_runs_never_write_completion(prediction, monkeypatch, failure):
    p = prediction
    p.argv.append("--fit-valid")
    expected = ValueError
    match = {"configuration": "configuration differs", "head": "one-logit", "class": "class ID",
             "decode": "decode failed", "split-hash": "hash mismatch"}.get(failure)
    if failure == "configuration":
        threshold = p.data / "threshold.json"
        threshold.write_text(json.dumps({"tau": .1, "configuration": {}}))
        p.argv.extend(["--threshold", str(threshold)])
    elif failure in {"head", "class"}:
        factory = sys.modules["rfdetr"].RFDETRSmall

        def model(**kwargs):
            m = factory(**kwargs)
            if failure == "head":
                m.model.model.class_embed.bias = np.zeros(2)
            else:
                m.predict = lambda *a, **k: SimpleNamespace(class_id=[1])
            return m

        monkeypatch.setattr(sys.modules["rfdetr"], "RFDETRSmall", model)
    elif failure == "decode":
        (p.data / "1.png").write_bytes(b"not an image")
    elif failure == "existing-output":
        p.output.mkdir()
        expected = FileExistsError
    else:
        def reject(*args):
            raise ValueError("hash mismatch")
        monkeypatch.setattr(p.mod, "verify_split", reject)
    with pytest.raises(expected, match=match):
        p.mod.main()
    assert not (p.output / "COMPLETE.json").exists()
