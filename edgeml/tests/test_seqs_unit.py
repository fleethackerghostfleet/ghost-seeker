import json

import pytest

from edgeml.data.seqs import append_index, gt_by_frame, list_sequences, load_sequence, write_sequence


def make_sequence(root, name="sample", rows=None, extra=None):
    rows = [(1, 7, [1.25, 2.5, 10, 20], 1, 3, .75), (2, 8, [0, 0, 1, 1], 0, 4, 1)] if rows is None else rows
    write_sequence(root, name, 2, 100, 80, iter(rows), {"3": "tanker"}, 5, extra)
    return load_sequence(root)


def test_sequence_round_trip_preserves_metadata_and_filters_ignored_ground_truth(tmp_path):
    seq = make_sequence(tmp_path / "sequence", extra={"source_dataset": "local", "split": "dev"})
    assert (seq.seq_id, seq.fps, seq.n_frames, seq.width, seq.height) == ("sample", 2, 5, 100, 80)
    assert seq.labels == {"3": "tanker"} and seq.meta["source_dataset"] == "local"
    assert "imdir" not in seq.meta and "imext" not in seq.meta
    assert seq.gt[0] == (1, 7, [1.25, 2.5, 10., 20.], 1, 3, .75)
    assert seq.duration_s == 2.5
    assert seq.gt_tracks() == {7: [(1, [1.25, 2.5, 10., 20.])]}
    assert gt_by_frame(seq) == {1: [(7, [1.25, 2.5, 10., 20.], 3)]}
    assert list(seq.frames(every=2)) == [(i, (i-1)/2, seq.root / "img1" / f"{i:06d}.jpg") for i in [1, 3, 5]]
    assert [i for i, _, _ in seq.frames(max_frames=2)] == [1, 2]
    assert seq.frame_path(3).name == "000003.jpg"


def test_sequence_optional_files_and_short_mot_rows(tmp_path):
    seq = make_sequence(tmp_path, rows=[])
    (tmp_path / "gt/labels.json").unlink()
    (tmp_path / "gt/gt.txt").write_text("\n1.0,4.0,1,2,3,4\n2,4,2,3,4,5,0\n3,4,3,4,5,6,1,9\n")
    text = (tmp_path / "seqinfo.ini").read_text().replace("name=sample\n", "").replace("frameRate=2\n", "")
    (tmp_path / "seqinfo.ini").write_text(text)
    seq = load_sequence(tmp_path)
    assert seq.seq_id == tmp_path.name and seq.fps == 1 and seq.labels == {}
    assert [row[3:] for row in seq.gt] == [(1, 1, 1.), (0, 1, 1.), (1, 9, 1.)]
    assert [frame for frame, _ in seq.gt_tracks()[4]] == [1, 3]
    (tmp_path / "gt/gt.txt").unlink()
    assert load_sequence(tmp_path).gt == []


def test_empty_sequence_has_no_frames_or_tracks(tmp_path):
    seq = make_sequence(tmp_path, rows=[])
    seq.n_frames = 0
    assert list(seq.frames()) == [] and seq.duration_s == 0
    assert seq.gt_tracks() == gt_by_frame(seq) == {}


def test_index_replacement_split_filtering_and_metadata_precedence(tmp_path):
    make_sequence(tmp_path / "a", name="a", extra={"split": "ini-split"})
    make_sequence(tmp_path / "b", name="b")
    append_index(tmp_path, {"seq_id": "a", "split": "test", "origin": "old"})
    append_index(tmp_path, {"seq_id": "b", "split": "test"})
    append_index(tmp_path, {"seq_id": "a", "split": "dev", "origin": "updated"})
    with (tmp_path / "index.jsonl").open("a") as stream:
        stream.write("\n" + json.dumps({"seq_id": "missing", "split": "dev"}) + "\n")
    assert [seq.seq_id for seq in list_sequences(tmp_path)] == ["b", "a"]
    dev = list_sequences(tmp_path, "dev")
    assert len(dev) == 1 and dev[0].meta["origin"] == "updated" and dev[0].meta["split"] == "ini-split"
    assert [seq.seq_id for seq in list_sequences(tmp_path, "test")] == ["b"]
    assert list_sequences(tmp_path, "absent") == []


def test_sequence_directory_fallback_is_sorted_and_ignores_non_sequences(tmp_path):
    make_sequence(tmp_path / "z", name="z")
    make_sequence(tmp_path / "a", name="a")
    (tmp_path / "unrelated.txt").write_text("not a sequence")
    assert [seq.seq_id for seq in list_sequences(tmp_path)] == ["a", "z"]


@pytest.mark.parametrize("file,payload,error", [("gt/gt.txt", "not,MOT", ValueError),
    ("gt/labels.json", "not JSON", json.JSONDecodeError), ("seqinfo.ini", "[Other]\nx=1", KeyError)])
def test_sequence_surfaces_malformed_inputs(tmp_path, file, payload, error):
    make_sequence(tmp_path)
    (tmp_path / file).write_text(payload)
    with pytest.raises(error):
        load_sequence(tmp_path)


def test_index_invalid_json_is_not_silently_discarded(tmp_path):
    (tmp_path / "index.jsonl").write_text("not JSON")
    with pytest.raises(json.JSONDecodeError):
        list_sequences(tmp_path)
    with pytest.raises(json.JSONDecodeError):
        append_index(tmp_path, {"seq_id": "new"})
