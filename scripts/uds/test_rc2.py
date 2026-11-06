import importlib.util
import hashlib
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch


spec = importlib.util.spec_from_file_location('rc2', Path(__file__).with_name('two-cluster-rc2.py'))
rc2 = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rc2)


class ReleaseRecorderTest(unittest.TestCase):
    def test_redacts_output_without_changing_command(self):
        process = Mock(stdout=iter(['safe output\n', 'password=private-test-value\n']))
        process.wait.return_value = 0
        with tempfile.TemporaryDirectory() as directory, patch.object(rc2, 'LOGS', Path(directory)), \
                patch.object(rc2.subprocess, 'Popen', return_value=process) as popen:
            rc2.run('safe-command', ['example', 'argument'])
            text = (Path(directory)/'command-transcript.log').read_text()
            self.assertIn('$ example argument', text)
            self.assertIn('safe output', text)
            self.assertNotIn('private-test-value', text)
            self.assertIn('[redacted credential-related output]', text)
            self.assertEqual(popen.call_args.args[0], ['example', 'argument'])

    def test_empty_output(self):
        process = Mock(stdout=iter([]))
        process.wait.return_value = 0
        with tempfile.TemporaryDirectory() as directory, patch.object(rc2, 'LOGS', Path(directory)), \
                patch.object(rc2.subprocess, 'Popen', return_value=process):
            rc2.run('empty', ['example'])
            self.assertIn('[exit 0]', (Path(directory)/'command-transcript.log').read_text())

    def test_failure_is_recorded_and_propagated(self):
        process = Mock(stdout=iter(['failure detail\n']))
        process.wait.return_value = 2
        with tempfile.TemporaryDirectory() as directory, patch.object(rc2, 'LOGS', Path(directory)), \
                patch.object(rc2.subprocess, 'Popen', return_value=process):
            with self.assertRaisesRegex(RuntimeError, 'exit 2'):
                rc2.run('failed', ['example'])
            self.assertIn('[exit 2]', (Path(directory)/'command-transcript.log').read_text())


class ReleaseIntegrityTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.assets = self.root/'assets'
        self.logs = self.root/'logs'
        self.assets.mkdir()
        self.logs.mkdir()
        self.names = [f'{kind}-ghost-seeker-{role}-amd64-0.1.0-rc2.tar.zst'
                      for kind in ('uds-bundle', 'zarf-package') for role in ('ship', 'shore')]
        entries = []
        for name in self.names:
            data = name.encode()
            (self.assets/name).write_bytes(data)
            entries.append(hashlib.sha256(data).hexdigest()+'  '+name)
        self.manifest = '\n'.join(entries)+'\n'
        (self.assets/'SHA256SUMS').write_text(self.manifest)
        for key, value in [('ASSETS', self.assets), ('LOGS', self.logs)]:
            context = patch.object(rc2, key, value)
            context.start()
            self.addCleanup(context.stop)

    def test_current_bytes_verified_without_historical_log(self):
        rc2.verify_release_assets()
        self.assertEqual((self.logs/'SHA256-verification.txt').read_text().count(': OK'), 4)

    def test_stale_success_log_does_not_authorize_corrupt_assets(self):
        (self.logs/'SHA256-verification.txt').write_text('old: OK\n'*4)
        (self.assets/self.names[0]).write_bytes(b'corrupt')
        with patch.object(rc2, 'run') as run, self.assertRaisesRegex(ValueError, 'SHA256 mismatch'):
            rc2.bootstrap()
        run.assert_not_called()

    def test_missing_asset_fails_before_commands(self):
        (self.assets/self.names[0]).unlink()
        with patch.object(rc2, 'run') as run, self.assertRaises(FileNotFoundError):
            rc2.bootstrap()
        run.assert_not_called()

    def test_missing_duplicate_or_invalid_manifest_entry_fails(self):
        for manifest in ['', self.manifest+self.manifest.splitlines()[0]+'\n',
                         self.manifest.replace(self.manifest.split()[0], 'invalid', 1)]:
            with self.subTest(manifest=manifest):
                (self.assets/'SHA256SUMS').write_text(manifest)
                with patch.object(rc2, 'run') as run, self.assertRaises(ValueError):
                    rc2.bootstrap()
                run.assert_not_called()


class BootstrapPreflightTest(unittest.TestCase):
    def test_low_or_invalid_inotify_limit_prevents_all_commands(self):
        with tempfile.TemporaryDirectory() as directory:
            limit = Path(directory)/'inotify'
            for value in ['128', '', 'invalid']:
                with self.subTest(value=value):
                    limit.write_text(value)
                    with patch.object(rc2, 'INOTIFY_LIMIT', limit), patch.object(rc2, 'run') as run, \
                            self.assertRaises((RuntimeError, ValueError)):
                        rc2.bootstrap()
                    run.assert_not_called()

    def test_supported_inotify_limit_allows_bootstrap(self):
        with tempfile.TemporaryDirectory() as directory:
            limit = Path(directory)/'inotify'
            limit.write_text('1024')
            with patch.object(rc2, 'INOTIFY_LIMIT', limit), patch.object(rc2, 'verify_release_assets'), \
                    patch.object(rc2, 'run') as run:
                rc2.bootstrap()
            self.assertIn('delete-owned-clusters', [call.args[0] for call in run.call_args_list])

    def test_tool_preflight_failure_prevents_deletion(self):
        with tempfile.TemporaryDirectory() as directory:
            limit = Path(directory)/'inotify'
            limit.write_text('1024')
            with patch.object(rc2, 'INOTIFY_LIMIT', limit), patch.object(rc2, 'verify_release_assets'), \
                    patch.object(rc2, 'run', side_effect=RuntimeError('missing tool')) as run, \
                    self.assertRaisesRegex(RuntimeError, 'missing tool'):
                rc2.bootstrap()
            self.assertEqual([call.args[0] for call in run.call_args_list], ['version-k3d'])


if __name__ == '__main__':
    unittest.main()
