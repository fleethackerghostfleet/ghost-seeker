import json
import sys
from contextlib import nullcontext
from types import SimpleNamespace

import numpy as np
import pytest

from edgeml.embed.clip_embedder import ClipEmbedder
from edgeml.runtime import backends


def cpu_embedder(tmp_path, monkeypatch, engine="ort"):
    meta = {"mean": [0.1, 0.2, 0.3], "std": [0.2, 0.4, 0.5], "input_size": 4, "dim": 2}
    (tmp_path / "meta.json").write_text(json.dumps(meta))
    inputs, opened = [], []

    def run(batch):
        inputs.append(batch.copy())
        return [np.array([[3.0, 4.0]] if batch.mean() < 0 else [[0.0, 5.0]], dtype=np.float32)]

    session = SimpleNamespace(model_bytes=123, run=run)

    def open_session(path, **kwargs):
        opened.append((path, kwargs))
        return session

    def forbid_weights(*args, **kwargs):
        raise AssertionError("real model loading is forbidden")

    monkeypatch.setitem(sys.modules, "open_clip", SimpleNamespace(create_model_and_transforms=forbid_weights))
    monkeypatch.setattr(backends, "open_session", open_session)
    embedder = ClipEmbedder(backend=engine, onnx_dir=tmp_path, threads=2, precision="fp16")
    return embedder, session, inputs, opened


@pytest.mark.parametrize("engine,filename", [("ort", "model.onnx"), ("openvino", "model.xml")])
def test_cpu_backend_loads_metadata_and_forwards_session_configuration(tmp_path, monkeypatch, engine, filename):
    embedder, session, inputs, opened = cpu_embedder(tmp_path, monkeypatch, engine)
    assert opened == [(tmp_path / filename, {"engine": engine, "threads": 2, "precision": "fp16"})]
    assert not (tmp_path / filename).exists()
    assert embedder.model is None and embedder.tokenizer is None
    assert embedder._session is session and embedder.model_bytes == 123
    assert embedder.size == 4 and embedder.dim == 2
    assert embedder.name == f"mobileclip-s1-{engine}-fp16"
    assert inputs == []


@pytest.mark.parametrize("shape", [(4, 8), (8, 4), (8, 8), (2, 2)])
def test_preprocessing_center_crops_resizes_and_normalizes_bgr_to_rgb(tmp_path, monkeypatch, shape):
    embedder, _, _, _ = cpu_embedder(tmp_path, monkeypatch)
    image = np.full((*shape, 3), [10, 20, 30], dtype=np.uint8)
    if shape == (4, 8):
        image[:, :2] = image[:, 6:] = 255
    if shape == (8, 4):
        image[:2] = image[6:] = 255
    original = image.copy()
    batch = embedder.preprocess([image])
    expected = (np.array([30, 20, 10], np.float32) / 255 - embedder.mean) / embedder.std
    assert batch.shape == (1, 3, 4, 4) and batch.dtype == np.float32
    np.testing.assert_allclose(batch[0], np.broadcast_to(expected[:, None, None], (3, 4, 4)), atol=1e-6)
    np.testing.assert_array_equal(image, original)


def test_cpu_embeddings_run_one_crop_per_session_call_and_normalize_without_nan(tmp_path, monkeypatch):
    embedder, session, inputs, _ = cpu_embedder(tmp_path, monkeypatch)
    assert embedder.embed_crops([]).shape == (0, 2) and inputs == []
    assert embedder.last_ms == 0
    crops = [np.zeros((4, 4, 3), np.uint8), np.full((4, 4, 3), 255, np.uint8)]
    result = embedder.embed_crops(crops)
    np.testing.assert_allclose(result, [[0.6, 0.8], [0.0, 1.0]], atol=1e-6)
    assert result.dtype == np.float32
    assert len(inputs) == 2 and all(batch.shape == (1, 3, 4, 4) for batch in inputs)
    assert not np.array_equal(inputs[0], inputs[1])
    assert embedder.last_ms >= 0
    session.run = lambda batch: [np.zeros((1, 2), np.float32)]
    np.testing.assert_array_equal(embedder.embed_crops(crops[:1]), [[0, 0]])


def test_cpu_session_failure_is_not_replaced_with_synthetic_embeddings(tmp_path, monkeypatch):
    embedder, session, _, _ = cpu_embedder(tmp_path, monkeypatch)

    def fail(batch):
        raise RuntimeError("inference failed")

    session.run = fail
    with pytest.raises(RuntimeError, match="inference failed"):
        embedder.embed_crops([np.zeros((4, 4, 3), np.uint8)])


def test_cached_text_preserves_prompt_order_duplicates_and_configured_scale(tmp_path, monkeypatch):
    embedder, _, inputs, _ = cpu_embedder(tmp_path, monkeypatch)
    cache = {"boat": [1, 0], "person": [0, 1], "__logit_scale__": 42.5}
    embedder.text_cache_path.write_text(json.dumps(cache))
    features = embedder.embed_text(["person", "boat", "person"])
    np.testing.assert_array_equal(features, [[0, 1], [1, 0], [0, 1]])
    assert features.dtype == np.float32 and embedder.logit_scale() == 42.5
    del cache["__logit_scale__"]
    embedder.text_cache_path.write_text(json.dumps(cache))
    assert embedder.logit_scale() == 100.0 and inputs == []


@pytest.mark.parametrize("cache", [None, {"known": [1, 0]}])
def test_missing_cached_prompts_report_count_and_only_three_examples(tmp_path, monkeypatch, cache):
    embedder, _, _, _ = cpu_embedder(tmp_path, monkeypatch)
    if cache is not None:
        embedder.text_cache_path.write_text(json.dumps(cache))
    prompts = ["missing-a", "missing-b", "missing-c", "missing-d"]
    if cache is not None:
        prompts.append("known")
    with pytest.raises(RuntimeError) as exc:
        embedder.embed_text(prompts)
    assert "4 prompts missing" in str(exc.value)
    assert "missing-a" in str(exc.value) and "missing-c" in str(exc.value)
    assert "missing-d" not in str(exc.value)


def test_corrupt_cache_and_missing_metadata_errors_propagate(tmp_path, monkeypatch):
    embedder, _, _, _ = cpu_embedder(tmp_path, monkeypatch)
    embedder.text_cache_path.write_text("{broken")
    with pytest.raises(json.JSONDecodeError):
        embedder.embed_text(["boat"])
    with pytest.raises(json.JSONDecodeError):
        embedder.logit_scale()
    with pytest.raises(FileNotFoundError):
        ClipEmbedder(backend="ort", onnx_dir=tmp_path / "missing")
    with pytest.raises(KeyError, match="not-a-model"):
        ClipEmbedder(short="not-a-model", backend="ort", onnx_dir=tmp_path)


def test_torch_adapter_uses_only_fake_model_tensors_and_tokenizer(monkeypatch):
    image_inputs, text_inputs, opened = [], [], []

    class Tensor:
        def __init__(self, values):
            self.values = np.asarray(values, np.float32)
            self.shape = self.values.shape

        def float(self):
            return self

        def numpy(self):
            return self.values

    class Model:
        visual = SimpleNamespace(parameters=lambda: [SimpleNamespace(numel=lambda: 6, element_size=lambda: 4)])
        logit_scale = SimpleNamespace(exp=lambda: SimpleNamespace(item=lambda: 2.0))
        evaluated = False

        def eval(self):
            self.evaluated = True

        def encode_image(self, batch):
            image_inputs.append(batch.copy())
            return Tensor(np.tile([3, 4], (len(batch), 1)))

        def encode_text(self, tokens):
            text_inputs.append(tokens)
            return Tensor(np.tile([0, 5], (len(tokens), 1)))

    model = Model()
    norm = type("Normalize", (), {"mean": [0, 0, 0], "std": [1, 1, 1]})()

    def create(arch, pretrained):
        opened.append((arch, pretrained))
        return model, None, SimpleNamespace(transforms=[norm])

    monkeypatch.setitem(sys.modules, "open_clip", SimpleNamespace(create_model_and_transforms=create, get_tokenizer=lambda arch: lambda texts: list(texts)))
    monkeypatch.setitem(sys.modules, "torch", SimpleNamespace(no_grad=nullcontext, zeros=lambda *shape: np.zeros(shape, np.float32), from_numpy=lambda x: x))
    embedder = ClipEmbedder(short="mobileclip-s0")
    assert opened == [("MobileCLIP2-S0", "dfndr2b")]
    assert model.evaluated and embedder.dim == 2 and embedder.model_bytes == 24
    assert image_inputs[0].shape == (1, 3, 256, 256)
    result = embedder.embed_crops([np.zeros((2, 2, 3), np.uint8)])
    np.testing.assert_allclose(result, [[0.6, 0.8]], atol=1e-6)
    assert len(image_inputs) == 2
    np.testing.assert_allclose(embedder.embed_text(["boat", "person"]), [[0, 1], [0, 1]], atol=1e-6)
    assert text_inputs == [["boat", "person"]] and embedder.logit_scale() == 2.0
