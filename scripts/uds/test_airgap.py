import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


FAKE = '''#!/usr/bin/env python3
import json, os, pathlib, sys
path = pathlib.Path(os.environ['FIREWALL_STATE'])
state = json.loads(path.read_text())
args = sys.argv[1:]
if pathlib.Path(sys.argv[0]).name == 'docker':
    if args[0] == 'ps': print('k3d-uds-ship-server-0')
    elif args[:2] == ['network', 'inspect']: print(os.environ.get('FAKE_SUBNET', '172.19.0.0/16'))
    elif args[0] == 'inspect': print('123')
    else: sys.exit(2)
    sys.exit(0)
assert args.pop(0) == '-n'
realm = 'host'
if args[0] == 'nsenter':
    realm = 'node'
    args = args[args.index('iptables'):]
assert args[:3] == ['iptables', '-w', '5']
args = args[3:]
op, chain = args[:2]
rules = state[realm]
rest = args[2:]
if op == '-N':
    if chain in rules: sys.exit(1)
    rules[chain] = []
elif op == '-X':
    if chain not in rules or rules[chain]: sys.exit(1)
    del rules[chain]
elif chain not in rules: sys.exit(1)
elif op == '-C': sys.exit(0 if rest in rules[chain] else 1)
elif op == '-A': rules[chain].append(rest)
elif op == '-I': rules[chain].insert(int(rest.pop(0))-1, rest)
elif op == '-D': rules[chain].remove(rest)
elif op == '-S': print(chain, rules[chain])
else: sys.exit(2)
path.write_text(json.dumps(state))
'''


class AirgapTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        directory = Path(self.directory.name)
        for name in ('sudo', 'docker'):
            executable = directory/name
            executable.write_text(FAKE)
            executable.chmod(0o700)
        self.state = directory/'firewall.json'
        self.initial = {'host': {'DOCKER-USER': [['-m', 'comment', '--comment', 'unrelated', '-j', 'RETURN']],
                                 'INPUT': [['-p', 'tcp', '--dport', '22', '-j', 'ACCEPT']]},
                        'node': {'OUTPUT': []}}
        self.state.write_text(json.dumps(self.initial))
        self.env = dict(os.environ, PATH=str(directory)+':'+os.environ['PATH'], FIREWALL_STATE=str(self.state))

    def invoke(self, operation, expected=0):
        result = subprocess.run(['bash', str(Path(__file__).with_name('two-cluster-airgap.sh')), operation],
                                env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode, expected, result.stdout+result.stderr)
        return result.stdout

    def test_on_is_idempotent_and_off_restores_unrelated_rules(self):
        self.invoke('on')
        active = json.loads(self.state.read_text())
        self.assertEqual(active['host']['GS-UDS-AG-FWD'][-1][-1], 'DROP')
        self.assertEqual(active['node']['OUTPUT'][0][-1], 'DROP')
        self.invoke('on')
        self.assertEqual(json.loads(self.state.read_text()), active)
        self.assertIn('ON', self.invoke('status'))
        self.invoke('off')
        self.assertEqual(json.loads(self.state.read_text()), self.initial)

    def test_empty_off_and_status_are_safe(self):
        self.invoke('off')
        self.invoke('off')
        self.assertIn('OFF', self.invoke('status'))
        self.assertEqual(json.loads(self.state.read_text()), self.initial)

    def test_invalid_operation_does_not_change_rules(self):
        self.invoke('invalid', expected=1)
        self.assertEqual(json.loads(self.state.read_text()), self.initial)

    def test_unexpected_network_is_rejected(self):
        self.env['FAKE_SUBNET'] = '192.0.2.0/24'
        self.invoke('on', expected=1)
        self.assertEqual(json.loads(self.state.read_text()), self.initial)


if __name__ == '__main__':
    unittest.main()
