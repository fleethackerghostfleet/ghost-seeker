import importlib.util
import json
import random
from pathlib import Path

import cv2
import numpy as np
import pytest
import yaml

from edgeml.data import catalog
from edgeml.detectors.labels import agnostic

TOOLS = Path(__file__).resolve().parents[1] / "tools"
spec = importlib.util.spec_from_file_location("validate_data", TOOLS / "validate_data.py")
validate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validate)


@pytest.mark.parametrize("box,expected,reason", [
    ([1, 2, 3, 4], [1, 2, 3, 4], None),
    ([-1, 2, 5, 20], [0, 2, 4, 8], "clip_bounds"),
    ([10, 0, 3, 2], None, "outside_image"),
    ([0, 0, 0, 5], None, "zero_or_negative_area"),
    ([0, 0, -1, 5], None, "zero_or_negative_area"),
    ([float("nan"), 0, 1, 1], None, "nonfinite_box"),
    ([], None, "invalid_box"),
])
def test_box_validation(box, expected, reason):
    assert validate.clean_box(box, 10, 10) == (expected, reason)


def jpeg(seed=0):
    array = np.random.default_rng(seed).integers(0, 256, (16, 24, 3), dtype=np.uint8)
    return cv2.imencode(".jpg", array)[1].tobytes()


def test_image_checks():
    assert not validate.inspect_image(b"not an image")["readable"]
    black = cv2.imencode(".png", np.zeros((12, 16, 3), dtype=np.uint8))[1].tobytes()
    result = validate.inspect_image(black)
    assert result["black"] and result["blank"]
    assert (result["width"], result["height"]) == (16, 12)
    result = validate.inspect_image(jpeg())
    assert not result["blank"]
    assert result["phash"] == validate.inspect_image(jpeg())["phash"]


def test_hash_index_matches_exhaustive_hamming_search():
    rng = random.Random(123)
    values = [rng.getrandbits(63) for _ in range(120)]
    values += [value ^ sum(1 << bit for bit in rng.sample(range(63), 4)) for value in values[:20]]
    index = validate.HashIndex(4)
    seen = set()
    for value in values:
        assert set(index.matches(value)) == {v for v in seen if (v ^ value).bit_count() <= 4}
        index.add(value)
        seen.add(value)


@pytest.mark.parametrize("label,expected", [("small boat", "vessel"), ("drone", "object"),
                                          ("military aircraft", "object"), ("civil aircraft", "object"),
                                          ("shark fin", "object"), ("not a class", None)])
def test_observed_labels(label, expected):
    assert agnostic(label) == expected


def test_raw_xml_checks_do_not_silently_rejoin_mismatched_names(tmp_path):
    repo, _ = policy_repo(tmp_path)
    root = tmp_path / "mirror/raw/ir"
    root.mkdir(parents=True)
    (root / "1.jpg").write_bytes(jpeg())
    (root / "1.xml").write_text('<annotation><filename>other.jpg</filename><object><name>liner</name>'
                                '<bndbox><xmin>-1</xmin><ymin>0</ymin><xmax>12</xmax><ymax>8</ymax>'
                                '</bndbox></object></annotation>')
    audit = validate.Audit(repo, None, tmp_path / "out", None, 4)
    audit.extra_images([root])
    assert audit.inventory[0]["xml_labels"] == 1
    assert {issue["reason"] for issue in audit.issues} == {
        "xml_image_name_mismatch", "xml_clip_bounds", "raw_label_review_required"}
    assert agnostic("liner") == "vessel"


def policy_repo(tmp_path):
    repo = tmp_path / "repo"
    config = repo / "edgeml/configs"
    config.mkdir(parents=True)
    policy = {"test": {"ghost_fleet_5fps": ["Sailboat_on_the_horizon_seg0", "Arriving_In_Norfolk_seg1h10"]},
              "dev": {}, "source_video_aliases": {"Arriving_In_Norfolk_USA_3hr_uncut": "Arriving_In_Norfolk"}}
    (config / "splits.yaml").write_text(yaml.safe_dump(policy))
    return repo, policy


def test_split_source_aliases_and_segments(tmp_path):
    repo, config = policy_repo(tmp_path)
    policy = validate.SplitPolicy(config)
    assert policy.expected("video_sailboat_on_the_horizon", "Sailboat_on_the_horizon") == "test"
    assert policy.expected("video_arriving_in_norfolk_usa_3hr_uncut", "Arriving_In_Norfolk_USA_3hr_uncut") == "test"
    assert policy.expected("seaships7000", "x", "valid") == "dev"
    assert policy.expected("singapore_maritime", "MVI_1474_VIS_frame_000324.jpg", "train") == "unassigned"
    assert validate.source_video("MVI_0790_f00030.jpg") == validate.source_video("MVI_0790")


def test_legacy_clips_lock_all_held_out_recordings():
    assert catalog.clip_split("Sailboat_on_the_horizon") == "test"
    assert catalog.clip_split("Arriving_In_Norfolk_USA_3hr_uncut") == "test"
    assert catalog.clip_split("Submarine_surfacing") == "dev"


def test_fvessel_overlap_is_quarantined():
    config = yaml.safe_load((TOOLS.parent / "configs/splits.yaml").read_text())
    assert validate.SplitPolicy(config).expected("fvessel_clip10", "fvessel_clip05") == "quarantine"
    assert "fvessel_clip05" not in config["dev"]["fvessel_clip10"]
    assert "fvessel_clip07" in config["test"]["fvessel_clip10"]


def test_exact_leak_quarantines_dev_not_test(tmp_path):
    repo, _ = policy_repo(tmp_path)
    audit = validate.Audit(repo, None, tmp_path / "out")
    data = jpeg()
    audit.image("test.jpg", data, "dataset", "a", "test")
    audit.image("dev.jpg", data, "dataset", "b", "dev")
    audit.duplicates()
    assert [r["record"] for r in audit.issues if r["reason"] == "dev_exact_content_leak"] == ["dev.jpg"]


def test_eval_loader_honours_quarantine(monkeypatch):
    def image(key, split):
        return catalog.EvalImage(key, "singapore_maritime", "", "", 24, 16, "train", key, eval_split=split)
    monkeypatch.setattr(catalog, "load_dataset", lambda *a: [image("unsafe", "quarantine"), image("ok", "dev")])
    assert [i.image_id for i in catalog.eval_set("singapore_maritime", "dev", "", "")] == ["ok"]
    assert catalog.eval_set("singapore_maritime", "test", "", "") == []
    monkeypatch.setattr(catalog, "load_dataset", lambda *a: [image("legacy", None), image("clean", "dev")])
    with pytest.raises(ValueError, match="mixed legacy/clean"):
        catalog.eval_set("singapore_maritime", "dev", "", "")


def test_inventory_does_not_double_count_shallow_objects(tmp_path):
    repo, _ = policy_repo(tmp_path)
    inventory = tmp_path / "objects.jsonl"
    inventory.write_text(json.dumps({"name": "catalog/datasets.jsonl", "size": 7}) + "\n")
    audit = validate.Audit(repo, None, tmp_path / "out", inventory)
    audit.object_inventory()
    assert audit.metrics["gcs_prefixes"]["catalog/datasets.jsonl"] == {"objects": 1, "bytes": 7}


def test_smd_rebuild_uses_image_path_not_colliding_image_id(tmp_path):
    pq = pytest.importorskip("pyarrow.parquet")
    pa = pytest.importorskip("pyarrow")
    repo, _ = policy_repo(tmp_path)
    mirror = tmp_path / "mirror"
    raw = mirror / "raw/singapore_maritime/data"
    raw.mkdir(parents=True)
    rows = [{"image_id": 7, "image": {"bytes": jpeg(i), "path": f"MVI_{i}_VIS_frame_000007.jpg"},
             "width": 24, "height": 16, "objects": {"bbox": [[i, 1, 3, 4]], "category": ["Boat"], "id": [i]}}
            for i in (1, 2)]
    pq.write_table(pa.Table.from_pylist(rows), raw / "train-00000-of-00001.parquet")
    output = tmp_path / "clean"
    audit = validate.Audit(repo, mirror, output)
    original = {"dataset_id": "singapore_maritime", "image_id": "singapore_maritime/train/7"}
    audit.images = {("singapore_maritime", original["image_id"]): original}
    audit.repair_smd()
    assert len(audit.images) == 2
    assert len(audit.smd_annotations) == 2
    for i, row in zip((1, 2), audit.smd_annotations):
        assert row["image_id"] == f"singapore_maritime/MVI_{i}_VIS_frame_000007.jpg"
        assert row["box"][0] == i
    assert (output / "datasets/singapore_maritime/images/MVI_1_VIS_frame_000007.jpg").read_bytes() == jpeg(1)
    assert len(audit.changes) == 5


def test_smd_rebuild_refuses_duplicate_paths(tmp_path):
    pq = pytest.importorskip("pyarrow.parquet")
    pa = pytest.importorskip("pyarrow")
    repo, _ = policy_repo(tmp_path)
    mirror = tmp_path / "mirror"
    raw = mirror / "raw/singapore_maritime/data"
    raw.mkdir(parents=True)
    row = {"image_id": 1, "image": {"bytes": jpeg(), "path": "same.jpg"}, "width": 24, "height": 16,
           "objects": {"bbox": [], "category": [], "id": []}}
    pq.write_table(pa.Table.from_pylist([row, row]), raw / "train-0.parquet")
    audit = validate.Audit(repo, mirror, tmp_path / "output")
    with pytest.raises(ValueError, match="duplicate source image.path"):
        audit.repair_smd()


def test_write_jsonl_never_overwrites(tmp_path):
    path = tmp_path / "records.jsonl"
    validate.write_jsonl(path, [{"x": 1}])
    with pytest.raises(FileExistsError):
        validate.write_jsonl(path, [{"x": 2}])
    assert list(validate.read_jsonl(path)) == [{"x": 1}]
