import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class TargetNetworkTest(unittest.TestCase):
    def run_dispatch(self, target):
        source = Path(__file__).with_name('two-cluster-up.sh').read_text()
        dispatch = source[source.index('case "${TARGET:-both}"'):]
        prefix = '''set -euo pipefail
up() { echo "created $1"; }
k3d() { echo 'cluster list'; }
docker() {
  [[ "$1 $2" == 'network inspect' ]] || return 1
  shift 2
  while [[ "$1" != --format ]]; do
    case "${TARGET:-both}:$1" in
      shore:k3d-shore-test|ship:k3d-ship-test|both:k3d-shore-test|both:k3d-ship-test) echo "$1";;
      *) echo 'network does not exist on this host' >&2; return 1;;
    esac
    shift
  done
}
'''
        with tempfile.TemporaryDirectory() as directory:
            environment = dict(os.environ, TARGET=target, SHORE_CLUSTER='shore-test',
                               SHIP_CLUSTER='ship-test', LOG_DIR=directory)
            result = subprocess.run(['bash', '-c', prefix+dispatch], env=environment,
                                    text=True, capture_output=True)
            path = Path(directory)/'networks.txt'
            networks = path.read_text().splitlines() if path.exists() else []
            return result, networks

    def test_only_targeted_networks_are_inspected(self):
        for target, expected in [('shore', ['k3d-shore-test']), ('ship', ['k3d-ship-test']),
                                 ('both', ['k3d-shore-test', 'k3d-ship-test'])]:
            with self.subTest(target=target):
                result, networks = self.run_dispatch(target)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(networks, expected)

    def test_invalid_and_default_target_handling(self):
        for target in ['invalid', '']:
            with self.subTest(target=target):
                result, networks = self.run_dispatch(target)
                if target == '':
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(networks, ['k3d-shore-test', 'k3d-ship-test'])
                else:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(networks, [])


if __name__ == '__main__':
    unittest.main()
