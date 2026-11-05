import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import cv2
import numpy as np

from benchmark_real import candidate_rank, image_paths
from benchmark_scale import generated_frames

BASELINE = Path(__file__).resolve().parents[4] / 'algorithm'
if not BASELINE.exists():
    BASELINE = Path(__file__).resolve().parents[1] / 'algorithm'
sys.path.insert(0, str(BASELINE))


class RealBenchmarkTests(unittest.TestCase):
    def test_matches_canonical_rank_with_unusable_and_id_ties(self):
        from ghost_priority.analysis import Analyzer
        from ghost_priority.policy import rank_records, _policy
        from ghost_priority.similarity import engine_from_policy
        analyzer = Analyzer(threads=0)
        frames = list(generated_frames(5, 7321))
        records = [{**analyzer.analyze(frame), 'id': f'z-{i:04d}'} for i, frame in enumerate(frames)]
        records.append({**records[0], 'id': 'a-tied'})
        records.append({**analyzer.analyze(np.zeros_like(frames[0])), 'id': 'unusable'})
        engine = engine_from_policy(_policy({'similarity_method': 'color-phash'}))

        class Adapter:
            def compare(self, a, b): return engine.compare(a, b)['score']
            def upper_bound(self, a, b): return self.compare(a, b)

        descriptors = {r['id']: engine.prepare(r) for r in records}
        expected = [{k: r[k] for k in ['id', 'final_score', 'max_similarity', 'nearest_prior_id']}
                    for r in rank_records(records, policy={'similarity_method': 'color-phash'})]
        plain, _ = candidate_rank(records, Adapter(), descriptors, prune=False)
        fast, stats = candidate_rank(records, Adapter(), descriptors, prune=True)
        self.assertEqual(expected, plain)
        self.assertEqual(expected, fast)
        self.assertEqual(stats['possible_pairs'], 15)

    def test_input_count_and_no_annotation_parsing(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'one.jpg').write_bytes(b'not decoded by inventory')
            (root / 'gt.json').write_text('deliberately invalid JSON')
            self.assertEqual(image_paths(root, 1), [root / 'one.jpg'])
            with self.assertRaises(ValueError):
                image_paths(root, 120)

    def test_small_complete_pipeline_and_accounting(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            inputs = root / 'images'
            inputs.mkdir()
            for i, frame in enumerate(generated_frames(4, 7321)):
                self.assertTrue(cv2.imwrite(str(inputs / f'{i:06d}.jpg'), frame))
            out = root / 'result'
            run = subprocess.run([sys.executable, str(Path(__file__).with_name('benchmark_real.py')),
                '--image-dir', str(inputs), '--out', str(out), '--expected-count', '4',
                '--parity-size', '4', '--threads', '1', '--baseline-path', str(BASELINE)],
                capture_output=True, text=True, timeout=60)
            self.assertEqual(run.returncode, 0, run.stdout + run.stderr)
            result = json.loads((out / 'no_objects.json').read_text())
            self.assertEqual(result['status'], 'complete')
            self.assertEqual(len(result['frames']), 4)
            common = result['common_stages']['common_pipeline_ms']['total_seconds']
            for name, method in result['methods'].items():
                prep = method['preparation']['total_seconds'] if method['preparation'] else 0
                total = common + prep + method['ranking']['wall_seconds']
                self.assertEqual(total, method['pipeline_stage_sum_seconds'])
                self.assertEqual(4 / total, method['amortized_input_fps'])
            self.assertTrue(all(r['exact_order_scores_and_nearest_parity']
                                for r in result['parity_checks'].values()))
            again = subprocess.run([sys.executable, str(Path(__file__).with_name('benchmark_real.py')),
                '--image-dir', str(inputs), '--out', str(out), '--expected-count', '4',
                '--baseline-path', str(BASELINE)], capture_output=True, text=True, timeout=10)
            self.assertNotEqual(again.returncode, 0)


if __name__ == '__main__':
    unittest.main()
