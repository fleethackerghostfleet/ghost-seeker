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
    p = AggregatorParams(min_update_interval_s=0, unknown_bonus_age_s=2)
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
