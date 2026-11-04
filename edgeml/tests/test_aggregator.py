import numpy as np

from edgeml.tracking import AggregatorParams, SightingAggregator
from edgeml.types import Track


def _img(w=640, h=360, seed=0):
    rng = np.random.default_rng(seed)
    return rng.integers(0, 255, (h, w, 3), dtype=np.uint8)


def _trk(tid, box, fi, t, score=0.9, label="vessel"):
    return Track(track_id=tid, box=list(box), score=score, label=label, frame_idx=fi, t_s=t, state="confirmed")


def _run(agg, steps, fps=5.0):
    img = _img()
    out = []
    for fi, tracks in steps:
        t = (fi - 1) / fps
        out += agg.update(tracks, img, fi, t)
    return out


def test_open_after_min_hits_then_close():
    agg = SightingAggregator("c", AggregatorParams(min_hits=2, close_after_s=1.0, min_update_interval_s=0.0))
    steps = [(1, [_trk(1, [100, 100, 40, 20], 1, 0.0)]), (2, [_trk(1, [102, 100, 40, 20], 2, 0.2)])]
    steps += [(k, []) for k in range(3, 12)]
    ev = _run(agg, steps)
    kinds = [e["event"] for e in ev]
    assert kinds[0] == "open" and ev[0]["frame_idx"] == 2
    assert kinds[-1] == "close" and kinds.count("open") == 1
    assert ev[0]["forms"][0]["kind"] == "fact" and ev[0]["forms"][0]["bytes"] > 0
    assert {f["kind"] for f in ev[0]["forms"]} == {"fact", "crop", "thumb", "full"}
    assert all(f["bytes"] > 0 for f in ev[0]["forms"])
    assert 0.0 <= ev[0]["value"] <= 1.0


def test_single_hit_track_never_reported():
    agg = SightingAggregator("c", AggregatorParams(min_hits=2, close_after_s=0.5))
    ev = _run(agg, [(1, [_trk(1, [10, 10, 30, 30], 1, 0.0)])] + [(k, []) for k in range(2, 8)])
    assert ev == []


def test_relink_continues_sighting_after_gap():
    p = AggregatorParams(min_hits=2, close_after_s=0.6, relink_gap_s=5.0, appearance_thr=0.0, min_update_interval_s=0.0)
    agg = SightingAggregator("c", p)
    steps = [(1, [_trk(1, [100, 100, 40, 20], 1, 0.0)]), (2, [_trk(1, [104, 100, 40, 20], 2, 0.2)]), (3, [_trk(1, [108, 100, 40, 20], 3, 0.4)])]
    steps += [(k, []) for k in range(4, 10)]                     # lost for 1.2 s -> close
    steps += [(10, [_trk(7, [136, 100, 40, 20], 10, 1.8)]), (11, [_trk(7, [140, 100, 40, 20], 11, 2.0)])]
    ev = _run(agg, steps)
    sids = {e["sighting_id"] for e in ev}
    assert len(sids) == 1
    assert [e["event"] for e in ev] == ["open", "close", "update"]
    assert ev[-1]["reason"] == "relink" and 7 in ev[-1]["track_ids"]


def test_no_relink_when_far_away():
    p = AggregatorParams(min_hits=2, close_after_s=0.6, relink_gap_s=5.0, relink_speed_frac=0.01, appearance_thr=0.0)
    agg = SightingAggregator("c", p)
    steps = [(1, [_trk(1, [10, 10, 40, 20], 1, 0.0)]), (2, [_trk(1, [12, 10, 40, 20], 2, 0.2)])]
    steps += [(k, []) for k in range(3, 9)]
    steps += [(9, [_trk(7, [500, 300, 40, 20], 9, 1.6)]), (10, [_trk(7, [502, 300, 40, 20], 10, 1.8)])]
    ev = _run(agg, steps)
    assert len({e["sighting_id"] for e in ev}) == 2


def test_duplicate_concurrent_track_merges():
    agg = SightingAggregator("c", AggregatorParams(min_hits=2))
    steps = [(1, [_trk(1, [100, 100, 40, 20], 1, 0.0)]), (2, [_trk(1, [102, 100, 40, 20], 2, 0.2)]),
             (3, [_trk(1, [104, 100, 40, 20], 3, 0.4), _trk(2, [105, 101, 40, 20], 3, 0.4)]),
             (4, [_trk(2, [106, 100, 40, 20], 4, 0.6)]), (5, [_trk(2, [108, 100, 40, 20], 5, 0.8)])]
    ev = _run(agg, steps)
    assert len({e["sighting_id"] for e in ev}) == 1
    assert [e["event"] for e in ev].count("open") == 1


def test_material_update_only_on_change():
    p = AggregatorParams(min_hits=2, min_update_interval_s=0.0, keyframe_improve=10.0, bearing_step_deg=1000)
    agg = SightingAggregator("c", p)
    steps = [(k, [_trk(1, [100 + k, 100, 40, 20], k, (k - 1) / 5)]) for k in range(1, 15)]   # same size class, steady
    ev = _run(agg, steps)
    assert [e["event"] for e in ev] == ["open"]
    # now grow the box a lot -> size_class change / closing
    steps = [(k, [_trk(1, [100, 100, 40 + 20 * (k - 14), 20 + 10 * (k - 14)], k, (k - 1) / 5)]) for k in range(15, 22)]
    ev2 = _run(agg, steps)
    assert ev2 and ev2[0]["event"] == "update" and ev2[0]["reason"] in ("size_class", "closing", "aspect")


def test_key_frame_prefers_bigger_sharper_box():
    agg = SightingAggregator("c", AggregatorParams(min_hits=1, min_update_interval_s=0.0))
    img = np.zeros((360, 640, 3), np.uint8)
    img[50:150, 50:250] = _img(200, 100)[..., :]   # textured region
    agg.update([_trk(1, [300, 300, 10, 10], 1, 0.0)], img, 1, 0.0)         # tiny, flat
    agg.update([_trk(1, [50, 50, 200, 100], 2, 0.2)], img, 2, 0.2)         # big, textured
    st = next(iter(agg.states.values()))
    assert st.s.best_frame_idx == 2
    forms = {f["kind"]: f for f in agg.events[-1]["forms"]} if agg.events[-1]["event"] != "close" else {}
    assert forms and forms["crop"]["region"][2] >= 200


def test_static_mask_suppresses_own_ship_structure():
    agg = SightingAggregator("c", AggregatorParams(min_hits=50, static_mask_s=1.0, close_after_s=0.5))
    steps = [(k, [_trk(1, [100, 100, 40, 20], k, (k - 1) / 5)]) for k in range(1, 10)]
    ev = _run(agg, steps)
    assert ev == [] and any(st.status == "masked" for st in agg.states.values())
    # a new track on the same pixels is ignored
    ev = _run(agg, [(10, [_trk(9, [101, 100, 40, 20], 10, 1.8)]), (11, [_trk(9, [101, 100, 40, 20], 11, 2.0)])])
    assert ev == [] and 9 not in agg.track_to_sid
