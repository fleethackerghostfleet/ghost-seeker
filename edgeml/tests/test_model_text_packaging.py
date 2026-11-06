import importlib.util
from pathlib import Path

import pytest


SPEC = importlib.util.spec_from_file_location("prepare", Path(__file__).resolve().parents[2] / "deploy/models/prepare.py")
prepare = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(prepare)


def test_missing_text_files_leave_image_only_context(tmp_path):
    # Real catalog entries are small and pinned; no model weights needed for this test.
    repo = Path(__file__).resolve().parents[2]
    sums = tmp_path / "sums"
    sums.write_text("\n".join(x for x in (repo / "deploy/models/SHA256SUMS").read_text().splitlines() if "catalog/" in x))
    prepare.prepare(tmp_path, sums, tmp_path / "out")
    assert not (tmp_path / "out/mobileclip-s1/text_model.onnx").exists()
    assert "text_model" not in (tmp_path / "out/SHA256SUMS").read_text()


def test_partial_or_tampered_text_assets_fail_before_context_copy(tmp_path):
    d = tmp_path / "mobileclip-s1"
    d.mkdir()
    (d / "text_model.onnx").write_bytes(b"not shipped weights")
    sums = tmp_path / "sums"
    sums.write_text("")
    with pytest.raises(ValueError, match="both"):
        prepare.prepare(tmp_path, sums, tmp_path / "out")
    (d / "tokenizer.json").write_text("{}")
    with pytest.raises(ValueError, match="checksum mismatch"):
        prepare.prepare(tmp_path, sums, tmp_path / "out")
    assert not (tmp_path / "out").exists()
