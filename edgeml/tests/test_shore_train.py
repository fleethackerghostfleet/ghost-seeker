import importlib.util
from pathlib import Path

import pytest

spec = importlib.util.spec_from_file_location("shore_train", Path(__file__).parents[1] / "tools/shore_train.py")
train = importlib.util.module_from_spec(spec)
spec.loader.exec_module(train)


def test_budget_reserves_a_complete_epoch():
    assert not train.stop_before_next_epoch(10, 20, 100)
    assert train.stop_before_next_epoch(80, 20, 100)
    assert train.stop_before_next_epoch(101, 20, 100)
    with pytest.raises(ValueError):
        train.stop_before_next_epoch(1, 2, -1)


def test_training_category_adapter_does_not_mutate_frozen_annotations(tmp_path):
    c = {"categories": [{"id": 1, "name": "vessel", "supercategory": "vessel"}],
         "images": [{"id": 1, "file_name": "x.jpg"}],
         "annotations": [{"category_id": 1, "bbox": [1, 2, 3, 4], "iscrowd": 0}]}
    adapted = train.training_view(c, tmp_path)
    assert adapted["categories"][0]["id"] == 0
    assert adapted["annotations"][0]["category_id"] == 0
    assert adapted["annotations"][0]["bbox"] == [1, 2, 3, 4]
    assert adapted["images"][0]["file_name"] == str(tmp_path / "x.jpg")
    assert c["annotations"][0]["category_id"] == c["categories"][0]["id"] == 1
    c["annotations"] = []
    assert train.training_view(c, tmp_path)["annotations"] == []
    c["categories"][0]["id"] = 3
    with pytest.raises(ValueError):
        train.training_view(c, tmp_path)
