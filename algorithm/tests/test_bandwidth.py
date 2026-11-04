"""Absolute payload allowance and offline selection boundary checks."""
import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from ghost_priority.bandwidth import BANDWIDTH_PRESETS_BPS, bandwidth_budget_bytes
from ghost_priority.cli import main, read_jsonl


class BandwidthTests(unittest.TestCase):
    def test_si_units_and_fractional_seconds_round_down(self):
        self.assertEqual(bandwidth_budget_bytes(64_000, 60), 480_000)
        self.assertEqual(bandwidth_budget_bytes(9_600, 60), 72_000)
        self.assertEqual(bandwidth_budget_bytes(), 15_000_000)
        self.assertEqual(bandwidth_budget_bytes(80, .29), 2)
        self.assertEqual(bandwidth_budget_bytes(8, .125), 0)
        self.assertEqual(bandwidth_budget_bytes(0, 60), 0)

    def test_invalid_rates_and_windows_rejected(self):
        for rate in (-1, 1.5, True, '64000', float('inf'), float('nan')):
            with self.subTest(rate=rate), self.assertRaises(ValueError):
                bandwidth_budget_bytes(rate)
        for seconds in (0, -1, True, '60', float('inf'), float('nan')):
            with self.subTest(seconds=seconds), self.assertRaises(ValueError):
                bandwidth_budget_bytes(64_000, seconds)

    def run_cli(self, args):
        with patch('sys.argv', ['ghost_priority', *args]), contextlib.redirect_stdout(io.StringIO()):
            main()

    def records(self, root, count=3):
        rows = [{'id': str(i), 'source': 's', 'payload_bytes': (i+1)*100,
                 'payload_path': 'unused.jpg', 'width': 10, 'height': 10,
                 'signals': {'novelty': .6, 'quality': 1}, 'detections': [],
                 'fingerprint': [i / 10], 'usable': True} for i in range(count)]
        path = root / 'records.jsonl'
        path.write_text(''.join(json.dumps(r) + '\n' for r in rows))
        return path

    def test_absolute_budget_does_not_scale_with_dataset(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            for count in (3, 5):
                records = self.records(root, count)
                out = root / f'rank-{count}'
                # 80 bits/s for 30 seconds permits 300 bytes, regardless of input size.
                self.run_cli(['rank', '--records', str(records), '--out', str(out),
                              '--bandwidth-bps', '80', '--window-seconds', '30'])
                rows = json.loads((out/'comparison.json').read_text())
                self.assertEqual({r['method'] for r in rows}, {'priority', 'detector', 'novelty', 'uniform'})
                for row in rows:
                    self.assertEqual(row['budget_bytes'], 300)
                    self.assertEqual(row['available_bandwidth_bps'], 80)
                    self.assertLessEqual(row['selected_payload_bytes'], 300)
                    self.assertEqual(row['unused_budget_bytes'], 300-row['selected_payload_bytes'])
                selected = read_jsonl(out/'priority-bandwidth.jsonl')
                self.assertTrue(selected)
                self.assertLessEqual(sum(r['payload_bytes'] for r in selected), 300)
                self.assertIn('80 bps × 30 s (300 bytes)', (out/'report.html').read_text())

    def test_default_and_disconnected_presets(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            records = self.records(root)
            for name, flags, expected_count, expected_bytes in (
                ('default', [], 3, 15_000_000),
                ('disconnected', ['--bandwidth-preset', 'disconnected'], 0, 0),
                ('explicit-zero', ['--bandwidth-bps', '0'], 0, 0),
                ('too-small', ['--bandwidth-bps', '1', '--window-seconds', '1'], 0, 0),
            ):
                out = root / name
                self.run_cli(['rank', '--records', str(records), '--out', str(out), *flags])
                for row in json.loads((out/'comparison.json').read_text()):
                    self.assertEqual(row['budget_bytes'], expected_bytes)
                    self.assertEqual(row['selected_images'], expected_count)
                self.assertEqual(len(read_jsonl(records)), 3)  # Evidence remains available offline.
            self.assertEqual(BANDWIDTH_PRESETS_BPS['ddil'], 64_000)

    def test_invalid_or_conflicting_cli_settings_do_not_create_output(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            records = self.records(root)
            out = root / 'rejected'
            for flags in (['--bandwidth-bps', '-1'], ['--window-seconds', 'nan'],
                          ['--window-seconds', '0'], ['--bandwidth-bps', '1.5'],
                          ['--bandwidth-bps', '64000', '--fractions', '.1'],
                          ['--bandwidth-bps', '64000', '--bandwidth-preset', 'ddil'],
                          ['--fractions', '.1', '--window-seconds', '60']):
                with self.subTest(flags=flags), contextlib.redirect_stderr(io.StringIO()):
                    with self.assertRaises(SystemExit) as caught:
                        self.run_cli(['rank', '--records', str(records), '--out', str(out), *flags])
                    self.assertEqual(caught.exception.code, 2)
                    self.assertFalse(out.exists())


if __name__ == '__main__':
    unittest.main()
