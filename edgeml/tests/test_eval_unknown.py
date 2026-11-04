from pathlib import Path
import sys

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'tools'))
from eval_unknown import measure, pool, verify_split
from edgeml.data.seqs import MotSequence


def sequence(status='complete'):
    return MotSequence('sample', Path('/data/ghost_fleet_5fps/sample'), 1, 3, 100, 100,
                       gt=[(1, 1, [0, 0, 10, 10], 1, 1, 1)], meta={'gt_status': status})


def event(sid, frame, unknown=.8, real=False):
    return dict(sighting_id=sid, frame_idx=frame, unknownness=unknown, unknown_real=real, best_score=.8)


def test_unknown_metrics_use_matched_sightings_and_complete_sea_frames():
    seq = sequence()
    traj = [('known', 1, [0, 0, 10, 10], .8), ('unknown', 1, [0, 0, 10, 10], .8),
            ('noise', 2, [50, 50, 10, 10], .8), ('stable_noise', 3, [50, 50, 10, 10], .8)]
    events = [event('known', 1, .1), event('unknown', 1, real=True),
              event('noise', 2), event('noise', 2), event('stable_noise', 3, real=True)]
    result = measure(seq, events, traj, .5)
    assert result['before']['surfaced_gt'] == result['after']['surfaced_gt'] == 1
    assert result['after']['matched_unknown_flagged'] == 1
    assert result['before']['false_unknown_sea'] == 2
    assert result['after']['false_unknown_sea'] == 1
    assert result['after']['sea_frames'] == 2
    assert pool([result])['after']['unknown_recall'] == 1
    for status in ('partial', 'unspecified'):
        result = measure(sequence(status), events, traj, .5)
        assert result['after']['sea_frames'] == 0
        assert result['after']['false_unknown_sea'] == 0
        assert result['after']['unmatched_noncomplete'] == 1


def test_empty_events_and_ignored_annotations_are_not_false_sea():
    seq = sequence()
    seq.gt.append((2, 2, [20, 20, 10, 10], 0, 1, 1))
    result = measure(seq, [], [], .5)
    assert result['after']['sea_frames'] == 1
    assert pool([result])['after']['object_recall'] == 0
    assert pool([result])['after']['unknown_recall'] is None
    with pytest.raises(ValueError, match='canonical dev'):
        verify_split([seq], 'dev')
