"""Regression checks for similarity weighting, including experimental overrides."""
import copy
import unittest

from ghost_priority.policy import DEFAULT_POLICY, _similarity, rank_records
from ghost_priority.selection import select_ranked


def observation(name, relevance, fingerprint, size=100):
    return {'id': name, 'source': 'sensor', 'object_relevance': relevance,
            'signals': {'visual_interest': 0.0, 'quality': 1.0},
            'fingerprint': fingerprint, 'payload_bytes': size,
            'detections': None, 'usable': True,
            'phash': {'version': 'phash64-dct-v1', 'hex': '0000000000000000', 'analysis_size': 320}}


class SimilarityTests(unittest.TestCase):
    def test_modest_penalty_can_prefer_a_different_view(self):
        rows = [observation('a', 1.0, [0.0]),
                observation('repeat', .9, [.08]),
                observation('different', .61, [.8])]
        old = rank_records(rows, {'duplicate_strength': .85})
        new = rank_records(rows, {'duplicate_strength': .9})
        self.assertEqual([r['id'] for r in old], ['a', 'repeat', 'different'])
        self.assertEqual([r['id'] for r in new], ['a', 'different', 'repeat'])
        self.assertEqual({r['id']: r['base_score'] for r in old},
                         {r['id']: r['base_score'] for r in new})
        self.assertAlmostEqual(DEFAULT_POLICY['duplicate_strength'], .85)
        self.assertAlmostEqual(DEFAULT_POLICY['duplicate_distance'], .08)

    def test_exact_repeat_retains_positive_score_and_all_observations(self):
        rows = [observation('first', 1.0, [.2]*192),
                observation('repeat', .8, [.2]*192),
                observation('different', .6, [.8]*192)]
        ranked = rank_records(rows)
        repeat = next(r for r in ranked if r['id'] == 'repeat')
        self.assertAlmostEqual(repeat['final_score'], .12)
        self.assertEqual(repeat['max_similarity'], 1)
        self.assertEqual(repeat['nearest_prior_id'], 'first')
        self.assertEqual(len(ranked), len(rows))
        self.assertEqual({r['id'] for r in ranked}, {r['id'] for r in rows})

    def test_order_independent_of_input_order_size_and_selection_budget(self):
        rows = [observation('a', 1.0, [.1]*192, size=300),
                observation('b', .9, [.1]*192, size=100),
                observation('c', .7, [.8]*192, size=200)]
        before = copy.deepcopy(rows)
        ranked = rank_records(rows)
        altered = copy.deepcopy(list(reversed(rows)))
        for row in altered:
            row['payload_bytes'] *= 999
            row['ground_truth'] = {'interesting': False}
        fields = ('id', 'priority_rank', 'base_score', 'final_score', 'max_similarity')
        self.assertEqual([{k: r[k] for k in fields} for r in ranked],
                         [{k: r[k] for k in fields} for r in rank_records(altered)])
        for budget in (0, 100, 300, 400, 600):
            selected = select_ranked(ranked, budget)
            self.assertLessEqual(sum(r['payload_bytes'] for r in selected), budget)
            for row in selected:
                original = ranked[row['priority_rank']-1]
                self.assertEqual(row['final_score'], original['final_score'])
                self.assertEqual(row['max_similarity'], original['max_similarity'])
        self.assertEqual(rows, before)

    def test_small_fingerprint_change_remains_a_known_limit_not_a_hard_gate(self):
        # A small local change is diluted by the mean over the coarse descriptor.
        # This check deliberately documents that stronger suppression DOES NOT
        # solve small-feature preservation; both observations must remain ranked.
        a = [.5]*192
        b = list(a)
        b[0] = .9
        self.assertGreater(_similarity(a, b, .08), .99)
        ranked = rank_records([observation('earlier', 1.0, a),
                               observation('small-change', .9, b)])
        self.assertEqual(len(ranked), 2)
        changed = next(r for r in ranked if r['id'] == 'small-change')
        self.assertGreater(changed['final_score'], 0)
        self.assertLess(changed['final_score'], .15)


if __name__ == '__main__':
    unittest.main()
