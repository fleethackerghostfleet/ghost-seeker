import json

import pytest
import yaml

from edgeml.data import catalog


def write_catalog(root, dataset, images, annotations=None):
    for kind, rows in (("images", images), ("annotations", annotations)):
        if rows is None:
            continue
        path = root / kind / f"{dataset}.jsonl"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("\n" + "\n\n".join(json.dumps(row) for row in rows) + "\n")


def image(key, dataset="example", **fields):
    return {"image_id": key, "dataset_id": dataset, "gcs_uri": f"{catalog.BUCKET}/datasets/{dataset}/images/{key}.jpg",
            "width": 24, "height": 16, "split": "train", "source_ref": key, **fields}


@pytest.mark.parametrize("reverse", [False, True])
@pytest.mark.parametrize("serialized_horizon", [False, True])
def test_catalog_loads_real_jsonl_sorted_with_boxes_and_horizons(tmp_path, reverse, serialized_horizon):
    rows = [image("b"), image("a", split=None, source_ref=None)]
    if reverse:
        rows.reverse()
    horizon = {"X": 0, "Y": .4, "Nx": 0, "Ny": 1}
    annotations = [
        {"image_id": "b", "label": "boat", "box": [1, 2, 3, 4]},
        {"image_id": "a", "label": "horizon", "attrs": json.dumps(horizon) if serialized_horizon else horizon},
        {"image_id": "b", "label": "boat", "box": []},
        {"image_id": "missing", "label": "boat", "box": [1, 2, 3, 4]},
    ]
    write_catalog(tmp_path, "example", rows, annotations)
    loaded = catalog.load_dataset("example", str(tmp_path), str(tmp_path / "data"))
    assert [row.image_id for row in loaded] == ["a", "b"]
    assert loaded[0].horizon == horizon
    assert loaded[0].split == "all" and loaded[0].source_ref == ""
    assert loaded[0].boxes == [] and loaded[0].labels == []
    assert loaded[1].boxes == [[1., 2., 3., 4.]] and loaded[1].labels == ["boat"]
    assert loaded[1].local_path == str(tmp_path / "data/datasets/example/images/b.jpg")


@pytest.mark.parametrize("reverse", [False, True])
def test_explicit_splits_quarantine_and_gt_filter_precede_stride(tmp_path, reverse):
    rows = [image(f"d{i:02}", dataset="clean", eval_split="dev") for i in range(1, 5)]
    rows += [image("q00", dataset="clean", eval_split="quarantine"), image("t05", dataset="clean", eval_split="test")]
    if reverse:
        rows.reverse()
    annotations = [{"image_id": key, "label": "boat", "box": [1, 2, 3, 4]} for key in ("d01", "d03", "d04", "t05")]
    write_catalog(tmp_path, "clean", rows, annotations)
    args = ("clean", "dev", str(tmp_path), str(tmp_path / "data"))
    assert [row.image_id for row in catalog.eval_set(*args)] == ["d01", "d02", "d03", "d04"]
    assert [row.image_id for row in catalog.eval_set(*args, stride=2)] == ["d01", "d03"]
    assert [row.image_id for row in catalog.eval_set(*args, stride=2, only_with_gt=True)] == ["d01", "d04"]
    assert [row.image_id for row in catalog.eval_set("clean", "test", *args[2:])] == ["t05"]
    with pytest.raises(AssertionError, match="never subsample test"):
        catalog.eval_set("clean", "test", *args[2:], stride=2)


@pytest.mark.parametrize("dataset,validation", [("seaships7000", "valid"), ("singapore_maritime", "validation")])
def test_upstream_splits_use_real_catalog_without_annotations(tmp_path, dataset, validation):
    write_catalog(tmp_path, dataset, [image("test", dataset=dataset, split="test"), image("valid", dataset=dataset, split=validation),
                                      image("train", dataset=dataset), image("unused", dataset=dataset, split="unknown")])
    args = (str(tmp_path), str(tmp_path / "data"))
    assert [row.image_id for row in catalog.eval_set(dataset, "dev", *args)] == ["train", "valid"]
    assert [row.image_id for row in catalog.eval_set(dataset, "test", *args)] == ["test"]
    assert catalog.eval_set(dataset, "dev", *args, only_with_gt=True) == []


def test_source_video_and_clip_selection_read_real_split_yaml(tmp_path, monkeypatch):
    policy = {"dev": {"smd_vis_onboard_sample": ["MVI_0790"]},
              "test": {"smd_vis_onboard_sample": ["MVI_0801"], "ghost_fleet_clips": ["Locked_Clip"]}}
    split_file = tmp_path / "splits.yaml"
    split_file.write_text(yaml.safe_dump(policy))
    monkeypatch.setattr(catalog, "SPLITS_YAML", split_file)
    write_catalog(tmp_path, "smd_vis_onboard_sample", [
        image("a", dataset="smd_vis_onboard_sample", source_ref="MVI_0790_f00001.jpg"),
        image("b", dataset="smd_vis_onboard_sample", source_ref="MVI_0801_f00001.jpg"),
        image("c", dataset="smd_vis_onboard_sample", source_ref="unassigned.jpg")])
    args = (str(tmp_path), str(tmp_path / "data"))
    assert [row.image_id for row in catalog.eval_set("smd_vis_onboard_sample", "dev", *args)] == ["a"]
    assert [row.image_id for row in catalog.eval_set("smd_vis_onboard_sample", "test", *args)] == ["b"]
    assert catalog.clip_split("Locked_Clip") == "test"
    assert catalog.clip_split("Other_Clip") == "dev"
    write_catalog(tmp_path, "video_locked_clip", [image("frame", dataset="video_locked_clip", source_ref="")])
    frames = catalog.clip_frames("Locked_Clip", *args)
    assert [row.image_id for row in frames] == ["frame"] and frames[0].video is None
    frames[0].gcs_uri = f"{catalog.BUCKET}/MVI_0790_f00000.jpg"
    assert frames[0].video == "MVI_0790"


def test_catalog_rejects_ambiguous_unknown_and_invalid_inputs(tmp_path):
    args = (str(tmp_path), str(tmp_path / "data"))
    write_catalog(tmp_path, "mixed", [image("a", dataset="mixed", eval_split="dev"), image("b", dataset="mixed")])
    with pytest.raises(ValueError, match="mixed legacy/clean"):
        catalog.eval_set("mixed", "dev", *args)
    write_catalog(tmp_path, "unknown", [image("a", dataset="unknown")])
    with pytest.raises(ValueError, match="no split rule for unknown"):
        catalog.eval_set("unknown", "dev", *args)
    with pytest.raises(AssertionError, match="train"):
        catalog.eval_set("unknown", "train", *args)
    with pytest.raises(AssertionError, match="other-bucket"):
        catalog.local_path_for("gs://other-bucket/image.jpg", args[1])
    with pytest.raises(FileNotFoundError):
        catalog.load_dataset("absent", *args)
    (tmp_path / "images/unknown.jsonl").write_text("{not-json}\n")
    with pytest.raises(json.JSONDecodeError):
        catalog.load_dataset("unknown", *args)
