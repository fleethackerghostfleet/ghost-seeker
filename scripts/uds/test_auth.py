import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('two_cluster_auth', ROOT/'scripts/uds/two-cluster-auth.py')
auth = importlib.util.module_from_spec(spec)
spec.loader.exec_module(auth)


class AuthTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.state = Path(self.directory.name)
        self.environment = {'ROOT': str(ROOT), 'STATE': str(self.state),
                            'SHORE_CONTEXT': 'shore', 'SHIP_CONTEXT': 'ship',
                            'SHORE_NAMESPACE': 'shore-ns', 'SHIP_NAMESPACE': 'ship-ns'}
        self.calls = []
        self.addCleanup(os.umask, os.umask(0o077))

    def run_auth(self):
        def apply(command, **kwargs):
            self.calls.append((command, json.loads(kwargs['input'])))
        with patch.dict(os.environ, self.environment), patch.object(auth.subprocess, 'run', side_effect=apply):
            auth.main()

    def test_generate_and_reuse(self):
        self.run_auth()
        paths = list((self.state/'auth').iterdir())
        self.assertEqual({path.name for path in paths}, {'signing-key', 'operator-token', 'unit-token'})
        original = {path.name: path.read_text() for path in paths}
        for path in paths:
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        secrets = [(cmd, doc) for cmd, doc in self.calls if doc['kind'] == 'Secret']
        self.assertEqual(len(secrets), 3)
        for command, document in secrets:
            ship = document['metadata']['name'] == 'edge-auth'
            self.assertEqual(command[2], 'ship' if ship else 'shore')
            self.assertEqual(document['metadata']['namespace'], 'ship-ns' if ship else 'shore-ns')
        self.run_auth()
        self.assertEqual({path.name: path.read_text() for path in paths}, original)

    def test_incomplete_auth(self):
        (self.state/'auth').mkdir()
        (self.state/'auth/signing-key').write_text('test-key')
        with self.assertRaisesRegex(ValueError, 'incomplete auth'):
            self.run_auth()
        self.assertEqual(self.calls, [])

    def test_repository_path_rejected(self):
        self.environment['STATE'] = str(ROOT/'forbidden-auth')
        with self.assertRaisesRegex(ValueError, 'outside the repository'):
            self.run_auth()
        self.assertEqual(self.calls, [])


if __name__ == '__main__':
    unittest.main()
