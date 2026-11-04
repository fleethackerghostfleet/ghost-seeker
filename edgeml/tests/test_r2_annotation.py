import json
import sys
from pathlib import Path

import numpy as np

SCRIPTS = Path(__file__).resolve().parents[1] / "scripts"
sys.path.insert(0, str(SCRIPTS))

from autolabel_gemini import (
    box_2d_to_xywh,
    crop_verifier_box,
    filter_frame_runaway,
    filter_repeated_boxes,
    load_v1_responses,
)
from qa_apply import apply, apply_sample_provenance, rewrite_attrs


def test_box_2d_to_xywh_scales_and_clips():
    assert box_2d_to_xywh([100, 200, 500, 600], 1000, 500) == [
        200.0,
        50.0,
        400.0,
        200.0,
    ]
    assert box_2d_to_xywh([900, 900, 1100, 1100], 1000, 500) == [
        900.0,
        450.0,
        100.0,
        50.0,
    ]
    assert box_2d_to_xywh([100, 100, 102, 102], 1000, 500) is None
    assert box_2d_to_xywh([0, 0, 1000, 700], 1000, 500) is None


def test_f2_drops_all_boxes_for_runaway_raw_frame():
    detections = [{"box": [0, 0, 4, 4]}]
    kept, dropped = filter_frame_runaway([{}] * 24, detections)
    assert kept == detections
    assert dropped is False
    kept, dropped = filter_frame_runaway([{}] * 25, detections)
    assert kept == []
    assert dropped is True


def test_f1_drops_only_connected_components_of_six_or_more():
    detections = [
        {"box": [0, center - 5, 3, 10]} for center in range(20, 26)
    ] + [
        {"box": [20, center - 5, 3, 10]} for center in range(50, 55)
    ] + [{"box": [40, 85, 10, 30]}]
    kept, dropped = filter_repeated_boxes(detections, 100)
    assert dropped == 6
    assert len(kept) == 6
    assert kept == detections[6:]


def test_uncached_v1_frames_are_requested_and_cached(tmp_path, monkeypatch):
    seq_dir = tmp_path / "seq_a"
    cache_dir = seq_dir / "teacher" / "cache"
    cache_dir.mkdir(parents=True)
    cached = {"objects": [{"label": "buoy"}], "night": False}
    (cache_dir / "frame_000002.json").write_text(json.dumps(cached))
    requested = []
    fresh = {"objects": [{"label": "ship"}], "night": True}

    def fake_request_frame(frame, image_uri, prompt, cache_path):
        requested.append((frame, image_uri, prompt))
        if frame == 3:
            return None, "frame 3: request failed"
        cache_path.parent.mkdir(parents=True, exist_ok=True)
        cache_path.write_text(json.dumps(fresh))
        return fresh, None

    monkeypatch.setattr("autolabel_gemini.request_frame", fake_request_frame)
    parsed, errors = load_v1_responses(
        seq_dir,
        {
            "1": {"gs_uri": "gs://bucket/1.jpg"},
            "2": {"gs_uri": "gs://bucket/2.jpg"},
            "3": {"gs_uri": "gs://bucket/3.jpg"},
        },
        3,
        "verbatim prompt",
        8,
    )

    assert requested == [
        (1, "gs://bucket/1.jpg", "verbatim prompt"),
        (3, "gs://bucket/3.jpg", "verbatim prompt"),
    ]
    assert parsed == {2: cached, 1: fresh, 3: None}
    assert json.loads((cache_dir / "frame_000001.json").read_text()) == fresh
    assert errors == [
        {
            "frame": 3,
            "source_uri": "gs://bucket/3.jpg",
            "error": "frame 3: request failed",
        }
    ]


def test_verifier_crop_expands_clips_and_upscales():
    image = np.zeros((100, 200, 3), dtype=np.uint8)
    crop, bounds = crop_verifier_box(image, [0, 0, 10, 20])
    assert bounds == (0, 0, 64, 64)
    assert crop.shape == (256, 256, 3)
    assert crop[0, 0].tolist() == [0, 0, 255]

    larger_image = np.zeros((400, 400, 3), dtype=np.uint8)
    crop, bounds = crop_verifier_box(larger_image, [100, 120, 40, 60])
    assert bounds == (80, 90, 160, 210)
    assert crop.shape == (384, 256, 3)


def _make_sequence(tmp_path):
    seq_dir = tmp_path / "seq_a"
    (seq_dir / "teacher").mkdir(parents=True)
    (seq_dir / "gt").mkdir()
    (seq_dir / "gt" / "labels.json").write_text(
        json.dumps({"classes": {"1": "ship", "2": "buoy"}})
    )
    teacher_rows = (
        "1,1,10.0,20.0,30.0,40.0,1,1,1.00\n"
        "1,2,200.0,200.0,20.0,30.0,1,2,0.50\n"
    )
    (seq_dir / "gt" / "gt.txt").write_text(teacher_rows)
    (seq_dir / "teacher" / "gt_teacher.txt").write_text(teacher_rows)
    (seq_dir / "gt" / "attrs.jsonl").write_text(
        '{"frame":1,"id":1,"label":"ship","nav_lights_visible":false,"lights":[],"approaching":null,"wake":false,"visibility":"full","confidence":0.9}\n'
        '{"frame":1,"id":2,"label":"buoy","nav_lights_visible":false,"lights":[],"approaching":null,"wake":false,"visibility":"partial","confidence":0.8}\n'
    )
    return seq_dir, teacher_rows


def test_qa_apply_box_corrections_and_preserve_no_correction_output(tmp_path):
    seq_dir, original = _make_sequence(tmp_path)
    gt_path = seq_dir / "gt" / "gt.txt"
    rows, classes = apply(seq_dir, {}, {"1": "ship", "2": "buoy"})
    assert rows == [
        [1, 1, 10.0, 20.0, 30.0, 40.0, 1, 1, 1.0],
        [1, 2, 200.0, 200.0, 20.0, 30.0, 1, 2, 0.5],
    ]
    assert classes == {"1": "ship", "2": "buoy"}
    before_attrs = (seq_dir / "gt" / "attrs.jsonl").read_bytes()
    rewrite_attrs(seq_dir, {}, rows, classes)
    assert (seq_dir / "gt" / "attrs.jsonl").read_bytes() == before_attrs
    formatted = "".join(
        f"{f},{i},{x:.1f},{y:.1f},{w:.1f},{h:.1f},{c},{k},{v:.2f}\n"
        for f, i, x, y, w, h, c, k, v in rows
    )
    gt_path.write_text(formatted)
    assert gt_path.read_bytes() == original.encode()

    corrections = {
        "drop_boxes": [[1, 2]],
        "relabel_boxes": {"1:1": "tug"},
        "fix_boxes": {"1:1": [11, 21, 32, 42]},
        "add_boxes": [{"frame": 2, "id": 3, "cls": "person", "box": [4, 5, 6, 7]}],
    }
    rows, classes = apply(seq_dir, corrections, {"1": "ship", "2": "buoy"})
    assert rows == [
        [1, 1, 11.0, 21.0, 32.0, 42.0, 1, 3, 1.0],
        [2, 3, 4.0, 5.0, 6.0, 7.0, 1, 4, 1.0],
    ]
    assert classes == {"1": "ship", "2": "buoy", "3": "tug", "4": "person"}
    rewrite_attrs(seq_dir, corrections, rows, classes)
    attrs = [
        json.loads(line)
        for line in (seq_dir / "gt" / "attrs.jsonl").read_text().splitlines()
    ]
    assert [(row["frame"], row["id"], row["label"]) for row in attrs] == [
        (1, 1, "tug"),
        (2, 3, "person"),
    ]
    assert gt_path.read_bytes() == original.encode()


def test_qa_apply_records_frame_and_box_provenance(tmp_path):
    seq_dir = tmp_path / "seq_a"
    gt_dir = seq_dir / "gt"
    gt_dir.mkdir(parents=True)
    (gt_dir / "frame_attrs.jsonl").write_text(
        '{"frame":1,"night_gemini":false}\n{"frame":2,"night_gemini":true}\n'
    )
    (gt_dir / "attrs.jsonl").write_text(
        '{"frame":1,"id":1,"label":"unknown"}\n'
        '{"frame":2,"id":2,"label":"buoy"}\n'
    )
    apply_sample_provenance(seq_dir, {1: "pass"})
    frame_attrs = [
        json.loads(line) for line in (gt_dir / "frame_attrs.jsonl").read_text().splitlines()
    ]
    attrs = [json.loads(line) for line in (gt_dir / "attrs.jsonl").read_text().splitlines()]
    assert [(row["qa_reviewed"], row["qa_verdict"]) for row in frame_attrs] == [
        (True, "pass"),
        (False, None),
    ]
    assert [row["label_source"] for row in attrs] == ["teacher+human", "teacher"]
