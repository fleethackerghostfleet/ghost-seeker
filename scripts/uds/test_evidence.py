import importlib.util
import json
from pathlib import Path
import unittest


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


if __name__ == '__main__':
    unittest.main()
