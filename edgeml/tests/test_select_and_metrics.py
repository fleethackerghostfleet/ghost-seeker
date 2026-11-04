import numpy as np

from edgeml.eval.baselines import image_dedup_events, naive_events
from edgeml.eval.sightings import match_sightings, sighting_metrics
from edgeml.select import greedy
from edgeml.types import Detection


def _ev(sid, value, forms, kind="open"):
    return {"event": kind, "sighting_id": sid, "value": value, "forms": forms, "t_s": 0.0, "frame_idx": 1}


FORMS = [{"kind": "fact", "bytes": 300}, {"kind": "crop", "bytes": 3000, "quality": 50}, {"kind": "crop", "bytes": 6000, "quality": 80},
         {"kind": "thumb", "bytes": 12000}, {"kind": "full", "bytes": 100000}]


def test_greedy_cheap_forms_first_and_budget_respected():
    evs = [_ev("a", 0.9, FORMS), _ev("b", 0.5, FORMS)]
    r = greedy(evs, 7000)
    assert r["bytes_used"] <= 7000
    kinds = [(c["sighting_id"], c["kind"]) for c in r["selected"]]
    assert kinds[:2] == [("a", "fact"), ("b", "fact")]           # facts of every object before any image
    assert ("a", "crop") in kinds and ("b", "full") not in kinds


def test_greedy_decay_spreads_bytes_across_objects():
    evs = [_ev("a", 1.0, FORMS), _ev("b", 0.3, FORMS)]
    no_decay = greedy(evs, 9400, ladder_decay=None)
    decay = greedy(evs, 9400, ladder_decay=(1, 0.5, 0.25, 0.1))
    assert no_decay["n_objects_touched"] <= decay["n_objects_touched"]
    assert decay["n_objects_touched"] == 2


def test_greedy_byte_cap_per_object_blocks_full():
    evs = [_ev("a", 1.0, FORMS)]
    r = greedy(evs, 1_000_000, max_bytes_per_object=20_000)
    assert "full" not in {c["kind"] for c in r["selected"]}
    assert greedy(evs, 1_000_000)["selected"][-1]["kind"] == "full"


def test_greedy_skips_close_events_and_handles_iterators():
    evs = iter([_ev("a", 1.0, FORMS, kind="close"), _ev("b", 0.5, FORMS)])
    r = greedy(evs, 10_000)
    assert r["n_items"] == 1 and r["selected"][0]["sighting_id"] == "b"


def _gt():
    return {1: [(f, [100.0 + f, 100.0, 40.0, 20.0]) for f in range(1, 21)], 2: [(f, [400.0, 200.0, 30.0, 30.0]) for f in range(5, 15)]}


def test_matching_and_duplicates():
    gt = _gt()
    straj = [("s1", f, [100.0 + f, 100.0, 40.0, 20.0], 0.9) for f in range(1, 11)]
    straj += [("s2", f, [100.0 + f, 100.0, 40.0, 20.0], 0.9) for f in range(12, 21)]   # second sighting on object 1 = duplicate
    straj += [("s3", f, [700.0, 50.0, 30.0, 30.0], 0.9) for f in range(1, 11)]          # false
    m = match_sightings(straj, gt)
    assert m == {"s1": 1, "s2": 1, "s3": None}
    events = [{"event": "open", "sighting_id": "s1", "t_s": 0.2, "frame_idx": 2}, {"event": "update", "sighting_id": "s1", "t_s": 1.0, "frame_idx": 6},
              {"event": "open", "sighting_id": "s2", "t_s": 2.4, "frame_idx": 13}, {"event": "open", "sighting_id": "s3", "t_s": 0.2, "frame_idx": 2}]
    r = sighting_metrics(events, straj, gt, fps=5.0, n_frames=20)
    assert r["object_recall"] == 0.5 and r["duplicates_per_object"] == 0.5 and r["false_sightings"] == 1
    assert abs(r["ttfr_mean_s"] - 0.2) < 1e-9 and r["reports_per_object"] == 3.0


def test_naive_and_dedup_baselines_count_reports():
    img = np.zeros((120, 160, 3), np.uint8)
    per = [(f, (f - 1) / 5, img, [Detection(box=[10, 10, 20, 20], score=0.9, label="vessel")]) for f in range(1, 11)]
    ev, traj = naive_events("c", per)
    assert len(ev) == 10 and all(e["event"] == "open" for e in ev) and all(e["forms"][0]["kind"] == "fact" for e in ev)
    ev2, _ = image_dedup_events("c", per)
    assert 1 <= len(ev2) < len(ev)   # identical frames collapse


def test_stabilizer_affine_roundtrip_and_horizon():
    import cv2
    from edgeml.stabilize import HorizonStabilizer
    st = HorizonStabilizer()
    img = np.full((360, 640, 3), 200, np.uint8)
    img[200:, :] = 60                                    # sharp sea/sky edge at row 200
    M0 = st.estimate(img)
    assert st.ref is not None and abs(st.ref[1] - 200) < 6 and np.allclose(M0, np.array([[1, 0, 0], [0, 1, 0]]), atol=1e-6)
    tilted = cv2.warpAffine(img, cv2.getRotationMatrix2D((320, 200), 5.0, 1.0), (640, 360), borderMode=cv2.BORDER_REPLICATE)
    tilted = np.roll(tilted, 20, axis=0)
    M = st.estimate(tilted)
    assert not np.allclose(M, M0)
    box = [100.0, 150.0, 40.0, 20.0]
    back = st.warp_box(st.warp_box(box, M), st.inverse(M))
    assert np.allclose(back[:2], box[:2], atol=1.5) and back[2] >= 40 and back[3] >= 20
