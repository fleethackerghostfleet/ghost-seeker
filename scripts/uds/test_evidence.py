import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location('evidence', Path(__file__).with_name('two-cluster-evidence.py'))
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)


class EvidenceTest(unittest.TestCase):
    def test_arrival_order_and_updates(self):
        def event(identity, cursor, score):
            return 'data: '+json.dumps({'cursor': str(cursor), 'event': 'created',
                                       'item': {'id': identity, 'tier': 'P1', 'score': score,
                                                'source': 'camera', 'contacts': []}})+'\n\n'
        text = ': connected\n\n'+event('old', 1, 0.9)+event('high', 2, 0.8)+event('high', 3, 0.8)+event('low', 4, 0.2)
        result = evidence.arrivals(text, {'high', 'low'})
        self.assertEqual([row['id'] for row in result], ['high', 'low'])
        self.assertEqual([row['cursor'] for row in result], ['2', '4'])

    def test_empty_stream(self):
        self.assertEqual(evidence.arrivals(': heartbeat\n\n', {'missing'}), [])

    def test_malformed_event_rejected(self):
        with self.assertRaises(json.JSONDecodeError):
            evidence.arrivals('data: invalid\n', {'item'})

    def test_separate_edge_operator_token(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            (state/'auth').mkdir()
            (state/'auth/operator-token').write_text('shore-test-value')
            (state/'auth/edge-operator-token').write_text('edge-test-value')
            with patch.object(evidence, 'STATE', state):
                client = evidence.Capture()
            self.assertIn('edge-test-value', client.command(evidence.EDGE+'/api/v1/items')[1])
            self.assertIn('shore-test-value', client.command(evidence.SHORE+'/api/v1/items')[1])
            self.assertEqual(client.command(evidence.EDGE+'/api/v1/items', authenticated=False)[1], '')

    def test_legacy_shared_operator_token(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            (state/'auth').mkdir()
            (state/'auth/operator-token').write_text('shared-test-value')
            with patch.object(evidence, 'STATE', state):
                client = evidence.Capture()
            self.assertEqual(client.config, client.edge_config)

    def test_missing_operator_token_fails_closed(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(evidence, 'STATE', Path(directory)):
            with self.assertRaises(FileNotFoundError):
                evidence.Capture()


if __name__ == '__main__':
    unittest.main()
