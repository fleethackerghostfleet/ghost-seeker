import importlib.util
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


if __name__ == '__main__':
    unittest.main()
