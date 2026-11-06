import copy
import unittest

from annotation_sensitivity import summarize


def prov(sequence, status, group='recording-a', label_source='teacher+human', **qa):
    return {'sequence': sequence, 'source_group': group,
            'metadata': {'gt_status': status, 'label_source': label_source, 'split': 'dev'},
            'labelinfo': {'label_source': label_source, 'qa': {'gt_status': status, **qa}}}


def row(sequence, window, all_tracks, selected, group='recording-a'):
    return {'sequence': sequence, 'source_group': group, 'window': window,
            'method': 'color-phash', 'bandwidth_bps': 64000, 'selected_images': 1,
            'selected_bytes': 100, 'all_positive_tracks': all_tracks,
            'sampled_positive_tracks': all_tracks, 'selected_positive_tracks': selected}


class AnnotationSensitivityTests(unittest.TestCase):
    def test_micro_counts_group_clustering_and_physical_ids_are_preserved(self):
        provenance = {'inputs': [prov('a', 'complete', same_object_groups=[[1, 2]]), prov('b', 'partial')]}
        rows = [row('a', 0, ['1', '2'], ['1']), row('a', 1, ['1'], ['1']), row('b', 0, ['1'], [])]
        before = copy.deepcopy((provenance, rows))
        result, notes = summarize('test', provenance, rows)
        all_row = next(r for r in result if r['stratum_axis'] == 'all')
        self.assertEqual((all_row['selected_positive_track_windows'], all_row['positive_track_windows']), (2, 4))
        self.assertEqual((all_row['selected_local_track_ids'], all_row['positive_local_track_ids']), (1, 3))
        self.assertEqual(all_row['source_group_count'], 1)
        self.assertEqual(notes[0]['qa_same_object_groups'], [[1, 2]])
        self.assertEqual((provenance, rows), before)

    def test_unlabeled_sequences_are_not_negatives_and_status_conflict_is_exposed(self):
        entry = prov('x', 'complete')
        entry['labelinfo']['qa']['gt_status'] = 'incomplete'
        result, notes = summarize('test', {'inputs': [entry]}, [row('x', 0, [], [])])
        self.assertTrue(notes[0]['status_conflict'])
        group = next(r for r in result if r['stratum_axis'] == 'gt_status_group')
        self.assertEqual(group['stratum'], 'partial_or_incomplete')
        self.assertEqual(group['selected_bytes'], 100)
        self.assertEqual(group['positive_source_group_count'], 0)
        self.assertIsNone(group['positive_track_window_coverage'])
        self.assertIsNone(group['whole_local_track_coverage'])

    def test_duplicates_mismatched_group_and_non_nested_sets_rejected(self):
        provenance = {'inputs': [prov('x', 'partial')]}
        good = row('x', 0, ['1'], [])
        with self.assertRaises(ValueError):
            summarize('test', provenance, [good, good])
        with self.assertRaises(ValueError):
            summarize('test', provenance, [dict(good, source_group='another')])
        with self.assertRaises(ValueError):
            summarize('test', provenance, [dict(good, selected_positive_tracks=['2'])])


if __name__ == '__main__':
    unittest.main()
