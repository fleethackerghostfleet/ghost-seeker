"""Local frozen partitions and fake model training; no GPU or real weights."""
import hashlib
import importlib.util
import json
import sys
from pathlib import Path
from types import ModuleType, SimpleNamespace

import pytest


@pytest.fixture
def training(tmp_path, monkeypatch):
    spec = importlib.util.spec_from_file_location("shore_train_unit", Path(__file__).parents[1] / "tools/shore_train.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    dataset, manifests, output = [tmp_path / s for s in ("dataset", "manifests", "output")]
    dataset.mkdir()
    manifests.mkdir()
    for split in ("train", "valid"):
        folder = dataset / split
        folder.mkdir()
        raw = b"frozen image bytes"
        (folder / "one.jpg").write_bytes(raw)
        coco = {"info": {"split": split}, "categories": [{"id": 1, "name": "vessel", "supercategory": "vessel"}],
                "images": [{"id": 1, "file_name": "one.jpg"}],
                "annotations": [{"id": 1, "image_id": 1, "category_id": 1, "bbox": [0, 0, 10, 10]}]}
        cbytes = json.dumps(coco).encode()
        (folder / "_annotations.coco.json").write_bytes(cbytes)
        shard = json.dumps({"images": [["one.jpg", hashlib.sha256(raw).hexdigest()]]}).encode()
        (manifests / f"{split}.images.json").write_bytes(shard)
        (manifests / f"{split}.json").write_text(json.dumps({"coco_sha256": hashlib.sha256(cbytes).hexdigest(),
            "image_shards": [{"file": f"{split}.images.json", "sha256": hashlib.sha256(shard).hexdigest()}]}))
    (dataset / "test").symlink_to("valid", target_is_directory=True)
    argv = ["shore_train", "--dataset", str(dataset), "--manifests", str(manifests), "--output", str(output),
            "--resolution", "512", "--batch-size", "4", "--epochs", "2", "--budget-minutes", "1"]
    monkeypatch.setattr(sys, "argv", argv)
    calls = SimpleNamespace(models=[], trained=[], threads=[], stops=[])
    torch = ModuleType("torch")
    torch.set_num_threads = calls.threads.append
    torch.cuda = SimpleNamespace(max_memory_allocated=lambda: 2 * 2**30)
    monkeypatch.setitem(sys.modules, "torch", torch)

    class Model:
        def __init__(self, **kwargs):
            calls.models.append(kwargs)
            self.callbacks = {"on_fit_epoch_end": []}
            self.model = SimpleNamespace(request_early_stop=lambda: calls.stops.append(True))

        def train(self, **kwargs):
            calls.trained.append(kwargs)
            for callback in self.callbacks["on_fit_epoch_end"]:
                callback({"train_loss": 1.25, "epoch": 0, "test_coco_eval_bbox": [.6]})
                callback({"train_loss": .75, "epoch": 1, "test_coco_eval_bbox": [.7], "ema_test_coco_eval_bbox": [.8]})

    rfdetr = ModuleType("rfdetr")
    rfdetr.RFDETRSmall = Model
    monkeypatch.setitem(sys.modules, "rfdetr", rfdetr)
    monkeypatch.setattr(mod, "time", SimpleNamespace(monotonic=iter([0., 10., 40., 41.]).__next__))
    return SimpleNamespace(mod=mod, dataset=dataset, manifests=manifests, output=output, argv=argv, calls=calls)


def test_training_adapts_only_verified_partitions_and_stops_before_budget(training):
    t = training
    original = (t.dataset / "train/_annotations.coco.json").read_bytes()
    t.argv.append("--gradient-checkpointing")
    t.mod.main()
    assert t.calls.models == [{"device": "cuda", "resolution": 512, "gradient_checkpointing": True}]
    assert t.calls.threads == [4] and t.calls.stops == [True]
    config = t.calls.trained[0]
    assert config["grad_accum_steps"] == 4 and config["batch_size"] == 4
    assert config["run_test"] is False and config["use_ema"] is True
    assert config["checkpoint_interval"] == 2
    adapter = t.output / "roboflow"
    assert (adapter / "test").resolve() == (adapter / "valid").resolve()
    coco = json.loads((adapter / "train/_annotations.coco.json").read_text())
    assert coco["categories"][0]["id"] == coco["annotations"][0]["category_id"] == 0
    assert coco["images"][0]["file_name"] == str(t.dataset / "train/one.jpg")
    assert (t.dataset / "train/_annotations.coco.json").read_bytes() == original
    journal = [json.loads(line) for line in (t.output / "shore_epochs.jsonl").read_text().splitlines()]
    assert [r["epoch_seconds"] for r in journal] == [10, 30]
    assert [r["elapsed_seconds"] for r in journal] == [10, 40]
    assert [r["peak_gpu_gb"] for r in journal] == [2, 2]
    assert journal[0]["valid_ema_coco"] is None and journal[1]["valid_ema_coco"] == [.8]
    frozen = json.loads((t.output / "shore_config.json").read_text())
    assert frozen["training"] == config
    assert frozen["partition_sha256"] == {s: t.mod.verify_split(t.dataset / s, t.manifests, s) for s in ("train", "valid")}
    complete = json.loads((t.output / "SHORE_COMPLETE.json").read_text())
    assert complete["wall_seconds"] == 41 and complete["checkpoint"] == "checkpoint_best_total.pth"


@pytest.mark.parametrize("change,error", [("coco", "annotation hash"), ("partition", "Wrong data partition"),
    ("shard", "Image manifest changed"), ("membership", "Image membership"), ("image", "Image changed")])
def test_verify_split_detects_frozen_input_changes(training, change, error):
    t = training
    folder = t.dataset / "train"
    manifest_path = t.manifests / "train.json"
    manifest = json.loads(manifest_path.read_text())
    coco_path = folder / "_annotations.coco.json"
    if change == "coco":
        coco_path.write_text("{}")
    elif change in {"partition", "membership"}:
        coco = json.loads(coco_path.read_text())
        if change == "partition":
            coco["info"]["split"] = "test_sg"
        else:
            coco["images"] = []
        coco_path.write_text(json.dumps(coco))
        manifest["coco_sha256"] = hashlib.sha256(coco_path.read_bytes()).hexdigest()
        manifest_path.write_text(json.dumps(manifest))
    elif change == "shard":
        (t.manifests / "train.images.json").write_text("{}")
    else:
        (folder / "one.jpg").write_bytes(b"modified")
    with pytest.raises(ValueError, match=error):
        t.mod.verify_split(folder, t.manifests, "train")


@pytest.mark.parametrize("option,value", [("--resolution", "513"), ("--resolution", "448"),
    ("--batch-size", "0"), ("--batch-size", "3"), ("--epochs", "0"), ("--budget-minutes", "0")])
def test_invalid_training_arguments_never_construct_model(training, option, value):
    t = training
    t.argv[t.argv.index(option) + 1] = value
    with pytest.raises(SystemExit) as exc:
        t.mod.main()
    assert exc.value.code == 2 and t.calls.models == [] and not t.output.exists()


def test_locked_test_loader_is_rejected_before_training(training):
    t = training
    (t.dataset / "test").unlink()
    (t.dataset / "test").mkdir()
    with pytest.raises(SystemExit) as exc:
        t.mod.main()
    assert exc.value.code == 2 and t.calls.models == [] and not t.output.exists()


def test_nonfinite_loss_aborts_without_a_completion_marker(training, monkeypatch):
    t = training
    model = sys.modules["rfdetr"].RFDETRSmall

    def train(self, **kwargs):
        self.callbacks["on_fit_epoch_end"][0]({"train_loss": float("nan"), "epoch": 0})

    monkeypatch.setattr(model, "train", train)
    with pytest.raises(RuntimeError, match="Non-finite epoch loss"):
        t.mod.main()
    assert not (t.output / "SHORE_COMPLETE.json").exists()
    assert not (t.output / "shore_epochs.jsonl").exists()


def test_training_view_rejects_unexpected_annotation_category(training):
    t = training
    coco = json.loads((t.dataset / "train/_annotations.coco.json").read_text())
    coco["annotations"][0]["category_id"] = 0
    with pytest.raises(ValueError, match="Unexpected annotation category"):
        t.mod.training_view(coco, t.dataset / "train")


def test_empty_frozen_partition_verifies_without_reading_images(training):
    t = training
    coco_path = t.dataset / "train/_annotations.coco.json"
    coco = json.loads(coco_path.read_text())
    coco["images"], coco["annotations"] = [], []
    coco_path.write_text(json.dumps(coco))
    shard = t.manifests / "train.images.json"
    shard.write_text(json.dumps({"images": []}))
    digest = hashlib.sha256(coco_path.read_bytes()).hexdigest()
    (t.manifests / "train.json").write_text(json.dumps({"coco_sha256": digest, "image_shards": [
        {"file": shard.name, "sha256": hashlib.sha256(shard.read_bytes()).hexdigest()}]}))
    (t.dataset / "train/one.jpg").unlink()
    assert t.mod.verify_split(t.dataset / "train", t.manifests, "train") == digest
