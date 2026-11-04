"""End-to-end input separation and source handling checks."""
import contextlib
import io
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import cv2
import numpy as np

from ghost_priority.cli import main, read_jsonl


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


if __name__ == '__main__':
    unittest.main()
