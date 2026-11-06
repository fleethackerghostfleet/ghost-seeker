from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest

SCRIPT = Path(__file__).with_name("build_review_set.py")
SPEC = importlib.util.spec_from_file_location("build_review_set", SCRIPT)
build = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(build)


SPLITS = {
    "quarantine": {"fvessel_clip10": ["fvessel_clip05"]},
    "test": {
        "ghost_fleet_clips": ["Spotted_A_Submarine"],
        "smd_vis_onboard_sample": ["MVI_0801"],
    },
    "source_video_aliases": {"Arriving_In_Norfolk_USA_3hr_uncut": "Arriving_In_Norfolk"},
}


def row(**overrides):
    return {
        "dataset_id": "video_spotted_a_submarine",
        "image_id": "video_spotted_a_submarine/all/sec_0001",
        "source_video": "Spotted_A_Submarine",
        "eval_split": "dev",
        **overrides,
    }


@pytest.mark.parametrize(
    "candidate",
    [
        row(),
        row(dataset_id="smd_vis_onboard_sample", image_id="smd/MVI_0801/0001", source_video="MVI_0801"),
        row(dataset_id="fvessel_clip10", source_video="fvessel_clip05"),
        row(dataset_id="video_arriving_in_norfolk_usa_3hr_uncut", source_video="Arriving_In_Norfolk_USA_3hr_uncut"),
    ],
)
def test_selection_guard_rejects_test_and_quarantine(candidate):
    with pytest.raises(ValueError):
        build.selection_guard([candidate], SPLITS)


def test_selection_guard_rejects_non_dev_and_singapore_maritime():
    for candidate in (row(eval_split="test"), row(dataset_id="singapore_maritime")):
        with pytest.raises(ValueError):
            build.selection_guard([candidate], SPLITS)


def test_selection_guard_accepts_dev_row():
    build.selection_guard(
        [row(dataset_id="video_dev_clip", image_id="video_dev_clip/all/sec_0001", source_video="Dev_Clip")],
        SPLITS,
    )


@pytest.mark.parametrize("second", range(1, 12))
def test_selection_guard_rejects_non_maritime_hurricane_frames(second):
    with pytest.raises(ValueError, match="non-maritime studio/title frame"):
        build.selection_guard(
            [row(
                dataset_id="video_sailing_drone_inside_a_hurricane",
                image_id=f"video_sailing_drone_inside_a_hurricane/all/sec_{second:04d}",
                source_video="Sailing_Drone_Inside_A_Hurricane",
            )],
            SPLITS,
        )


def test_selection_guard_accepts_at_sea_hurricane_boundary_and_empty_selection():
    build.selection_guard([], SPLITS)
    build.selection_guard(
        [row(
            dataset_id="video_sailing_drone_inside_a_hurricane",
            image_id="video_sailing_drone_inside_a_hurricane/all/sec_0012",
            source_video="Sailing_Drone_Inside_A_Hurricane",
        )],
        SPLITS,
    )


def test_detector_label_mapping_uses_shiptype_catalog_and_safe_fallbacks():
    assert build.map_detector_class("boat", "warship") == "warship"
    assert build.map_detector_class("boat", "passenger") == "vessel"
    assert build.map_detector_class("boat") == "vessel"
    assert build.map_detector_class("airplane") == "aircraft"
    assert build.map_detector_class("bird") == "bird"
    assert build.map_detector_class("person") == "person"
    assert build.map_detector_class("surfboard") == "unknown"
    assert build.map_detector_class("kite") == "unknown"
    assert build.map_detector_class("unknown-label") == "unknown"
