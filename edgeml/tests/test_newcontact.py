from types import SimpleNamespace
from unittest.mock import Mock

import numpy as np
import pytest

from edgeml.tracking.newcontact import NewContactDetector
from edgeml.types import Detection, Track


def state(box, seen=1., first=0., status="open", traj=()):
    return SimpleNamespace(s=SimpleNamespace(box=box, first_t_s=first), last_seen_t=seen, status=status, traj=traj)


def event(box, first=2., kind="open", **kwargs):
    return dict(event=kind, sighting_id="new", first_t_s=first, box=box, **kwargs)


def test_new_object_id_switch_and_memory_expiry():
    detector = NewContactDetector(memory_s=10)
    old = state([10, 10, 20, 20])
    assert detector.is_new(event([100, 100, 20, 20]), {"old": old})
    assert not detector.is_new(event([12, 11, 20, 20]), {"old": old})
    old.status = "closed"
    assert not detector.is_new(event([12, 11, 20, 20]), {"old": old})
    assert detector.is_new(event([12, 11, 20, 20], first=12), {"old": old})
    assert not detector.is_new(event([12, 11, 20, 20], first=11), {"old": old})


@pytest.mark.parametrize("kind", ["update", "duplicate", "close"])
def test_only_opens_including_tentative_are_eligible(kind):
    detector = NewContactDetector()
    assert not detector.is_new(event([0, 0, 10, 10], kind=kind, reason="relink"), {})
    assert detector.is_new(event([0, 0, 10, 10], tentative=True), {})


def test_own_pending_tracks_and_first_frame_neighbours_do_not_suppress():
    detector = NewContactDetector()
    box = [0, 0, 10, 10]
    states = {"new": state(box), "pending": state(box, status="pending")}
    assert detector.is_new(event(box), states)
    states["same_frame"] = state(box, first=2., seen=2.)
    assert detector.is_new(event(box), states)


def test_delayed_confirmation_uses_observation_before_first_seen():
    detector = NewContactDetector()
    old = state([100, 100, 10, 10], seen=5., traj=[(1, 1., [0, 0, 10, 10], .9), (2, 5., [100, 100, 10, 10], .9)])
    assert not detector.is_new(event([0, 0, 10, 10]), {"old": old})
    assert detector.is_new(event([100, 100, 10, 10]), {"old": old})


def test_iou_and_diagonal_distance_are_alternative_near_tests():
    assert NewContactDetector(iou_thr=.3, dist_diag=0).near([0, 0, 20, 20], [5, 0, 20, 20])
    assert NewContactDetector(iou_thr=.9, dist_diag=1).near([0, 0, 20, 20], [22, 0, 20, 20])
    assert not NewContactDetector().near([0, 0, 20, 20], [100, 0, 20, 20])


def test_reappearance_at_25_seconds_survives_default_aggregator_eviction():
    from edgeml.tracking.aggregator import SightingAggregator

    detector, aggregator = NewContactDetector(), SightingAggregator("source")
    image = np.zeros((100, 100, 3), np.uint8)
    box = [20, 20, 20, 20]

    def frame(fi, t_s, tid=None):
        detector.observe(aggregator.states, t_s)
        tracks = [] if tid is None else [Track(tid, box, .9, "boat", fi, t_s)]
        events = aggregator.update(tracks, image, fi, t_s)
        return [(ev["event"], detector.is_new(ev, aggregator.states)) for ev in events]

    assert frame(1, 0., 1) == []
    assert frame(2, 1., 1) == [("open", True)]
    assert frame(3, 7.) == [("close", False)]
    assert frame(4, 22.) == []
    assert not aggregator.states
    assert frame(5, 26., 2) == []
    assert frame(6, 27., 2) == [("open", False)]


def test_history_is_copied_source_local_pruned_and_kept_for_confirmation_lag():
    detector = NewContactDetector()
    box = [0, 0, 10, 10]
    old = state(box, seen=0., status="closed")
    detector.observe({"old": old}, 0.)
    box[0] = 100
    assert not detector.is_new(event([0, 0, 10, 10], first=25.), {})
    assert NewContactDetector().is_new(event([0, 0, 10, 10], first=25.), {})
    pending = state([0, 0, 10, 10], first=25., status="pending")
    detector.observe({"new": pending}, 40.)
    assert not detector.is_new(event([0, 0, 10, 10], first=25.), {})
    detector.observe({}, 40.)
    assert not detector.history
    assert detector.is_new(event([0, 0, 10, 10], first=40.), {})


def test_long_lived_trajectory_work_is_bounded_to_memory_and_lookup():
    class CountedTrajectory(list):
        reads = 0

        def __getitem__(self, index):
            self.reads += 1
            return super().__getitem__(index)

        def __iter__(self):
            raise AssertionError("must not scan the full trajectory")

    trajectory = CountedTrajectory((i, float(i), [0, 0, 10, 10], .9) for i in range(10000))
    old = state([0, 0, 10, 10], seen=9999., traj=trajectory)
    detector = NewContactDetector()
    detector.observe({"old": old}, 9999.)
    assert len(detector.history["old"][1]) == 31
    assert detector.history["old"][1][0][0] == 9969.
    assert trajectory.reads == 32
    trajectory.reads = 0
    assert not detector.is_new(event([0, 0, 10, 10], first=1.), {"old": old})
    assert trajectory.reads <= 16


@pytest.mark.parametrize("first", [0., 1., 100., 9968., 9969., 9990., 9995., 9999., 10029., 10030.])
@pytest.mark.parametrize("evicted", [False, True])
def test_bounded_lookup_matches_full_scan(first, evicted):
    trajectory = [(i, float(i), [i % 80, 0, 10, 10], .9) for i in range(10000)]
    old = state(trajectory[-1][2], seen=9999., traj=trajectory)
    detector = NewContactDetector()
    detector.observe({"old": old}, 9999.)
    expected_history = [(ob[1], tuple(ob[2])) for ob in trajectory if ob[1] >= 9969.]
    assert detector.history["old"] == (old.s.first_t_s, expected_history)
    observations = expected_history if evicted else [(ob[1], tuple(ob[2])) for ob in trajectory]
    prior = next((ob for ob in reversed(observations) if ob[0] <= first), None)
    box = [5, 0, 10, 10]
    expected = not (old.s.first_t_s < first and prior is not None
                    and first - prior[0] <= detector.memory_s and detector.near(box, prior[1]))
    assert detector.is_new(event(box, first=first), {} if evicted else {"old": old}) is expected


@pytest.mark.parametrize("start,current,expected", [
    ([0, 0, 10, 10], [100, 100, 10, 10], False),
    ([100, 100, 10, 10], [0, 0, 10, 10], True),
])
def test_moving_contact_compares_both_boxes_at_first_observation(start, current, expected):
    own = state(current, first=2., seen=5., traj=[(2, 2., start, .9), (5, 5., current, .9)])
    old = state([0, 0, 10, 10], seen=1.)
    assert NewContactDetector().is_new(event(current), {"new": own, "old": old}) is expected


@pytest.mark.parametrize("config", [{"memory_s": -1}, {"iou_thr": 2}, {"dist_diag": -1}, {"memory_s": float("nan")}])
def test_invalid_config(config):
    with pytest.raises(ValueError):
        NewContactDetector(**config)


def test_service_new_contact_only_on_open_and_source_local(monkeypatch):
    from edgeml.runtime import detectors
    from edgeml.sidecar import service
    import edgeml.tracking as tracking

    base = SimpleNamespace(name="fake", detect=Mock(return_value=[]))
    monkeypatch.setattr(detectors, "load_detector", lambda *a, **kw: base)
    class Tracker:
        def update(self, dets, image, fi, ts):
            return [Track(1, d.box, d.score, d.label, fi, ts) for d in dets]
    monkeypatch.setattr(tracking, "make_tracker", lambda *a, **kw: Tracker())
    monkeypatch.setattr(service, "frame_report", lambda *a, **kw: None)
    engine = service.EdgemlEngine("fake", thermal_fallback=False, live_tiling={"enabled": False},
                                 agg={"min_hits": 1, "close_after_s": .1, "score_jump": .1, "min_update_interval_s": 0})
    image = np.random.default_rng(1).integers(0, 255, (100, 100, 3), dtype=np.uint8)
    responses = []
    for score in [.6, .95, .95, None]:
        base.detect.return_value = [] if score is None else [Detection([20, 20, 20, 20], score, "boat")]
        responses.append(engine.analyze("a", image, None, [], False))
    sightings = [s for r in responses for s in r["sightings"]]
    assert [s["event"] for s in sightings] == ["EVENT_OPEN", "EVENT_UPDATE", "EVENT_DUPLICATE", "EVENT_CLOSE"]
    assert sightings[0]["newContact"] is True
    assert all("newContact" not in s for s in sightings[1:])
    base.detect.return_value = [Detection([20, 20, 20, 20], .8, "boat")]
    assert engine.analyze("b", image, None, [], False)["sightings"][0]["newContact"] is True


def test_cli_new_contact_and_mock_open(monkeypatch):
    from edgeml.sidecar import __main__ as cli, service, server
    from edgeml.runtime import threads
    constructor = Mock()
    monkeypatch.setattr(service, "EdgemlEngine", constructor)
    monkeypatch.setattr(server, "serve", Mock())
    monkeypatch.setattr(threads, "pin_threads", Mock())
    cli.main(["--det", "fake", "--new-contact", '{"memory_s":15}'])
    assert constructor.call_args.kwargs["new_contact"] == {"memory_s": 15}
    engine = service.MockEngine()
    sightings = [s for _ in range(12) for s in engine.analyze("a", np.zeros((8, 8, 3), np.uint8), None, [], False)["sightings"]]
    assert any(s["event"] == "EVENT_OPEN" for s in sightings)
    assert all(s.get("newContact", False) == (s["event"] == "EVENT_OPEN") for s in sightings)
