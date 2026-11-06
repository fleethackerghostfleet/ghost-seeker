import hashlib
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace

import cv2
import numpy as np
import pytest

import edgeml.embed.clip_embedder as clip
import edgeml.embed.shiptype as ship
from edgeml.types import Detection


def load():
    spec = importlib.util.spec_from_file_location("review_set_unit", Path(__file__).parents[1] / "tools/build_review_set.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.fixture
def build(monkeypatch):
    module = load()
    monkeypatch.setattr(module.subprocess, "run", lambda *a, **k: pytest.fail("unexpected subprocess"))
    monkeypatch.setattr(module, "load_detector", lambda *a, **k: pytest.fail("real detector requested"))
    monkeypatch.setattr(module, "_ship_classifier", lambda *a, **k: pytest.fail("real ship classifier requested"))
    module.time = SimpleNamespace(monotonic=lambda: 0.)
    return module


def row(**overrides):
    return {"dataset_id": "dev-camera", "image_id": "clip/frame-1", "eval_split": "dev", "source_video": "dev-clip",
            "gcs_uri": "gs://fake/frame.jpg?generation=1", "sha256": "unused", "width": 100, "height": 80, **overrides}


def image_fixture(build, tmp_path, rows):
    for candidate in rows:
        path = tmp_path / build._relative_file(candidate)
        path.parent.mkdir(parents=True, exist_ok=True)
        assert cv2.imwrite(str(path), np.zeros((candidate["height"], candidate["width"], 3), dtype=np.uint8))


def test_review_selection_and_safe_relative_names(build, tmp_path):
    path = tmp_path / "selection.jsonl"
    path.write_text("\n")
    with pytest.raises(ValueError, match="selection is empty"):
        build.read_selection(path)
    path.write_text("not JSON")
    with pytest.raises(json.JSONDecodeError):
        build.read_selection(path)
    candidate = row(dataset_id="../../camera", image_id="../../secret", gcs_uri="gs://bucket/file.PNG?q=1")
    path.write_text(json.dumps(candidate) + "\n\n")
    assert build.read_selection(path) == [candidate]
    relative = build._relative_file(candidate)
    assert relative.as_posix() == "camera/secret.png"
    assert not relative.is_absolute() and ".." not in relative.parts
    assert build._safe_part("../") == "image"
    assert build._relative_file(row(gcs_uri="gs://bucket/no_extension")).suffix == ".jpg"


@pytest.mark.parametrize("mode", ["success", "missing", "bad-hash"])
def test_download_verifies_outputs_and_always_cleans_staging(build, tmp_path, monkeypatch, mode):
    payload = b"local synthetic image payload"
    candidate = row(sha256=hashlib.sha256(payload).hexdigest())
    image_dir = tmp_path / "images"
    stale = tmp_path / ".review-download"
    stale.mkdir(); (stale / "stale").write_text("stale")
    calls = []

    def download(command, check):
        calls.append(command)
        assert check and command[:4] == ["fake-gsutil", "-m", "cp", "-n"]
        if mode != "missing":
            (Path(command[-1]) / "frame.jpg").write_bytes(payload if mode == "success" else b"wrong bytes")

    monkeypatch.setattr(build.subprocess, "run", download)
    if mode == "success":
        build.download_images([candidate], image_dir, "fake-gsutil")
        target = image_dir / build._relative_file(candidate)
        assert target.read_bytes() == payload
        build.download_images([candidate], image_dir, "fake-gsutil")
        assert len(calls) == 1
    else:
        with pytest.raises((FileNotFoundError, ValueError), match="did not create|SHA-256 mismatch"):
            build.download_images([candidate], image_dir, "fake-gsutil")
        assert not (image_dir / build._relative_file(candidate)).exists()
    assert not stale.exists()


def test_download_rechecks_copied_hash(build, tmp_path, monkeypatch):
    candidate = row(sha256=hashlib.sha256(b"correct").hexdigest())
    monkeypatch.setattr(build.subprocess, "run", lambda command, **kw: (Path(command[-1]) / "frame.jpg").write_bytes(b"correct"))
    monkeypatch.setattr(build.shutil, "copy2", lambda source, target: target.write_bytes(b"corrupted copy"))
    with pytest.raises(ValueError, match="after copy"):
        build.download_images([candidate], tmp_path / "images", "fake-gsutil")
    assert not (tmp_path / ".review-download").exists()


@pytest.mark.parametrize("typing", ["disabled", "completed", "missing", "budget"])
def test_manifest_filters_scales_and_types_with_fake_backends(build, tmp_path, monkeypatch, typing):
    candidate = row()
    image_fixture(build, tmp_path, [candidate])
    detections = [Detection(box=[10, 20, 30, 20], score=.9, label="boat"),
                  Detection(box=[0, 0, 10, 10], score=.8, label="person"),
                  Detection(box=[0, 0, 1, 1], score=.99, label="car"),
                  Detection(box=[0, 0, 1, 1], score=.34, label="bird")]
    if typing == "budget":
        detections = [detections[0]] * 33
    configs = []
    monkeypatch.setattr(build, "load_detector", lambda path, **kw: configs.append((path, kw)) or SimpleNamespace(detect=lambda image: detections))
    batches = []

    def classifier(*args):
        if typing == "missing":
            raise FileNotFoundError("fake cache absent")
        def classify(crops):
            batches.append(len(crops))
            return [{"type": "warship", "label": "Warship", "aspect": "bow"} for _ in crops]
        return SimpleNamespace(classify=classify)

    monkeypatch.setattr(build, "_ship_classifier", classifier)
    if typing == "budget":
        ticks = iter([0., 1., 2., 3., 103., 104.])
        build.time = SimpleNamespace(monotonic=lambda: next(ticks))
    records, stats = build.build_manifest([candidate], tmp_path, Path("fake.onnx"), None if typing == "disabled" else Path("fake-embed"), 2, 1.)
    assert configs == [(Path("fake.onnx"), {"engine": "ort", "threads": 2, "score_thr": .05})]
    assert records[0]["file"] == build._relative_file(candidate).as_posix()
    assert records[0]["dets"][0]["box"] == {"x": 48., "y": 75., "w": 144., "h": 75.}
    assert all("_label" not in d for d in records[0]["dets"])
    if typing == "completed":
        assert stats["typed_boat_detections"] == 1 and batches == [1]
        assert records[0]["dets"][0]["cls"] == "warship"
        assert stats["detections_by_class"]["warship"] == 1
    else:
        assert stats["typed_boat_detections"] == 0
        assert records[0]["dets"][0]["cls"] == "vessel"
    if typing == "disabled":
        assert stats["ship_typing"] == "disabled"
        assert len(records[0]["dets"]) == 2 and stats["images_with_person_detection"] == 1
    if typing == "missing":
        assert stats["ship_typing"].startswith("skipped: fake cache absent")
    if typing == "budget":
        assert "estimated CPU runtime" in stats["ship_typing"] and batches == [32]


@pytest.mark.parametrize("failure", ["decode", "dimensions"])
def test_manifest_rejects_bad_images(build, tmp_path, monkeypatch, failure):
    candidate = row(); image_fixture(build, tmp_path, [candidate])
    monkeypatch.setattr(build, "load_detector", lambda *a, **k: SimpleNamespace(detect=lambda image: []))
    if failure == "decode":
        (tmp_path / build._relative_file(candidate)).write_bytes(b"not an image")
    else:
        candidate["width"] += 1
    with pytest.raises(ValueError, match="cannot decode|decoded size"):
        build.build_manifest([candidate], tmp_path, Path("fake.onnx"), None, 1, 1)


def test_empty_manifest_never_loads_ship_model(build, tmp_path, monkeypatch):
    monkeypatch.setattr(build, "load_detector", lambda *a, **k: SimpleNamespace(detect=lambda image: []))
    records, stats = build.build_manifest([], tmp_path, Path("fake.onnx"), Path("fake-embed"), 1, 1)
    assert records == [] and stats["detections_by_class"] == {} and stats["ship_typing"] == "disabled"


def test_ship_classifier_only_falls_back_for_missing_prompt_cache(tmp_path, monkeypatch):
    build = load()
    calls = []
    monkeypatch.setattr(clip, "ClipEmbedder", lambda *a, **kw: calls.append(kw) or kw["backend"])
    def classifier(embedder):
        if embedder == "ort":
            raise RuntimeError("prompts missing from cache")
        return "fake classifier"
    monkeypatch.setattr(ship, "ShipTypeClassifier", classifier)
    assert build._ship_classifier(tmp_path, 3) == "fake classifier"
    assert [c["backend"] for c in calls] == ["ort", "torch"]
    monkeypatch.setattr(ship, "ShipTypeClassifier", lambda embedder: (_ for _ in ()).throw(RuntimeError("unrelated failure")))
    with pytest.raises(RuntimeError, match="unrelated failure"):
        build._ship_classifier(tmp_path, 3)


def test_review_cli_publishes_local_manifest_and_runs_guard_first(build, tmp_path, monkeypatch, capsys):
    selection = tmp_path / "selection.jsonl"; selection.write_text(json.dumps(row()) + "\n")
    splits = tmp_path / "splits.yaml"; splits.write_text("{}")
    events = []
    monkeypatch.setattr(build, "download_images", lambda *a: events.append("download"))
    def manifest(rows, images, detector, embed, threads, limit):
        events.append("manifest"); assert embed is None and threads == 2
        return [{"image_id": rows[0]["image_id"]}], {"ship_typing": "disabled"}
    monkeypatch.setattr(build, "build_manifest", manifest)
    out = tmp_path / "output"
    args = ["--selection", str(selection), "--splits", str(splits), "--output", str(out), "--skip-ship-typing", "--threads", "2"]
    assert build.main(args) == 0
    assert events == ["download", "manifest"]
    assert json.loads((out / "manifest.json").read_text()) == [{"image_id": "clip/frame-1"}]
    assert json.loads(capsys.readouterr().out)["images"] == 1
    selection.write_text(json.dumps(row(eval_split="test")) + "\n")
    events.clear()
    with pytest.raises(ValueError, match="not 'dev'"):
        build.main(args)
    assert events == []
