import hashlib
import json

import pytest

from edgeml.sidecar.integrity import verify_models


def manifest_for(root, files):
    manifest = root / "manifest.json"
    entries = [{"path": str(path.relative_to(root)), "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                "source": "https://models.example/approved", "revision": "a" * 40} for path in files]
    manifest.write_text(json.dumps({"schema_version": 1, "artifacts": entries}))
    return manifest


@pytest.fixture
def bundle(tmp_path):
    directory = tmp_path / "detector"
    directory.mkdir()
    weights = directory / "model.bin"
    weights.write_bytes(b"approved model")
    meta = directory / "meta.json"
    meta.write_text('{}')
    return directory, weights, meta, manifest_for(tmp_path, [weights, meta])


def test_approved_bundle_and_model_free_empty_case(bundle):
    directory, weights, _, manifest = bundle
    verify_models([directory, weights], manifest)
    verify_models([], None)


def test_manifest_required_before_loading(bundle):
    with pytest.raises(ValueError, match="model-manifest is required"):
        verify_models([bundle[1]], None)


@pytest.mark.parametrize("field,value,message", [
    ("revision", "main", "immutable source revision"),
    ("sha256", "bad", "invalid SHA-256"),
    ("sha256", "0" * 64, "SHA-256 mismatch"),
    ("source", "", "source provenance"),
    ("path", "../outside", "unsafe model artifact path"),
])
def test_invalid_or_mutable_provenance_fails(bundle, field, value, message):
    _, weights, _, manifest = bundle
    data = json.loads(manifest.read_text())
    data["artifacts"][0][field] = value
    manifest.write_text(json.dumps(data))
    with pytest.raises(ValueError, match=message):
        verify_models([weights], manifest)


def test_metadata_and_auxiliary_files_must_be_approved(bundle):
    directory, weights, meta, manifest = bundle
    manifest = manifest_for(manifest.parent, [weights])
    with pytest.raises(ValueError, match="unapproved model artifact.*meta.json"):
        verify_models([directory], manifest)
    manifest_for(manifest.parent, [weights, meta])
    meta.write_text('{"tampered": true}')
    with pytest.raises(ValueError, match="SHA-256 mismatch.*meta.json"):
        verify_models([weights], manifest)


def test_missing_duplicate_empty_malformed_and_outside_artifacts(bundle, tmp_path):
    _, weights, _, manifest = bundle
    with pytest.raises(FileNotFoundError):
        verify_models([tmp_path / "missing.bin"], manifest)
    with pytest.raises(ValueError, match="outside manifest bundle"):
        verify_models([tmp_path.parent], manifest)
    data = json.loads(manifest.read_text())
    data["artifacts"].append(data["artifacts"][0])
    manifest.write_text(json.dumps(data))
    with pytest.raises(ValueError, match="duplicate model artifact"):
        verify_models([weights], manifest)
    data["artifacts"] = []
    manifest.write_text(json.dumps(data))
    with pytest.raises(ValueError, match="nonempty artifacts"):
        verify_models([weights], manifest)
    manifest.write_text("{")
    with pytest.raises(json.JSONDecodeError):
        verify_models([weights], manifest)


def test_symlink_not_accepted(bundle):
    directory, weights, _, manifest = bundle
    (directory / "alias.bin").symlink_to(weights)
    with pytest.raises(ValueError, match="cannot be a symlink"):
        verify_models([weights], manifest)


def test_onnx_external_data_must_be_hash_approved(tmp_path):
    import onnx

    tensor = onnx.TensorProto(name="weights", data_type=onnx.TensorProto.FLOAT, dims=[1])
    tensor.data_location = onnx.TensorProto.EXTERNAL
    tensor.external_data.add(key="location", value="weights.bin")
    graph = onnx.helper.make_graph([], "fixture", [], [], initializer=[tensor])
    model = tmp_path / "model.onnx"
    onnx.save_model(onnx.helper.make_model(graph), model)
    weights = tmp_path / "weights.bin"
    weights.write_bytes(b"\0\0\0\0")
    manifest = manifest_for(tmp_path, [model, weights])
    verify_models([model], manifest)
    manifest = manifest_for(tmp_path, [model])
    with pytest.raises(ValueError, match="unapproved ONNX external data"):
        verify_models([model], manifest)
