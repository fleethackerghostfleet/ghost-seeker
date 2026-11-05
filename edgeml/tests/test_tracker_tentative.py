import numpy as np
import pytest

pytest.importorskip("boxmot")

from edgeml.tracking.boxmot_adapter import make_tracker  # noqa: E402
from edgeml.types import Detection  # noqa: E402


def _det(box, score=0.9):
    return Detection(box=list(box), score=score, label="boat", label_id=8)


def _run(trk, steps, fps=5.0):
    """boxmot activates everything on its very first frame, so start with an empty frame (as any real clip effectively does)."""
    img = np.zeros((360, 640, 3), np.uint8)
    trk.update([], img, 0, -1.0 / fps)
    out = []
    for fi, dets in steps:
        out.append(trk.update(dets, img, fi, (fi - 1) / fps))
    return out


def test_bytetrack_single_detection_is_dropped_without_tentative():
    trk = make_tracker("bytetrack", fps=5.0, asso_func="centroid", max_lost_s=6)
    out = _run(trk, [(1, [_det([100, 100, 40, 20])]), (2, []), (3, [_det([104, 100, 40, 20])]), (4, [])])
    assert all(len(o) == 0 for o in out[1:])                 # k and k+2: never becomes a track


def test_tentative_passthrough_links_intermittent_detections():
    trk = make_tracker("bytetrack", fps=5.0, asso_func="centroid", max_lost_s=6, tentative_thresh=0.5, tentative_keep_s=1.0)
    out = _run(trk, [(1, [_det([100, 100, 40, 20])]), (2, []), (3, [_det([104, 100, 40, 20])]), (4, [])])
    assert [t.state for t in out[0]] == ["tentative"] and out[0][0].track_id < 0
    assert out[2] and out[2][0].track_id == out[0][0].track_id    # same tentative id two frames later
    assert out[1] == [] and out[3] == []
    # below threshold: not passed through
    trk2 = make_tracker("bytetrack", fps=5.0, asso_func="centroid", max_lost_s=6, tentative_thresh=0.5)
    assert _run(trk2, [(1, [_det([100, 100, 40, 20], 0.4)])])[0] == []


def test_tentative_superseded_by_confirmed_track():
    trk = make_tracker("bytetrack", fps=5.0, asso_func="centroid", max_lost_s=6, tentative_thresh=0.5)
    steps = [(k, [_det([100 + 2 * k, 100, 40, 20])]) for k in range(1, 5)]
    out = _run(trk, steps)
    assert [t.state for t in out[0]] == ["tentative"]
    later = [t for o in out[1:] for t in o]
    assert later and all(t.state == "confirmed" for t in later)   # once boxmot emits, no tentative duplicate
    assert all(len(o) == 1 for o in out[1:])


def test_far_small_leftovers_do_not_link():
    trk = make_tracker("bytetrack", fps=5.0, asso_func="centroid", max_lost_s=6, tentative_thresh=0.5, tentative_gate_diag=1.5)
    out = _run(trk, [(1, [_det([100, 100, 20, 10])]), (2, []), (3, [_det([300, 100, 20, 10])])])
    assert out[0][0].track_id != out[2][0].track_id


def test_jump_guard_reassigns_id_on_neighbour_hop():
    # a 30x12 boat tracked at x=100 disappears; 4 frames later its (lost) track is re-acquired 90 px away on a neighbour (inside the 0.15-diagonal gate)
    steps = [(k, [_det([100 + k, 300, 30, 12])]) for k in range(1, 6)] + [(k, []) for k in range(6, 10)]
    steps += [(k, [_det([190 + k, 300, 30, 12])]) for k in range(10, 14)]
    plain = _run(make_tracker("bytetrack", fps=5.0, asso_func="centroid", max_lost_s=6), steps)
    ids = {t.track_id for o in plain for t in o}
    assert len(ids) == 1                                         # boxmot: one id hops between the two boats
    guarded = _run(make_tracker("bytetrack", fps=5.0, asso_func="centroid", max_lost_s=6, max_jump_diag=1.5), steps)
    ids_g = [t.track_id for o in guarded for t in o]
    assert len(set(ids_g)) == 2 and ids_g[-1] == ids_g[-2] != ids_g[0]  # fresh id after the jump, then stable
    # normal motion (2 px/frame) is not a jump
    smooth = _run(make_tracker("bytetrack", fps=5.0, asso_func="centroid", max_lost_s=6, max_jump_diag=1.5),
                  [(k, [_det([100 + 2 * k, 300, 30, 12])]) for k in range(1, 20)])
    assert len({t.track_id for o in smooth for t in o}) == 1
