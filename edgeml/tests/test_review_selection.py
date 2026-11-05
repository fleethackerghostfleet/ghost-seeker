import json
from pathlib import Path


def test_committed_dev_review_excludes_only_hurricane_studio_frames():
    selection = Path(__file__).resolve().parents[2] / "datasets/review/clean_v2_dev_review.jsonl"
    rows = [json.loads(line) for line in selection.read_text().splitlines()]
    assert len(rows) == 2283
    assert all(row["eval_split"] == "dev" for row in rows)
    prefix = "video_sailing_drone_inside_a_hurricane/all/sec_"
    image_ids = {row["image_id"] for row in rows}
    assert image_ids.isdisjoint(f"{prefix}{second:04d}" for second in range(1, 12))
    hurricane_ids = {
        row["image_id"] for row in rows
        if row["dataset_id"] == "video_sailing_drone_inside_a_hurricane"
    }
    assert hurricane_ids == {f"{prefix}{second:04d}" for second in range(12, 50)}
