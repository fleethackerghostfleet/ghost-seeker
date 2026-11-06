import io
import math
import sys
from types import SimpleNamespace as NS

import numpy as np
import pytest

from edgeml.runtime import backends


@pytest.mark.parametrize("engine,path,expected", [("auto", "model.onnx", "ort"), ("auto", "model.xml", "ov"), ("ort", "model.xml", "ort"), ("openvino", "directory", "ov")])
def test_session_dispatch(monkeypatch, engine, path, expected):
    calls = []
    monkeypatch.setattr(backends, "OrtSession", lambda p, **kw: calls.append(("ort", p, kw)))
    monkeypatch.setattr(backends, "OvSession", lambda p, **kw: calls.append(("ov", p, kw)))
    backends.open_session(path, engine, threads=2, precision="int8", providers=["CPUExecutionProvider"], device="TEST")
    assert calls[0][:2] == (expected, path)
    assert calls[0][2] == ({"threads": 2, "precision": "int8", "providers": ["CPUExecutionProvider"]} if expected == "ort" else {"threads": 2, "precision": "int8", "device": "TEST"})
    with pytest.raises(ValueError, match="unsupported"):
        backends.open_session(path, "unsupported")


@pytest.mark.parametrize("provider,tag", [("CPUExecutionProvider", "ort-fp16"), ("CoreMLExecutionProvider", "ort-coreml-fp16")])
def test_ort_configuration_and_inference(tmp_path, monkeypatch, provider, tag):
    path = tmp_path / "fake.onnx"
    path.write_bytes(b"fake")
    calls = []
    output = np.array([[7]], dtype=np.float32)

    class Session:
        def __init__(self, path_arg, options, providers):
            calls.append((path_arg, options, providers))

        def get_providers(self):
            return [provider]

        def get_inputs(self):
            return [NS(name="pixels", shape=[1, 3, 8, 8])]

        def get_outputs(self):
            return [NS(name="boxes")]

        def run(self, outputs, feed):
            assert outputs == ["boxes"]
            assert feed["pixels"] is image
            return [output]

    fake = NS(get_available_providers=lambda: [provider], SessionOptions=NS, ExecutionMode=NS(ORT_SEQUENTIAL="sequential"), GraphOptimizationLevel=NS(ORT_ENABLE_ALL="all"), InferenceSession=Session)
    monkeypatch.setitem(sys.modules, "onnxruntime", fake)
    monkeypatch.setenv("EDGEML_ORT_PROVIDERS", f" {provider}, , ")
    session = backends.OrtSession(path, threads=3, precision="fp16")
    assert session.name == tag and session.model_bytes == 4
    assert session.input_shape == [1, 3, 8, 8]
    assert calls[0][0] == str(path) and calls[0][2] == [provider]
    options = calls[0][1]
    assert (options.intra_op_num_threads, options.inter_op_num_threads, options.execution_mode, options.graph_optimization_level) == (3, 1, "sequential", "all")
    image = np.zeros((1, 3, 8, 8), np.float32)
    assert session.run(image)[0] is output
    with pytest.raises(RuntimeError, match="not available"):
        backends.OrtSession(path, providers=["MissingExecutionProvider"])
    assert len(calls) == 1


@pytest.mark.parametrize("precision,hint,static", [("fp32", "f32", True), ("fp16", "f32", False), ("bf16", "bf16", True), ("int8", "f32", True)])
def test_openvino_configuration_and_owned_outputs(tmp_path, monkeypatch, precision, hint, static):
    path = tmp_path / "model.xml"
    path.write_bytes(b"xml")
    path.with_suffix(".bin").write_bytes(b"weights")
    calls = []
    buffers = [np.array([1.0]), np.array([2.0])]
    shape = NS(is_static=static, to_shape=lambda: (1, 3, 8, 8))
    port = NS(get_any_name=lambda: "pixels", get_partial_shape=lambda: shape)
    request = NS(infer=lambda feed: calls.append(("infer", feed)), get_output_tensor=lambda i: NS(data=buffers[i]))
    compiled = NS(inputs=[port], outputs=["boxes", "scores"], create_infer_request=lambda: request)
    core = NS(read_model=lambda p: calls.append(("read", p)) or "model", compile_model=lambda m, d, c: calls.append((m, d, c)) or compiled)
    monkeypatch.setitem(sys.modules, "openvino", NS(Core=lambda: core))
    session = backends.OvSession(path, threads=2, precision=precision, device="TEST")
    assert session.input_shape == ([1, 3, 8, 8] if static else None)
    assert session.model_bytes == 10 and session.name == f"ov-{precision}"
    assert calls[1] == ("model", "TEST", {"INFERENCE_NUM_THREADS": 2, "NUM_STREAMS": 1, "PERFORMANCE_HINT": "LATENCY", "INFERENCE_PRECISION_HINT": hint})
    image = np.zeros((1, 3, 8, 8), np.float32)
    result = session.run(image)
    assert calls[-1][1]["pixels"] is image
    np.testing.assert_array_equal(result, [[1.0], [2.0]])
    result[0][0] = 99
    assert buffers[0][0] == 1.0


def test_model_size_and_provider_configuration(tmp_path, monkeypatch):
    (tmp_path / "nested").mkdir()
    (tmp_path / "nested" / "weights.bin").write_bytes(b"12345")
    (tmp_path / "model.xml").write_bytes(b"xml")
    assert backends._dir_or_file_bytes(tmp_path) == 8
    assert backends._dir_or_file_bytes(tmp_path / "model.xml") == 3
    with pytest.raises(FileNotFoundError):
        backends._dir_or_file_bytes(tmp_path / "missing")
    monkeypatch.delenv("EDGEML_ORT_PROVIDERS", raising=False)
    assert backends.ort_providers() == ["CPUExecutionProvider"]
    monkeypatch.setenv("EDGEML_ORT_PROVIDERS", " CoreMLExecutionProvider, ,CPUExecutionProvider ")
    assert backends.ort_providers() == ["CoreMLExecutionProvider", "CPUExecutionProvider"]


@pytest.mark.parametrize("shape,pad", [((2, 4), (0, 2)), ((4, 2), (2, 0))])
def test_letterbox_preserves_channels_and_centers_image(shape, pad):
    image = np.full((*shape, 3), [7, 11, 13], dtype=np.uint8)
    canvas, scale, offset = backends.letterbox(image, 8, pad_value=99)
    assert scale == 2 and offset == pad and canvas.shape == (8, 8, 3)
    x, y = pad
    np.testing.assert_array_equal(canvas[y:y + shape[0] * 2, x:x + shape[1] * 2], np.tile([7, 11, 13], (shape[0] * 2, shape[1] * 2, 1)))
    assert np.all(canvas[0, 0] == 99)


def test_nms_orders_suppresses_and_handles_empty_and_degenerate_boxes():
    boxes = np.array([[0, 0, 10, 10], [1, 1, 11, 11], [20, 20, 30, 30], [4, 4, 4, 4]])
    scores = np.array([0.8, 0.9, 0.7, 0.6])
    np.testing.assert_array_equal(backends.nms_xyxy(boxes, scores, 0.5), [1, 2, 3])
    np.testing.assert_array_equal(backends.nms_xyxy(boxes, scores, 1.0), [1, 0, 2, 3])
    empty = backends.nms_xyxy(np.empty((0, 4)), np.empty(0), 0.5)
    assert empty.shape == (0,) and empty.dtype == np.int64
    for value in ([1], (1,)):
        assert backends.as_sequence(value) is value
    assert backends.as_sequence(7) == [7]


def test_memory_measurement_and_proc_fallback(monkeypatch):
    monkeypatch.setitem(sys.modules, "psutil", NS(Process=lambda pid: NS(memory_info=lambda: NS(rss=3 * 2**20))))
    assert backends.rss_mb() == 3
    monkeypatch.setitem(sys.modules, "psutil", None)
    monkeypatch.setattr(backends, "open", lambda path: io.StringIO("Name: test\nVmRSS: 2048 kB\n"), raising=False)
    assert backends.rss_mb() == 2
    monkeypatch.setattr(backends, "open", lambda path: io.StringIO("Name: test\n"))
    assert math.isnan(backends.rss_mb())
    monkeypatch.setitem(sys.modules, "resource", NS(RUSAGE_SELF=0, getrusage=lambda who: NS(ru_maxrss=2048)))
    assert backends.peak_rss_mb() == 2
