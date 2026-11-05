import json
from pathlib import Path

import cv2
import numpy as np
from edgeml_research.data.mot import MOTSequence, load_set, read_index, validate_sequence


def _make_seq(root: Path, set_name: str, seq_id: str, n=4, w=64, h=48, fps=5.0):
    d = root / set_name / seq_id
    (d / "img1").mkdir(parents=True)
    (d / "gt").mkdir()
    for f in range(1, n + 1):
        cv2.imwrite(str(d / "img1" / f"{f:06d}.jpg"), np.zeros((h, w, 3), np.uint8))
    (d / "seqinfo.ini").write_text(
        f"[Sequence]\nname={seq_id}\nimDir=img1\nframeRate={fps}\nseqLength={n}\nimWidth={w}\nimHeight={h}\nimExt=.jpg\nsource=unit\nlicense=none\n")
    (d / "gt" / "gt.txt").write_text("1,1,10,10,8,6,1,1,1.0\n2,1,11,10,8,6,1,1,1.0\n2,2,30,20,4,4,1,2,0.5\n3,1,12,10,8,6,1,1,1.0\n")
    (d / "gt" / "labels.json").write_text(json.dumps({"classes": {"1": "vessel", "2": "buoy"}, "label_source": "human"}))
    with open(root / set_name / "index.jsonl", "a") as f:
        f.write(json.dumps({"seq_id": seq_id, "set": set_name, "source": "unit", "fps": fps, "frames": n, "tracks": 2}) + "\n")
    return d


def test_sequence_and_set(tmp_path):
    _make_seq(tmp_path, "unit_set", "seq_a")
    _make_seq(tmp_path, "unit_set", "seq_b")
    seqs = load_set("unit_set", root=str(tmp_path))
    assert [s.seq_id for s in seqs] == ["seq_a", "seq_b"]
    s = seqs[0]
    assert len(s) == 4 and s.info.frame_rate == 5.0 and s.info.im_width == 64
    frames = list(s.iter_frames())
    assert [f for f, _, _ in frames] == [1, 2, 3, 4]
    assert abs(frames[2][1] - 0.4) < 1e-9 and frames[0][2].shape == (48, 64, 3)
    assert len(s.gt) == 4 and s.gt[2].box == [30, 20, 4, 4] and s.gt[2].visibility == 0.5
    assert sorted(s.gt_by_frame()) == [1, 2, 3] and len(s.gt_by_track()[1]) == 3
    assert s.class_name(2) == "buoy" and s.label_source == "human"
    st = s.stats()
    assert st["tracks"] == 2 and st["boxes"] == 4 and st["frames_with_gt"] == 3
    assert validate_sequence(s) == []
    assert len(read_index(tmp_path / "unit_set")) == 2


def test_validate_catches_problems(tmp_path):
    d = _make_seq(tmp_path, "unit_set", "seq_bad")
    (d / "img1" / "000004.jpg").unlink()
    (d / "gt" / "gt.txt").write_text("9,1,10,10,8,6,1,1,1.0\n")
    (d / "gt" / "labels.json").write_text(json.dumps({"classes": {}, "label_source": "guess"}))
    errs = validate_sequence(MOTSequence(d))
    assert any("missing frames" in e for e in errs)
    assert any("outside" in e for e in errs)
    assert any("label_source" in e for e in errs)


def test_split_filter(tmp_path):
    _make_seq(tmp_path, "unit_set", "seq_a")
    _make_seq(tmp_path, "unit_set", "seq_b")
    y = tmp_path / "splits.yaml"
    y.write_text("version: 1\ntest:\n  unit_set: [seq_b]\ndev:\n  unit_set: [seq_a]\n")
    assert [s.seq_id for s in load_set("unit_set", root=str(tmp_path), split="test", splits_yaml=y)] == ["seq_b"]
