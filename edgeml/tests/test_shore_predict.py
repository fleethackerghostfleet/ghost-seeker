import importlib.util
import sys
from types import ModuleType, SimpleNamespace
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parents[1] / "tools"))
spec = importlib.util.spec_from_file_location("shore_predict", Path(__file__).parents[1] / "tools/shore_predict.py")
predict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(predict)


def test_locked_authorization_rejects_any_changed_weights_or_threshold():
    selection = {"selected": {"threshold_sha256": "t", "weights_sha256": "w"}}
    predict.authorize(selection, "selected", "t", "w")
    with pytest.raises(ValueError):
        predict.authorize(selection, "selected", "different", "w")
    with pytest.raises(ValueError):
        predict.authorize(selection, "selected", "t", "different")


def test_valid_selection_uses_worst_gated_cp_bound_then_sum_then_latency():
    def metrics(a, b):
        return {"strata": {"day_small": {"recall_cp95_lower": a}, "day_medium_large": {"recall_cp95_lower": b}}}
    latency = {"p95_ms": 100}
    assert predict.candidate_key(metrics(.8, .9), latency) > predict.candidate_key(metrics(.7, .99), latency)
    assert predict.candidate_key(metrics(.8, .95), latency) > predict.candidate_key(metrics(.8, .9), latency)
    assert predict.candidate_key(metrics(.8, .9), latency) > predict.candidate_key(metrics(.8, .9), {"p95_ms": 200})
    with pytest.raises(ValueError):
        predict.candidate_key(metrics(None, .9), latency)


def test_reference_checkpoint_routes_by_hash_at_any_path(tmp_path, monkeypatch):
    checkpoint = tmp_path / "downloaded" / "reference-coco.pth"
    hashed = []

    def reference_digest(path):
        hashed.append(path)
        return predict.COCO_WEIGHTS_SHA256

    monkeypatch.setattr(predict, "digest", reference_digest)
    assert predict.resolve_checkpoint(checkpoint) == (checkpoint, predict.COCO_WEIGHTS_SHA256, True)
    assert predict.resolve_checkpoint(None) == (Path("rf-detr-small.pth"), predict.COCO_WEIGHTS_SHA256, True)
    predict.authorize({"reference": {"weights_sha256": predict.COCO_WEIGHTS_SHA256, "threshold_sha256": "t"}},
                      "reference", "t", predict.resolve_checkpoint(checkpoint)[1])
    assert hashed == [checkpoint, Path("rf-detr-small.pth"), checkpoint]


def test_checkpoint_routing_rejects_changed_default_and_preserves_fine_weights(tmp_path, monkeypatch):
    checkpoint = tmp_path / "fine.pth"
    checkpoint.write_bytes(b"fine weights")
    assert predict.resolve_checkpoint(checkpoint) == (checkpoint, predict.digest(checkpoint), False)
    monkeypatch.chdir(tmp_path)
    with pytest.raises(FileNotFoundError):
        predict.resolve_checkpoint(None)
    (tmp_path / "rf-detr-small.pth").write_bytes(b"wrong checkpoint")
    with pytest.raises(ValueError, match="Reference COCO checkpoint hash changed"):
        predict.resolve_checkpoint(None)
    with pytest.raises(ValueError, match="differs from pre-test selection"):
        predict.authorize({"reference": {"weights_sha256": predict.COCO_WEIGHTS_SHA256, "threshold_sha256": "t"}},
                          "reference", "t", predict.resolve_checkpoint(checkpoint)[1])


@pytest.mark.parametrize("weights", [None, "/arbitrary/path/reference-coco.pth"])
def test_rf_adapter_forwards_optional_checkpoint_without_changing_defaults(weights, monkeypatch):
    from edgeml.detectors.bakeoff import RFDetector

    calls = []

    def factory(**kwargs):
        calls.append(kwargs)
        return SimpleNamespace(model=SimpleNamespace(resolution=512, model=SimpleNamespace(parameters=lambda: [])))

    rfdetr = ModuleType("rfdetr")
    rfdetr.RFDETRSmall = factory
    classes = ModuleType("rfdetr.util.coco_classes")
    classes.COCO_CLASSES = {}
    monkeypatch.setitem(sys.modules, "rfdetr", rfdetr)
    monkeypatch.setitem(sys.modules, "rfdetr.util", ModuleType("rfdetr.util"))
    monkeypatch.setitem(sys.modules, "rfdetr.util.coco_classes", classes)
    monkeypatch.setattr(RFDetector, "_infer", lambda self, image: [])
    RFDetector("RFDETRSmall", "reference", device="cpu", pretrain_weights=weights)
    assert calls == [{"device": "cpu", **({"pretrain_weights": weights} if weights is not None else {})}]
