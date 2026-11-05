"""Frozen shore routing and requirement-metric regression tests; no cloud/GPU."""
import copy
import importlib.util
from pathlib import Path

import pytest
import yaml


def load(name):
    path = Path(__file__).parents[1] / "tools" / f"shore_{name}.py"
    spec = importlib.util.spec_from_file_location("shore_" + name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


ev = load("eval")
export = load("export")


def dataset(n=1):
    return {"info": {"split": "valid"}, "categories": [{"id": 1, "name": "vessel"}],
            "images": [{"id": i, "file_name": f"{i}.jpg", "width": 100, "height": 100,
                        "dataset": "mot", "source_video": "clip", "frame_index": i, "fps": 5,
                        "low_light": False} for i in range(1, n + 1)], "annotations": []}


def ann(i=1, iid=1, box=None, **kw):
    box = box or [0, 0, 10, 10]
    return {"id": i, "image_id": iid, "category_id": 1, "bbox": box,
            "area": box[2] * box[3], "iscrowd": 0, **kw}


def det(iid=1, box=None, score=0.9):
    return {"image_id": iid, "category_id": 1, "bbox": box or [0, 0, 10, 10], "score": score}


def test_cp_requirement_examples_and_edges():
    assert ev.cp_lower(292, 300) >= 0.95
    assert ev.cp_lower(291, 300) < 0.95
    assert ev.cp_lower(0, 10) == 0
    assert ev.cp_lower(10, 10) == pytest.approx(0.05 ** 0.1)
    assert ev.cp_lower(0, 0) is None
    with pytest.raises(ValueError):
        ev.cp_lower(11, 10)


def test_small_relaxed_iou_ap50_and_one_to_one():
    c = dataset()
    c["annotations"] = [ann()]
    # IoU=.428: relaxed recall succeeds; AP50 does not.
    r = ev.evaluate(c, [det(box=[4, 0, 10, 10]), det(box=[4, 0, 10, 10], score=0.8)], 0.5)
    assert r["strata"]["tiny"]["recall"] == 1
    assert r["strata"]["all"]["precision"] == 0.5
    assert r["AP50"] == 0


def test_large_strict_iou_and_perfect_ap():
    c = dataset()
    c["annotations"] = [ann(box=[0, 0, 40, 40])]
    assert ev.evaluate(c, [det(box=[16, 0, 40, 40])], 0)["strata"]["all"]["recall"] == 0
    r = ev.evaluate(c, [det(box=[0, 0, 40, 40])], 0)
    assert r["AP50"] == pytest.approx(1)
    assert r["strata"]["day_medium_large"]["AP50"] == pytest.approx(1)


def test_ignore_regions_empty_frames_and_lowlight():
    c = dataset(2)
    c["images"][1]["low_light"] = True
    c["annotations"] = [ann(ignore=1, iscrowd=1), ann(i=2, iid=2)]
    r = ev.evaluate(c, [det(), det(box=[50, 50, 10, 10]), det(iid=2)], 0.5)
    assert r["strata"]["all"]["false_positives"] == 1
    assert r["strata"]["all"]["fp_per_empty_frame"] == 1
    assert r["strata"]["low_light"]["recall"] == 1
    assert r["strata"]["day"]["vessels"] == 0
    assert r["strata"]["day"]["AP50"] is None


def test_no_detections_and_no_groundtruth():
    c = dataset()
    r = ev.evaluate(c, [], 0.5)
    assert r["strata"]["all"]["recall"] is None
    assert r["strata"]["all"]["fp_per_empty_frame"] == 0
    assert r["R4"]["recall"] is None
    assert r["AP50"] is None
    c["annotations"] = [ann()]
    assert ev.evaluate(c, [], 0.5)["AP50"] == 0
    assert not ev.fit_threshold(c, [det(box=[50, 50, 10, 10])])["feasible"]


def test_threshold_fit_precision_constraint_ties_and_test_guard():
    c = dataset(2)
    c["annotations"] = [ann(), ann(i=2, iid=2)]
    ds = [det(score=0.9), det(iid=2, score=0.8), det(box=[50, 50, 10, 10], score=0.7)]
    assert ev.fit_threshold(c, ds)["tau"] == 0.8
    # Equal-score TP/FP cannot be split by a threshold.
    ds[2]["score"] = 0.8
    assert ev.fit_threshold(c, ds)["tau"] == 0.9
    c["info"]["split"] = "test_sg"
    with pytest.raises(ValueError, match="valid"):
        ev.fit_threshold(c, ds)


def test_track_windows_gaps_short_tracks_and_source_scope():
    c = dataset(11)
    c["annotations"] = [ann(i=i, iid=i, track_id=1) for i in range(1, 12)]
    r = ev.track_recall(c, {1})
    assert r["windows"] == 2
    assert r["detected_windows"] == 1
    assert r["recall"] == 0.5
    c["annotations"] = c["annotations"][:9]
    assert ev.track_recall(c, {1})["windows"] == 0
    c = dataset(10)
    c["annotations"] = [ann(i=i, iid=i, track_id=1) for i in range(1, 11)]
    c["images"][5]["frame_index"] = 20
    assert ev.track_recall(c, {1})["windows"] == 0
    c = dataset(10)
    c["annotations"] = [ann(i=i, iid=i, track_id=1) for i in range(1, 11)]
    for im in c["images"][5:]:
        im["source_video"] = "other_clip"
        im["frame_index"] -= 5
    assert ev.track_recall(c, {1})["windows"] == 0


@pytest.mark.parametrize("change", ["unknown_image", "bad_box", "bad_score"])
def test_prediction_validation(change):
    c = dataset()
    d = det()
    if change == "unknown_image":
        d["image_id"] = 99
    elif change == "bad_box":
        d["bbox"][2] = -1
    else:
        d["score"] = float("nan")
    with pytest.raises(ValueError):
        ev.prepare(c, [d])


def test_source_routes_and_bucket_boundaries():
    policy = yaml.safe_load((Path(__file__).parents[1] / "configs/splits.yaml").read_text())
    sg = policy["singapore_maritime_v2"]
    assert [len(sg[k]) for k in ("train", "dev", "test")] == [26, 5, 5]
    assert len(set(sum(sg.values(), []))) == 36
    for split, sources in sg.items():
        for source in sources:
            assert export.route("singapore_maritime", source, "quarantine", policy) == {"train": "train", "dev": "valid", "test": "test_sg"}[split]
    assert export.route("seaships7000", "image1", "dev", policy) == "train"
    assert export.route("smd_vis_onboard_sample", "mvi0797", "test", policy) == "test_smd"
    assert export.route("smd_vis_onboard_sample", "mvi0790", "dev", policy) == "valid"
    assert export.route("fvessel_clip10", "fvessel_clip05", "dev", policy) is None
    with pytest.raises(ValueError):
        export.route("singapore_maritime", "unknown", None, policy)
    broken = copy.deepcopy(policy)
    broken["singapore_maritime_v2"]["train"].append(sg["test"][0])
    with pytest.raises(ValueError):
        export.route("singapore_maritime", sg["test"][0], None, broken)
    assert [export.size_bucket(a) for a in (255, 256, 1023, 1024, 9215, 9216)] == ["tiny", "small", "small", "medium", "medium", "large"]
    assert export.catalog_frame_index("gs://bucket/MVI_0790_f00570.jpg") == 570
    assert export.catalog_frame_index("gs://bucket/MVI_1448_VIS_Haze_frame_000123.jpg") == 123
    assert export.catalog_frame_index("gs://bucket/000001.jpg") is None
    assert export.catalog_annotation({"label": "horizon", "label_norm": None, "box": []}) is None
    assert export.catalog_annotation({"label": "boat", "label_norm": "vessel", "box": [0, 0, 3, 4]}) == {"bbox": [0, 0, 3, 4], "ignore": False}
