import math

import pytest

from edgeml.tracking import AggregatorParams, SightingAggregator
from edgeml.tracking.realness import trajectory_realness
from edgeml.types import Track


def trajectory(n=8):
    return [(i, i * .5, [100 + i * 2, 100, 20, 10], .2) for i in range(n)]


def test_realness_persistence_and_constant_velocity():
    assert trajectory_realness(trajectory()) == pytest.approx(1)
    assert trajectory_realness([]) == 0
    assert trajectory_realness(trajectory(1)) == 0
    assert trajectory_realness(trajectory(3)) == 0
    assert trajectory_realness([trajectory()[0]] * 8) == 0
    stationary = [(i, t, [100, 100, 20, 10], s) for i, t, b, s in trajectory()]
    assert trajectory_realness(stationary) == pytest.approx(1)


def test_realness_rejects_noise_and_invalid_boxes():
    jitter = [(i, t, [100 + (i % 2) * 100, 100, 20, 10], s) for i, t, b, s in trajectory()]
    sizes = [(i, t, [100, 100, 5 if i % 2 else 100, 5 if i % 2 else 100], s)
             for i, t, b, s in trajectory()]
    assert trajectory_realness(jitter) < .5
    assert trajectory_realness(sizes) < .5
    for bad in (float('nan'), 0, -1):
        rows = trajectory()
        rows[-1][2][2] = bad
        assert trajectory_realness(rows) == 0


def test_unknown_gate_emits_material_update_and_value_floor():
    p = AggregatorParams(min_update_interval_s=0, unknown_bonus_age_s=2,
                         gate_unknown_bonus_on_realness=True)
    a = SightingAggregator('unknown', p)
    events = []
    for i, t, box, score in trajectory():
        events += a.update([Track(1, box, score, 'object', i, t)], None, i, t)
    assert not events[0]['unknown_real']
    real = [e for e in events if e['unknown_real']]
    assert real and real[0]['reason'] == 'unknown_real'
    assert real[0]['value'] >= .3
    assert real[0]['realness'] == pytest.approx(1)
    assert a.flush(10)[0]['unknown_real']
    st = next(iter(a.states.values()))
    st.traj = trajectory(2)
    st.s.best_score = 0
    st.s.unknownness = 1
    value_unknown_noise = a._value(st)
    st.s.unknownness = 0
    assert a._value(st) == value_unknown_noise
    assert not st.s.unknown_real


def test_default_unknown_real_transition_does_not_emit_update():
    a = SightingAggregator('unknown', AggregatorParams(min_update_interval_s=0))
    events = []
    for i, t, box, score in trajectory():
        events += a.update([Track(1, box, score, 'object', i, t)], None, i, t)
    assert [e['event'] for e in events] == ['open']
    assert not events[0]['unknown_real']
    closed = a.flush(10)
    assert [e['event'] for e in closed] == ['close']
    assert closed[0]['unknown_real']
    assert closed[0]['realness'] == pytest.approx(1)


def test_real_trajectory_below_unknown_threshold_has_no_unknown_bonus():
    a = SightingAggregator('known', AggregatorParams(gate_unknown_bonus_on_realness=True))
    for i, t, box, score in trajectory():
        a.update([Track(1, box, score, 'object', i, t)], None, i, t)
    st = next(iter(a.states.values()))
    st.s.unknownness = 0
    baseline = a._value(st)
    st.s.unknownness = a.p.unknown_threshold - .01
    assert a._value(st) == baseline
    assert st.s.realness == pytest.approx(1)
    assert not st.s.unknown_real
    st.s.unknownness = a.p.unknown_threshold
    assert a._value(st) > baseline
    assert st.s.unknown_real


@pytest.mark.parametrize('rows,unknownness,expected_real', [
    (trajectory(), .49, False),
    ([(i, t, [100 + (i % 2) * 100, 100, 20, 10], s)
      for i, t, b, s in trajectory()], .8, False),
    ([(0, 0, [100, 100, 20, 10], .2), (1, 3, [100, 100, 20, 10], .2)], .8, False),
    (trajectory(), .5, True),
    (trajectory(2), .8, False),
])
def test_default_value_matches_main_despite_realness(rows, unknownness, expected_real):
    a = SightingAggregator('default')
    assert not a.p.gate_unknown_bonus_on_realness
    for i, t, box, score in trajectory():
        a.update([Track(1, box, score, 'object', i, t)], None, i, t)
    st = next(iter(a.states.values()))
    st.traj = rows
    st.s.first_t_s, st.s.last_t_s = rows[0][1], rows[-1][1]
    st.s.best_score = 0
    st.s.unknownness = unknownness
    size = min(1.0, math.sqrt(max(1.0, st.s.best_box[2] * st.s.best_box[3])) / (0.15 * a._W()))
    age = st.s.last_t_s - st.s.first_t_s
    unk = unknownness if age >= a.p.unknown_bonus_age_s else 0.0
    closing = min(1.0, max(0.0, a._growth(st) / .3))
    expected = min(1.0, max(0.0, .5 * st.s.best_score + .2 * unk + .2 * size + .1 * closing))
    assert a._value(st) == pytest.approx(expected)
    assert st.s.unknown_real == expected_real
    if expected_real:
        assert expected < .3 + .2 * size
