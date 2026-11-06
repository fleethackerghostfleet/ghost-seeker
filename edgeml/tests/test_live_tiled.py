from dataclasses import replace

import numpy as np
import pytest

from edgeml.detectors.live_tiled import LiveTiledDetector, LiveTilingConfig, horizon_y, water_people
from edgeml.types import Detection


class Clock:
    t = 0.

    def __call__(self):
        return self.t


class Base:
    name = "fake"

    def __init__(self, clock, costs=(1.5,), dets=()):
        self.clock, self.costs, self.dets = clock, costs, dets
        self.calls = 0

    def detect(self, image):
        self.clock.t += self.costs[min(self.calls, len(self.costs) - 1)]
        self.calls += 1
        return list(self.dets)


def detector(costs=(1.5,), **kw):
    clock = Clock()
    return LiveTiledDetector(Base(clock, costs), LiveTilingConfig(**kw), clock=clock, horizon_fn=lambda _: 400.)


FRAME = np.zeros((1080, 1920, 3), np.uint8)


def test_native_first_admission_and_tile_cap():
    d = detector()
    d.detect(FRAME)
    assert d.n_calls == 2  # 1.5 s native + 1.5 s tile; no budget for tile two
    assert d.last_timing_ms["total"] == 3000
    fast = detector(costs=(.01,))
    fast.detect(FRAME)
    assert fast.n_calls == 3


def test_budget_exhausted_and_sudden_overrun_do_not_start_more_calls():
    for costs, calls in [((4.,), 1), ((1., 5.), 2)]:
        d = detector(costs=costs)
        d.detect(FRAME)
        assert d.n_calls == calls


@pytest.mark.parametrize("kwargs", [{"enabled": False}, {"max_tiles": 0}, {"budget_ms": 0}, {"min_pixels": FRAME.size}])
def test_native_only_switches(kwargs):
    d = detector(**kwargs)
    d.detect(FRAME)
    assert d.n_calls == 1


def test_threshold_boundary_and_empty_context():
    d = detector()
    d.detect(np.zeros((720, 1280, 3), np.uint8))
    assert d.n_calls == 1
    d.horizon_fn = lambda _: None
    d.detect(FRAME)
    assert d.n_calls == 2  # bounded grid fallback; uncertain water context still abstains
    assert horizon_y(np.zeros((8, 8, 3), np.uint8)) is None
    assert horizon_y(FRAME) is None


def test_class_aware_merge_offsets_and_no_mutation():
    clock = Clock()
    person = Detection([10, 100, 20, 20], .8, "person")
    boat = replace(person, label="boat")
    d = LiveTiledDetector(Base(clock, (.01,), [person, boat]), clock=clock, horizon_fn=lambda _: 400.)
    out = d.detect(FRAME)
    assert len(out) == 6  # three origins, two classes; no cross-class NMS
    assert any(o.box == [10, 372, 20, 20] for o in out)
    assert person.box == [10, 100, 20, 20]
    first = {tuple(o.box) for o in out}
    assert {tuple(o.box) for o in d.detect(FRAME)} != first  # horizon coverage rotates


def test_possible_mob_keeps_confidence_and_rejects_aboard_above_or_uncertain():
    p = Detection([100, 500, 20, 20], .6, "person")
    assert water_people([p], 400, 1080, .35)[0].label == "person in water"
    assert p.label == "person"
    assert water_people([p], 400, 1080, .35)[0].score == .6
    assert water_people([p], None, 1080, .35)[0].label == "person"
    assert water_people([replace(p, box=[100, 200, 20, 20])], 400, 1080, .35)[0].label == "person"
    boat = Detection([90, 490, 100, 100], .9, "boat")
    assert water_people([p, boat], 400, 1080, .35)[0].label == "person"


@pytest.mark.parametrize("kwargs", [{"tile": 0}, {"overlap": 1}, {"max_tiles": -1}, {"budget_ms": -1},
                                   {"tile": 640.5}, {"max_tiles": 1.5}, {"enabled": "false"}, {"overlap": .99999},
                                   {"budget_ms": float("nan")}, {"cost_margin": .5}, {"merge_iou": 0}])
def test_config_validation(kwargs):
    with pytest.raises(ValueError):
        LiveTilingConfig(**kwargs)
