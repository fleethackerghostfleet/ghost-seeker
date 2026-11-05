import copy
import contextlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import cv2
import numpy as np

from ghost_priority.analysis import Analyzer
from ghost_priority.cli import main, read_jsonl
from ghost_priority.policy import rank_records, load_policy
from ghost_priority.similarity import CachedSimilarity
from ghost_priority.similarity_evaluation import evaluate_pairs
from ghost_priority.similarity_features import describe


def row(key, color=.5, hash_value=0, relevance=1):
    return {'id': key, 'fingerprint': [color]*192,
            'phash': {'version': 'phash64-dct-v1', 'hex': f'{hash_value:016x}', 'analysis_size': 320},
            'object_relevance': relevance, 'signals': {'visual_interest': 0, 'quality': 1}}


class HybridSimilarityTests(unittest.TestCase):
    def test_default_combines_color_and_phash_and_audits_limiting_pair(self):
        records = [row('first'), row('same', relevance=.9),
                   row('different-structure', hash_value=(1 << 32)-1, relevance=.8)]
        before = copy.deepcopy(records)
        ranked = rank_records(records)
        self.assertEqual(load_policy()['similarity_method'], 'color-phash')
        self.assertEqual([r['id'] for r in ranked], ['first', 'different-structure', 'same'])
        self.assertEqual(ranked[-1]['max_similarity'], 1)
        self.assertAlmostEqual(ranked[-1]['final_score'], .9*.15)
        self.assertEqual(ranked[-1]['similarity_match']['phash_distance_bits'], 0)
        self.assertEqual(ranked[1]['similarity_match']['phash_distance_bits'], 32)
        self.assertLess(ranked[1]['max_similarity'], 1e-6)
        self.assertEqual(records, before)

    def test_either_component_can_limit_redundancy(self):
        engine = CachedSimilarity()
        origin = engine.prepare(row('a'))
        color_changed = engine.compare(origin, engine.prepare(row('b', color=.9)))
        shape_changed = engine.compare(origin, engine.prepare(row('c', hash_value=(1 << 32)-1)))
        self.assertEqual(color_changed['phash_similarity'], 1)
        self.assertEqual(shape_changed['color_similarity'], 1)
        for match in (color_changed, shape_changed):
            self.assertEqual(match['score'], min(match['color_similarity'], match['phash_similarity']))
            self.assertLess(match['score'], 1e-6)

    def test_hamming_extremes_symmetry_and_mixed_preprocessing(self):
        engine = CachedSimilarity('phash')
        a, b = engine.prepare(row('a')), engine.prepare(row('b', hash_value=(1 << 64)-1))
        self.assertEqual(engine.compare(a, b)['phash_distance_bits'], 64)
        self.assertEqual(engine.compare(a, b), engine.compare(b, a))
        self.assertEqual(engine.compare(a, a)['score'], 1)
        different = row('c')
        different['phash']['analysis_size'] = 160
        with self.assertRaisesRegex(ValueError, 'analysis sizes'):
            engine.compare(a, engine.prepare(different))

    def test_old_caches_require_explicit_migration_or_color_override(self):
        legacy = row('old')
        del legacy['phash']
        with self.assertRaisesRegex(ValueError, 'cache-similarity'):
            rank_records([legacy])
        self.assertEqual(rank_records([legacy], {'similarity_method': 'color'})[0]['id'], 'old')

    def test_malformed_descriptors_and_policy_rejected(self):
        for field, values in [('hex', ['0', 'g'*16, 5, None]),
                              ('version', ['unknown', None]), ('analysis_size', [True, 0, 320.0])]:
            for value in values:
                candidate = row('a')
                candidate['phash'][field] = value
                with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                    rank_records([candidate])
        for value in (0, -1, 65, True, float('nan'), '8'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                load_policy_overrides = {'phash_distance_scale': value}
                rank_records([], load_policy_overrides)

    def test_default_descriptor_is_deterministic_and_brightness_tolerant(self):
        image = np.random.default_rng(4).integers(20, 180, (180, 320, 3), dtype=np.uint8)
        original = image.copy()
        color, phash = describe(image)
        result = Analyzer().analyze(image)
        self.assertEqual(result['fingerprint'], color)
        self.assertEqual(result['phash'], phash)
        self.assertEqual(describe(image), describe(image.copy()))
        _, bright = describe(image+25)
        self.assertLessEqual((int(phash['hex'], 16)^int(bright['hex'], 16)).bit_count(), 4)
        np.testing.assert_array_equal(image, original)

    def test_cached_ranking_imports_no_cv_runtime(self):
        script = '''
import builtins, json
original = builtins.__import__
def restricted(name, *args, **kwargs):
    if name.split('.')[0] in {'cv2', 'numpy'}:
        raise AssertionError('Image runtime imported during ranking')
    return original(name, *args, **kwargs)
builtins.__import__ = restricted
from ghost_priority.policy import rank_records
records = json.loads(input())
assert len(rank_records(records)) == 2
'''
        result = subprocess.run([sys.executable, '-c', script], input=json.dumps([row('a'), row('b')]),
                                text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_pair_evaluation_separates_false_suppression_and_unknown(self):
        records = [row('a'), row('same'), row('change'), row('uncertain')]
        pairs = [{'a': 'a', 'b': 'same', 'label': 'same_information'},
                 {'a': 'a', 'b': 'change', 'label': 'changed_information'},
                 {'a': 'a', 'b': 'uncertain', 'label': 'uncertain'}]
        report = evaluate_pairs(records, pairs, [.5])
        metric = next(r for r in report['metrics'] if r['group'] is None and r['method'] == 'color-phash')
        self.assertEqual(metric['duplicate_candidates'], 1)
        self.assertEqual(metric['false_suppression_candidates'], 1)
        self.assertEqual(metric['uncertain_candidates'], 1)
        self.assertEqual(metric['changed_pair_false_suppression_rate'], 1)
        # Identical global summaries can conceal changed information. Evaluation
        # must report that miss instead of treating the reviewed change as a duplicate.
        self.assertEqual(metric['duplicate_candidate_recall'], 1)

    def test_pair_evaluation_rejects_bad_labels_and_duplicate_pairs(self):
        records = [row('a'), row('b')]
        pair = {'a': 'a', 'b': 'b', 'label': 'uncertain'}
        for pairs in ([], [pair, {**pair, 'a': 'b', 'b': 'a'}], [{**pair, 'label': 'auto_duplicate'}], [{**pair, 'b': 'missing'}]):
            with self.subTest(pairs=pairs), self.assertRaises(ValueError):
                evaluate_pairs(records, pairs)

    def run_cli(self, args):
        with patch('sys.argv', ['ghost_priority', *args]), contextlib.redirect_stdout(io.StringIO()):
            main()

    def test_backfill_preserves_evidence_and_requires_matching_source(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            image = np.random.default_rng(5).integers(0, 256, (80, 120, 3), dtype=np.uint8)
            cv2.imwrite(str(root/'source.png'), image)
            record = {'id': 'x', 'input_path': 'source.png', 'payload_path': 'payload.jpg',
                      **Analyzer().analyze(image)}
            record.pop('phash')
            (root/'records.jsonl').write_text(json.dumps(record)+'\n')
            with patch('ghost_priority.analysis.Analyzer.analyze', side_effect=AssertionError('Repeated providers')):
                self.run_cli(['cache-similarity', '--records', str(root/'records.jsonl'), '--out', str(root/'cached')])
            updated = read_jsonl(root/'cached/records.jsonl')[0]
            for field in ('signals', 'detections', 'fingerprint', 'timing_ms', 'regions', 'object_relevance'):
                self.assertEqual(updated[field], record[field])
            self.assertEqual(updated['phash'], describe(image)[1])
            self.assertEqual(updated['payload_path'], str((root/'payload.jpg').resolve()))
            cv2.imwrite(str(root/'source.png'), np.zeros_like(image))
            with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                self.run_cli(['cache-similarity', '--records', str(root/'records.jsonl'), '--out', str(root/'bad')])
            self.assertFalse((root/'bad').exists())

    def test_evaluation_cli_has_no_inference_and_does_not_change_policy(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root/'records.jsonl').write_text('\n'.join(json.dumps(r) for r in [row('a'), row('b')]))
            (root/'pairs.json').write_text(json.dumps([{'a': 'a', 'b': 'b', 'label': 'uncertain'}]))
            before = load_policy()
            with patch('ghost_priority.analysis.Analyzer.analyze', side_effect=AssertionError('Inference')):
                self.run_cli(['evaluate-similarity', '--records', str(root/'records.jsonl'),
                              '--pairs', str(root/'pairs.json'), '--out', str(root/'evaluation')])
            self.assertTrue((root/'evaluation/report.html').is_file())
            self.assertEqual(before, load_policy())

    def test_video_cache_migration_rebuilds_original_indexed_frames(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            video = root/'source.avi'
            writer = cv2.VideoWriter(str(video), cv2.VideoWriter_fourcc(*'MJPG'), 4, (64, 48))
            if not writer.isOpened():
                self.skipTest('MJPG writer unavailable')
            for i in range(8):
                frame = np.full((48, 64, 3), i*20, np.uint8)
                frame[12:30, 5+i:20+i] = [150, 75, 220]
                writer.write(frame)
            writer.release()
            self.run_cli(['analyze', '--video', str(video), '--fps', '1', '--out', str(root/'original')])
            expected = read_jsonl(root/'original/records.jsonl')
            legacy = copy.deepcopy(expected)
            for record in legacy:
                record.pop('phash')
                record['payload_path'] = str(root/'original'/record['payload_path'])
            (root/'legacy.jsonl').write_text('\n'.join(json.dumps(r) for r in legacy))
            with patch('ghost_priority.analysis.Analyzer.analyze', side_effect=AssertionError('Repeated inference')):
                self.run_cli(['cache-similarity', '--records', str(root/'legacy.jsonl'), '--out', str(root/'cached')])
            migrated = read_jsonl(root/'cached/records.jsonl')
            self.assertEqual([r['frame_index'] for r in migrated], [0, 4])
            self.assertEqual([r['phash'] for r in migrated], [r['phash'] for r in expected])


if __name__ == '__main__':
    unittest.main()
