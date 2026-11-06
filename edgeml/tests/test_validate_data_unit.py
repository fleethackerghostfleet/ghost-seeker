import hashlib
import importlib.util
import json
import sys
import zipfile
from pathlib import Path

import cv2
import numpy as np
import pytest
import yaml

from edgeml.data.catalog import BUCKET


REPO = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("p1_validate_data", REPO / "edgeml/tools/validate_data.py")
validate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validate)


def jpeg(seed=0):
    array = np.random.default_rng(seed).integers(0, 256, (16, 24, 3), dtype=np.uint8)
    ok, encoded = cv2.imencode(".jpg", array)
    assert ok
    return encoded.tobytes()


def write_jsonl(path, rows):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("\n".join(json.dumps(row) for row in rows) + "\n")


def policy_repo(tmp_path):
    repo = tmp_path / "repo"
    config = repo / "edgeml/configs"
    config.mkdir(parents=True)
    (config / "splits.yaml").write_text(yaml.safe_dump({
        "dev": {"onboard": ["MVI_0790"]}, "test": {"onboard": ["MVI_0801", "locked"]}}))
    return repo


def test_repository_scans_real_archives_imagery_qa_and_scenario_provenance(tmp_path):
    repo = policy_repo(tmp_path)
    frames = repo / "frames"
    frames.mkdir()
    data = jpeg()
    with zipfile.ZipFile(frames / "MVI_frames.zip", "w", compression=zipfile.ZIP_STORED) as archive:
        archive.writestr("directory/", b"")
        archive.writestr("directory/MVI_0790_f00001.jpg", data)
        archive.writestr("notes.txt", "source metadata")
    with zipfile.ZipFile(frames / "locked_frames.zip", "w") as archive:
        archive.writestr("000001.jpg", jpeg(1))
    (frames / "broken.zip").write_bytes(b"not a zip archive")
    imagery = repo / "web/static/imagery"
    imagery.mkdir(parents=True)
    for name, seed in (("good", 2), ("unlicensed", 3), ("orphan", 4)):
        (imagery / f"{name}.jpg").write_bytes(jpeg(seed))
    manifest = [
        {"file": "imagery/good.jpg", "src": "fixture", "license": "CC0", "dets": [
            {"cls": "boat", "b": {"x": 1, "y": 2, "w": 3, "h": 4}}]},
        {"file": "imagery/unlicensed.jpg", "dets": [
            {"cls": "not a class", "b": {"x": -1, "y": 2, "w": 3, "h": 4}}]},
        {"file": "imagery/missing.jpg"},
    ]
    (repo / "internal/catalog").mkdir(parents=True)
    (repo / "internal/catalog/imagery.json").write_text(json.dumps(manifest))
    qa = repo / "edgeml/eval_qa"
    qa.mkdir()
    (qa / "valid.yaml").write_text("sequences: []\n")
    (qa / "invalid.yaml").write_text("- not a mapping\n")
    write_jsonl(qa / "index.jsonl", [
        {"set": "video_fixture", "seq_id": "fixture", "split": "dev", "source_dataset": "fixture", "license": "CC0"},
        {"set": "video_fixture", "seq_id": "locked", "split": "dev"},
    ])
    scenarios = repo / "scenarios"
    scenarios.mkdir()
    (scenarios / "local.jpg").write_bytes(data)
    (scenarios / "scenario.json").write_text(json.dumps({"sets": [
        {"path": "local.jpg", "source": "fixture", "license": "CC0", "label": "boat"},
        {"Path": "missing.jpg", "Label": "not a class"},
    ]}))
    audit = validate.Audit(repo, None, tmp_path / "output")
    audit.repository()
    archives = {row["dataset"]: row for row in audit.inventory if row["dataset"].startswith("archive:")}
    assert archives["archive:MVI_frames.zip"]["frames"] == 1
    assert archives["archive:MVI_frames.zip"]["unpacked_bytes"] == len(data) + len("source metadata")
    assert archives["archive:broken.zip"]["frames"] == 0
    scanned = {row["ref"]: row for row in audit.frames}
    assert scanned["frames/MVI_frames.zip:directory/MVI_0790_f00001.jpg"]["eval_split"] == "dev"
    assert scanned["frames/locked_frames.zip:000001.jpg"]["eval_split"] == "test"
    assert scanned["imagery/good.jpg"]["readable"]
    assert (scanned["imagery/good.jpg"]["width"], scanned["imagery/good.jpg"]["height"]) == (24, 16)
    reasons = {row["reason"] for row in audit.issues}
    assert {"corrupt_archive", "missing_image", "missing_source", "missing_synthetic_license", "clip_bounds",
            "unmapped_label", "unreferenced_synthetic_image", "invalid_qa_yaml", "missing_license",
            "missing_source_dataset", "qa_split_mismatch", "scenario_mixed_case_keys", "scenario_input_not_local"} <= reasons
    assert not [row for row in audit.issues if row["record"] == "imagery/good.jpg"]
    assert audit.metrics["qa_yaml_files"] == 2 and audit.metrics["qa_index_sequences"] == 2


def test_repository_reports_crc_failure_from_otherwise_valid_zip(tmp_path):
    repo = policy_repo(tmp_path)
    (repo / "frames").mkdir()
    (repo / "internal/catalog").mkdir(parents=True)
    (repo / "internal/catalog/imagery.json").write_text("[]")
    path = repo / "frames/crc.zip"
    with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_STORED) as archive:
        archive.writestr("good.jpg", jpeg(1))
        archive.writestr("bad.jpg", jpeg(2))
    with zipfile.ZipFile(path) as archive:
        info = archive.getinfo("bad.jpg")
        assert archive.read("good.jpg") == jpeg(1)
        assert archive.read("bad.jpg") == jpeg(2)
    raw = bytearray(path.read_bytes())
    offset = info.header_offset + 30 + len(info.filename.encode()) + len(info.extra)
    raw[offset] ^= 1
    path.write_bytes(raw)
    with zipfile.ZipFile(path) as archive:
        assert archive.namelist() == ["good.jpg", "bad.jpg"]
        with pytest.raises(zipfile.BadZipFile, match="CRC"):
            archive.read("bad.jpg")
    audit = validate.Audit(repo, None, tmp_path / "output")
    audit.repository()
    assert [(issue["reason"], issue["record"]) for issue in audit.issues] == [("corrupt_zip_member", "crc.zip:bad.jpg")]
    assert [row["ref"] for row in audit.frames] == ["frames/crc.zip:good.jpg"]
    assert audit.inventory[0]["frames"] == 1


def test_catalog_audit_decodes_files_repairs_metadata_and_cleans_annotations(tmp_path):
    repo = policy_repo(tmp_path)
    mirror = tmp_path / "mirror"
    ds = "seaships7000"
    rows = []
    for index, name in enumerate(("a", "b", "c", "d", "missing")):
        relative = f"datasets/{ds}/images/train/{name}.jpg"
        data = jpeg(index)
        if name == "c":
            ok, encoded = cv2.imencode(".jpg", np.zeros((16, 24, 3), dtype=np.uint8))
            assert ok
            data = encoded.tobytes()
        if name != "missing":
            path = mirror / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        row = {"dataset_id": ds, "image_id": f"{ds}/{name}", "gcs_uri": f"{BUCKET}/{relative}",
               "width": 24, "height": 16, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest(),
               "source_ref": "" if name == "b" else f"movie_{name}", "split": "train", "has_annotations": name == "d"}
        if name == "a":
            row.update(width=1, height=1, bytes=0, sha256="stale")
        rows.append(row)
    dataset = {"dataset_id": ds, "license": "CC0", "source_url": "https://example.invalid/fixture",
               "num_images": 5, "num_annotations": 0, "bytes": 0}
    write_jsonl(mirror / "catalog/datasets.jsonl", [dataset])
    write_jsonl(mirror / f"catalog/images/{ds}.jsonl", rows)
    def annotation(name="a", **fields):
        return {"dataset_id": ds, "image_id": f"{ds}/{name}", "label": "boat", "box": [1, 2, 3, 4], **fields}
    horizon = {"X": 0, "Y": 4, "Nx": 0, "Ny": 1}
    write_jsonl(mirror / f"catalog/annotations/{ds}.jsonl", [
        annotation(label_norm="wrong"), annotation(label_norm="vessel"),
        annotation(box=[-1, 1, 5, 3]), annotation(box=[0, 0, 0, 4]),
        annotation(label="not a class", box=[1, 5, 3, 4]),
        annotation(label="horizon", attrs=json.dumps(horizon)),
        annotation(label="horizon", attrs={"X": "invalid"}),
        annotation("missing"), annotation("c"),
    ])
    before = {str(path.relative_to(mirror)): path.read_bytes() for path in mirror.rglob("*") if path.is_file()}
    output = tmp_path / "output"
    audit = validate.Audit(repo, mirror, output)
    audit.load_catalog()
    audit.cloud_frames()
    audit.duplicates()
    audit.clean_catalog()
    cleaned = {row["image_id"]: row for row in validate.read_jsonl(output / f"catalog/images/{ds}.jsonl")}
    assert set(cleaned) == {f"{ds}/{name}" for name in ("a", "b", "d")}
    assert (cleaned[f"{ds}/a"]["width"], cleaned[f"{ds}/a"]["height"]) == (24, 16)
    assert cleaned[f"{ds}/a"]["sha256"] == hashlib.sha256(jpeg(0)).hexdigest()
    assert cleaned[f"{ds}/a"]["has_annotations"] and cleaned[f"{ds}/a"]["eval_split"] == "dev"
    assert cleaned[f"{ds}/b"]["eval_split"] == "quarantine"
    assert cleaned[f"{ds}/d"]["has_annotations"] is False
    annotations = list(validate.read_jsonl(output / f"catalog/annotations/{ds}.jsonl"))
    assert len(annotations) == 3
    assert [(row["box"], row["label_norm"]) for row in annotations if row["label"] != "horizon"] == [
        ([1, 2, 3, 4], "vessel"), ([0, 1, 4, 3], "vessel")]
    assert [row["attrs"] for row in annotations if row["label"] == "horizon"] == [json.dumps(horizon)]
    counts = list(validate.read_jsonl(output / "catalog/datasets.jsonl"))
    assert counts[0]["num_images"] == 3 and counts[0]["num_annotations"] == 3
    assert counts[0]["bytes"] == sum(row["bytes"] for row in cleaned.values())
    reasons = {issue["reason"] for issue in audit.issues}
    assert {"catalog_width_mismatch", "catalog_height_mismatch", "catalog_bytes_mismatch", "catalog_sha256_mismatch",
            "missing_source_ref", "catalog_image_not_scanned", "duplicate_annotation", "invalid_horizon",
            "unmapped_label", "zero_or_negative_area", "orphan_or_quarantined_annotation"} <= reasons
    assert {str(path.relative_to(mirror)): path.read_bytes() for path in mirror.rglob("*") if path.is_file()} == before


def test_catalog_loading_reports_duplicate_ids_and_unknown_provenance(tmp_path):
    repo = policy_repo(tmp_path)
    mirror = tmp_path / "mirror"
    data = jpeg()
    path = mirror / "datasets/fixture/images/same.jpg"
    path.parent.mkdir(parents=True)
    path.write_bytes(data)
    write_jsonl(mirror / "catalog/datasets.jsonl", [{"dataset_id": "fixture", "license": "unknown", "source_url": "",
                                                 "num_images": 1, "num_annotations": 0, "bytes": len(data)}])
    row = {"dataset_id": "fixture", "image_id": "same", "gcs_uri": f"{BUCKET}/datasets/fixture/images/same.jpg",
           "width": 24, "height": 16, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest(),
           "source_ref": "source.jpg", "split": "train", "has_annotations": False}
    write_jsonl(mirror / "catalog/images/fixture.jsonl", [row, row])
    audit = validate.Audit(repo, mirror, tmp_path / "output")
    audit.load_catalog()
    assert audit.images == {("fixture", "same"): row}
    assert {issue["reason"] for issue in audit.issues} == {"unknown_license", "unknown_source_url", "duplicate_image_id"}


@pytest.mark.parametrize("reverse", [False, True])
def test_duplicate_canonical_selection_is_stable_across_actual_arrival_orders(tmp_path, reverse):
    repo = policy_repo(tmp_path)
    audit = validate.Audit(repo, None, tmp_path / "output")
    rows = [("test-a.jpg", "A", "test"), ("test-b.jpg", "A", "test"), ("dev.jpg", "A", "dev"), ("copy.jpg", "B", "dev")]
    if reverse:
        rows.reverse()
    data = jpeg()
    for ref, dataset, split in rows:
        audit.image(ref, data, dataset, "source", split)
    audit.duplicates()
    within = [row for row in audit.issues if row["reason"] == "exact_duplicate_within_dataset"]
    assert [(row["record"], row["canonical"]) for row in within] == [("dev.jpg", "test-a.jpg")]
    locked = [row for row in audit.issues if row["reason"] == "exact_duplicate_locked_test"]
    assert [(row["record"], row["canonical"]) for row in locked] == [("test-b.jpg", "test-a.jpg")]
    assert {row["record"] for row in audit.issues if row["reason"] == "dev_exact_content_leak"} == {"dev.jpg", "copy.jpg"}


@pytest.mark.parametrize("reverse", [False, True])
def test_hamming_candidates_sort_by_distance_then_value_for_both_insert_orders(reverse):
    values = [0, 1, 2, 3, 4, 7]
    if reverse:
        values.reverse()
    index = validate.HashIndex(2)
    for value in values:
        index.add(value)
    assert index.matches(0) == [0, 1, 2, 4, 3]
    for radius in (-1, 9):
        with pytest.raises(ValueError, match="between 0 and 8"):
            validate.HashIndex(radius)


@pytest.mark.parametrize("empty", [False, True])
def test_extra_only_cli_writes_real_audit_and_refuses_existing_output(tmp_path, monkeypatch, empty):
    root = tmp_path / "raw/images"
    root.mkdir(parents=True)
    if not empty:
        (root / "good.jpg").write_bytes(jpeg())
        (root / "unreadable.jpg").write_bytes(b"invalid image payload")
        ok, black = cv2.imencode(".png", np.zeros((16, 24, 3), dtype=np.uint8))
        assert ok
        (root / "black.png").write_bytes(black.tobytes())
    before = {path.name: path.read_bytes() for path in root.iterdir()}
    inventory = tmp_path / "objects.jsonl"
    write_jsonl(inventory, [{"name": "datasets/demo/a.jpg", "size": 13}, {"name": "datasets/demo/b.jpg", "size": 0}])
    output = tmp_path / "audit"
    monkeypatch.setattr(sys, "argv", ["validate_data.py", "--repo", str(REPO), "--output", str(output),
                                    "--extra-only", "--image-root", str(root), "--object-inventory", str(inventory)])
    validate.main()
    report = json.loads((output / "audit.json").read_text())
    assert report["scanned_images"] == (0 if empty else 3)
    assert report["gcs_prefixes"]["datasets/demo"] == {"objects": 2, "bytes": 13}
    assert report["zero_byte_objects"] == 1
    assert report["object_inventory_sha256"] == hashlib.sha256(inventory.read_bytes()).hexdigest()
    assert len(report["repo_commit"]) == 40
    selection = {Path(row["ref"]).name: row["status"] for row in validate.read_jsonl(output / "selection.jsonl")}
    assert selection == ({} if empty else {"good.jpg": "keep", "unreadable.jpg": "quarantine", "black.png": "quarantine"})
    assert (output / "input_splits.yaml").read_bytes() == (REPO / "edgeml/configs/splits.yaml").read_bytes()
    original_report = (output / "audit.json").read_bytes()
    with pytest.raises(FileExistsError):
        validate.main()
    assert (output / "audit.json").read_bytes() == original_report
    assert {path.name: path.read_bytes() for path in root.iterdir()} == before


@pytest.mark.parametrize("flags,message", [
    (["--repair-smd"], "requires --mirror"),
    (["--extra-only"], "needs --image-root"),
    (["--image-root", "missing-directory"], "must be an existing directory"),
])
def test_cli_validation_rejects_invalid_modes_before_creating_output(tmp_path, monkeypatch, capsys, flags, message):
    output = tmp_path / "output"
    flags = [str(tmp_path / flag) if flag == "missing-directory" else flag for flag in flags]
    monkeypatch.setattr(sys, "argv", ["validate_data.py", "--output", str(output), *flags])
    with pytest.raises(SystemExit) as exc:
        validate.main()
    assert exc.value.code == 2
    assert message in capsys.readouterr().err
    assert not output.exists()
