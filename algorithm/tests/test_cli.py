"""End-to-end input separation and source handling checks."""
import contextlib
import io
import json
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch

import cv2
import numpy as np

from ghost_priority.cli import main, read_jsonl
from ghost_priority.contracts import ObjectResult, VisualResult


class CliTests(unittest.TestCase):
    def run_cli(self, args):
        with patch('sys.argv', ['ghost_priority', *args]), contextlib.redirect_stdout(io.StringIO()):
            main()

    def test_annotations_do_not_become_evidence_and_ranking_needs_no_inference(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            image = np.zeros((80, 120, 3), np.uint8)
            image[:, :60] = [40, 80, 120]
            cv2.imwrite(str(root/'input.jpg'), image)
            manifest = root/'manifest.jsonl'
            manifest.write_text(json.dumps({'id': 'a', 'path': 'input.jpg', 'source': 's',
                                           'detections': [{'label': 'ground_truth', 'confidence': 1}],
                                           'interesting': True}) + '\n')
            self.run_cli(['analyze', '--manifest', str(manifest), '--out', str(root/'evidence')])
            records = read_jsonl(root/'evidence/records.jsonl')
            self.assertIsNone(records[0]['detections'])
            self.assertNotIn('interesting', records[0])
            self.assertEqual(records[0]['payload_bytes'], (root/'evidence'/records[0]['payload_path']).stat().st_size)
            labels = root/'labels.json'
            labels.write_text(json.dumps({'a': {'interesting': True, 'classes': ['boat']}}))
            with patch('ghost_priority.analysis.Analyzer.analyze', side_effect=AssertionError('Inference repeated')):
                self.run_cli(['rank', '--records', str(root/'evidence/records.jsonl'), '--out', str(root/'ranked'),
                              '--fractions', '1', '--labels', str(labels)])
            selected = read_jsonl(root/'ranked/priority-1.000000.jsonl')
            self.assertEqual([r['id'] for r in selected], ['a'])
            self.assertTrue((root/'ranked/report.html').is_file())

    def test_video_sampling_uses_frame_timestamps(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            path = root/'clip.avi'
            writer = cv2.VideoWriter(str(path), cv2.VideoWriter_fourcc(*'MJPG'), 4, (64, 48))
            if not writer.isOpened():
                self.skipTest('MJPG writer unavailable')
            for i in range(8):
                img = np.zeros((48, 64, 3), np.uint8)
                img[8:30, 5+i:20+i] = [30, 80, 180]
                writer.write(img)
            writer.release()
            self.run_cli(['analyze', '--video', str(path), '--out', str(root/'evidence'), '--fps', '1'])
            records = read_jsonl(root/'evidence/records.jsonl')
            self.assertEqual([r['frame_index'] for r in records], [0, 4])
            self.assertEqual([r['timestamp_s'] for r in records], [0, 1])

    def test_custom_providers_cache_evidence_and_select_saved_ranking_without_models(self):
        calls = {'object': 0, 'visual': 0, 'quality': 0}

        class Objects:
            name = 'test-objects-v1'
            def analyze(self, bgr):
                calls['object'] += 1
                return ObjectResult(relevance=.8)

        class Visual:
            name = 'test-visual-v1'
            def analyze(self, bgr):
                calls['visual'] += 1
                return VisualResult(interest=.25, components={'test_component': .1})

        class Quality:
            name = 'test-quality-v1'
            def analyze(self, bgr):
                calls['quality'] += 1
                return .9

        plugin = types.ModuleType('test_teammate_providers')
        plugin.objects, plugin.visual, plugin.quality = Objects, Visual, Quality
        with tempfile.TemporaryDirectory() as td, patch.dict('sys.modules', {plugin.__name__: plugin}):
            root = Path(td)
            inputs = root/'images'
            inputs.mkdir()
            for i in range(2):
                image = np.full((40, 64, 3), 30 + 150*i, np.uint8)
                image[5:15, 8:22] = 100
                cv2.imwrite(str(inputs/f'{i}.jpg'), image)
            self.run_cli(['analyze', '--images', str(inputs), '--out', str(root/'evidence'),
                          '--object-provider', plugin.__name__+':objects',
                          '--visual-provider', plugin.__name__+':visual',
                          '--quality-provider', plugin.__name__+':quality'])
            evidence = read_jsonl(root/'evidence/records.jsonl')
            self.assertEqual(calls, {'object': 2, 'visual': 2, 'quality': 2})
            self.assertTrue(all(r['object_relevance'] == .8 for r in evidence))
            self.assertTrue(all(r['signals']['visual_interest'] == .25 for r in evidence))
            self.assertEqual(evidence[0]['providers']['object'], 'test-objects-v1')
            with patch('ghost_priority.analysis.Analyzer.analyze', side_effect=AssertionError('Inference repeated')):
                self.run_cli(['rank', '--records', str(root/'evidence/records.jsonl'),
                              '--ranking-only', '--out', str(root/'ranking')])
            ranked_path = root/'ranking/ranked-priority.jsonl'
            ranked = read_jsonl(ranked_path)
            self.assertEqual(len(ranked), 2)
            self.assertEqual([r['priority_rank'] for r in ranked], [1, 2])
            self.assertAlmostEqual(ranked[0]['base_score'], .792)
            self.assertTrue(all(Path(r['payload_path']).is_file() for r in ranked))
            self.assertFalse((root/'ranking/comparison.json').exists())
            with patch('ghost_priority.policy.rank_records', side_effect=AssertionError('Ranking repeated')):
                self.run_cli(['select', '--ranked', str(ranked_path), '--bandwidth-bps', '0',
                              '--out', str(root/'offline')])
                self.run_cli(['select', '--ranked', str(ranked_path), '--bandwidth-preset', 'normal',
                              '--out', str(root/'connected')])
            self.assertEqual(read_jsonl(root/'offline/selected.jsonl'), [])
            selected = read_jsonl(root/'connected/selected.jsonl')
            self.assertEqual([r['final_score'] for r in selected], [r['final_score'] for r in ranked])
            self.assertEqual([r['priority_rank'] for r in selected], [1, 2])
            self.assertEqual(calls, {'object': 2, 'visual': 2, 'quality': 2})

    def test_select_preserves_rank_gaps_and_scores(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            rows = [{'id': str(i), 'source': 's', 'priority_rank': i,
                     'payload_bytes': size, 'payload_path': 'unused.jpg', 'width': 10, 'height': 10,
                     'score': score, 'base_score': score, 'final_score': score,
                     'ranking_method': 'priority', 'max_similarity': 0, 'reasons': []}
                    for i, size, score in [(1, 300, .9), (2, 200, .8), (3, 100, .7)]]
            path = root/'ranked.jsonl'
            path.write_text(''.join(json.dumps(r)+'\n' for r in rows))
            with patch('ghost_priority.policy.rank_records', side_effect=AssertionError('Must not rerank')):
                self.run_cli(['select', '--ranked', str(path), '--bandwidth-bps', '3200',
                              '--window-seconds', '1', '--out', str(root/'selected')])
            selected = read_jsonl(root/'selected/selected.jsonl')
            self.assertEqual([r['priority_rank'] for r in selected], [1, 3])
            self.assertEqual([r['selection_rank'] for r in selected], [1, 2])
            self.assertEqual([r['final_score'] for r in selected], [.9, .7])
            self.assertIn('Priority #3 · selection #2', (root/'selected/report.html').read_text())

    def test_comparison_ranks_once_per_method_across_budgets(self):
        from ghost_priority.policy import rank_records
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            records = [{'id': str(i), 'source': 's', 'payload_bytes': 100,
                        'payload_path': 'unused.jpg', 'width': 10, 'height': 10,
                        'signals': {'visual_interest': .1+i*.1, 'quality': 1},
                        'detections': [], 'fingerprint': [float(i)]} for i in range(4)]
            path = root/'records.jsonl'
            path.write_text(''.join(json.dumps(r)+'\n' for r in records))
            with patch('ghost_priority.policy.rank_records', wraps=rank_records) as ranker:
                self.run_cli(['rank', '--records', str(path), '--fractions', '.25', '.5', '1',
                              '--out', str(root/'comparison')])
                self.assertEqual(ranker.call_count, 4)
            full = read_jsonl(root/'comparison/ranked-priority.jsonl')
            for fraction, count in [('0.250000', 1), ('0.500000', 2), ('1.000000', 4)]:
                selected = read_jsonl(root/f'comparison/priority-{fraction}.jsonl')
                self.assertEqual([r['id'] for r in selected], [r['id'] for r in full[:count]])

    def test_ranking_only_rejects_bandwidth_or_labels(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            path = root/'records.jsonl'
            path.write_text(json.dumps({'id': 'x', 'source': 's', 'payload_bytes': 100})+'\n')
            labels = root/'labels.json'
            labels.write_text('{}')
            for extra in (['--bandwidth-bps', '0'], ['--window-seconds', '60'], ['--labels', str(labels)]):
                with self.subTest(extra=extra), contextlib.redirect_stderr(io.StringIO()):
                    with self.assertRaises(SystemExit) as caught:
                        self.run_cli(['rank', '--records', str(path), '--ranking-only',
                                      '--out', str(root/'bad'), *extra])
                    self.assertEqual(caught.exception.code, 2)
                    self.assertFalse((root/'bad').exists())


if __name__ == '__main__':
    unittest.main()
