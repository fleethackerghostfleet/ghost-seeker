#!/usr/bin/env python3
"""Release-only rc2 runbook driver. Never builds or packages application images."""
import hashlib
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[2]
ASSETS = Path.home()/'.local/share/uds-rc2'
STATE = ASSETS/'state'
REPORT = ROOT/'docs/reports/uds-validation/rc2'
LOGS = REPORT/'logs'
CONFIG = ASSETS/'config'
INOTIFY_LIMIT = Path('/proc/sys/fs/inotify/max_user_instances')
ENV = dict(os.environ, STATE=str(STATE), SHORE_CONTEXT='k3d-uds-shore', SHIP_CONTEXT='k3d-uds-ship',
           SHORE_NS='ghost-seeker-shore', SHIP_NS='ghost-seeker-ship', EDGE_ID='ship-001')


def run(name, command, extra=None):
    extra = extra or {}
    command = [str(arg) for arg in command]
    display = ' '.join(shlex.quote(k+'='+str(v)) for k, v in extra.items())
    display = '$ '+(display+' ' if display else '')+shlex.join(command)+'\n'
    with (LOGS/'command-transcript.log').open('a') as transcript, (LOGS/(name+'.log')).open('w') as output:
        transcript.write(display)
        if name == 'runbook-auth':
            transcript.write('# Verbatim auth script (tracing disabled; no expanded values):\n'+
                             (ROOT/'scripts/uds/two-cluster-rc2-auth.sh').read_text()+'\n')
        output.write(display)
        transcript.flush()
        process = subprocess.Popen(command, cwd=ROOT, env=dict(ENV, **extra), stdout=subprocess.PIPE,
                                   stderr=subprocess.STDOUT, text=True)
        for line in process.stdout:
            if re.search(r'password|credential|signing.key|private.key|token|secret|[│┃]|^\s*\|', line, re.I):
                line = '[redacted credential-related output]\n'
            transcript.write(line)
            output.write(line)
            output.flush()
            transcript.flush()
        code = process.wait()
        transcript.write(f'[exit {code}]\n')
    if code:
        raise RuntimeError(f'{name}: exit {code}; see {LOGS/(name+".log")}')
    print(name+': OK', flush=True)


def kube(role, *args):
    return ['kubectl', '--context', 'k3d-uds-'+role, *args]


def verify_release_assets():
    entries = (ASSETS/'SHA256SUMS').read_text().splitlines()
    verified = []
    for kind in ('uds-bundle', 'zarf-package'):
        for role in ('ship', 'shore'):
            name = f'{kind}-ghost-seeker-{role}-amd64-0.1.0-rc2.tar.zst'
            matches = [parts[0] for line in entries if len(parts := line.split()) == 2 and
                       parts[1].lstrip('*') == name]
            if len(matches) != 1 or not re.fullmatch(r'[0-9a-fA-F]{64}', matches[0]):
                raise ValueError('Missing, duplicate or invalid SHA256SUMS entry: '+name)
            digest = hashlib.sha256()
            with (ASSETS/name).open('rb') as asset:
                for chunk in iter(lambda: asset.read(1024*1024), b''):
                    digest.update(chunk)
            if digest.hexdigest() != matches[0].lower():
                raise ValueError('SHA256 mismatch: '+name)
            verified.append(name+': OK\n')
    (LOGS/'SHA256-verification.txt').write_text(''.join(verified))


def bootstrap():
    if int(INOTIFY_LIMIT.read_text()) < 1024:
        raise RuntimeError('Two clusters require fs.inotify.max_user_instances >= 1024 before bootstrap')
    verify_release_assets()
    for command in [['k3d', 'version'], ['uds', 'version'], ['zarf', 'version'], ['kubectl', 'version', '--client'], ['curl', '--version']]:
        run('version-'+command[0], command)
    run('airgap-off-before-delete', [ROOT/'scripts/uds/two-cluster-airgap.sh', 'off'])
    run('stop-owned-shaper', [ROOT/'scripts/uds/two-cluster-link.sh', 'stop'],
        {'STATE': str(Path.home()/'.local/share/uds-two-cluster')})
    run('clusters-before', ['k3d', 'cluster', 'list'])
    for role in ('ship', 'shore'):
        run('old-'+role+'-identity', kube(role, 'get', 'namespace', 'default', '-o', 'jsonpath={.metadata.uid}'))
    run('delete-owned-clusters', ['k3d', 'cluster', 'delete', 'uds-ship', 'uds-shore'])
    for role, subnet, gateway in [('shore', '172.18.0.0/16', '172.18.0.1'), ('ship', '172.19.0.0/16', '172.19.0.1')]:
        run('create-'+role+'-network', ['docker', 'network', 'create', '--driver', 'bridge', '--subnet', subnet,
                                      '--gateway', gateway, 'k3d-uds-'+role])
        run('core-'+role, ['uds', 'deploy', 'ghcr.io/defenseunicorns/packages/uds/bundles/k3d-core-slim-dev:1.16.0',
                          '--confirm', '--no-color', '--no-progress', '--no-log-file'],
            {'UDS_CONFIG': str(CONFIG/('core-'+role+'.yaml'))})
        run('new-'+role+'-identity', kube(role, 'get', 'namespace', 'default', '-o', 'jsonpath={.metadata.uid}'))
        run(role+'-nodes-storage', kube(role, 'get', 'nodes,storageclasses'))
        run(role+'-core-ready', kube(role, 'wait', '--for=condition=Ready', 'pods', '-A', '--field-selector=status.phase!=Succeeded', '--timeout=180s'))
        run(role+'-core-pods', kube(role, 'get', 'pods', '-A', '-o', 'wide'))
        run(role+'-images-before-app', ['docker', 'exec', 'k3d-uds-'+role+'-server-0', 'crictl', 'images'])


def deploy():
    run('runbook-auth', ['bash', ROOT/'scripts/uds/two-cluster-rc2-auth.sh'])
    for role in ('shore', 'ship'):
        run(role+'-context', ['kubectl', 'config', 'use-context', 'k3d-uds-'+role])
        run(role+'-rc2-deploy', ['uds', 'deploy', ASSETS/f'uds-bundle-ghost-seeker-{role}-amd64-0.1.0-rc2.tar.zst',
                               '--confirm', '--no-color', '--no-progress', '--no-log-file'],
            {'UDS_CONFIG': str(CONFIG/(role+'.yaml'))})
        namespace = 'ghost-seeker-'+role
        for kind, name in ([('statefulset', 'shore'), ('deployment', 'operator')] if role == 'shore' else
                           [('statefulset', 'edge'), ('deployment', 'edgeml-sidecar')]):
            run(role+'-'+name+'-ready', kube(role, '-n', namespace, 'rollout', 'status', kind+'/'+name, '--timeout=600s'))
        run(role+'-app-pods-pvcs', kube(role, '-n', namespace, 'get', 'pods,pvc'))
        run(role+'-package', kube(role, '-n', namespace, 'get', 'packages.uds.dev', 'ghost-seeker-'+role, '-o', 'yaml'))
    run('start-owned-shaper', [ROOT/'scripts/uds/two-cluster-link.sh', 'start'], {'LOG_DIR': str(LOGS)})
    run('ship-harness-host-alias', kube('ship', 'patch', 'statefulset', 'edge', '-n', 'ghost-seeker-ship', '--type=merge', '-p',
        '{"spec":{"template":{"spec":{"hostAliases":[{"ip":"172.19.0.1","hostnames":["shore.uds.dev"]}]}}}}'))
    run('ship-host-alias-ready', kube('ship', '-n', 'ghost-seeker-ship', 'rollout', 'status', 'statefulset/edge', '--timeout=300s'))
    run('shore-harness-access-log', kube('shore', 'apply', '-f', ROOT/'scripts/uds/gateway-access-log.yaml'))
    run('sync-before-evidence', ['python3', '-B', ROOT/'scripts/uds/two-cluster-status.py'])


def main():
    LOGS.mkdir(parents=True, exist_ok=True)
    phase = sys.argv[1]
    if phase == 'bootstrap':
        bootstrap()
    elif phase == 'deploy':
        deploy()
    elif phase == 'evidence':
        run('rc2-evidence', ['python3', '-B', ROOT/'scripts/uds/two-cluster-evidence.py'],
            {'EVIDENCE_DIR': str(REPORT/'evidence'), 'SSE_IDLE_SECONDS': '40'})
        run('shore-gateway-access', kube('shore', '-n', 'istio-tenant-gateway', 'logs',
            'deployment/tenant-ingressgateway', '-c', 'istio-proxy', '--since=20m'))
    elif phase == 'screenshots':
        run('rc2-screenshots', ['python3', '-B', ROOT/'scripts/uds/two-cluster-screenshots.py'],
            {'EVIDENCE_DIR': str(REPORT/'evidence'), 'SCREENSHOT_DIR': str(REPORT/'img')})
    elif phase == 'airgap':
        run('rc2-airgap', ['python3', '-B', ROOT/'scripts/uds/two-cluster-airgap-verify.py'],
            {'AIRGAP_RESTART_ONLY': '1', 'AIRGAP_EVIDENCE_DIR': str(REPORT/'airgap')})
    else:
        raise ValueError('unknown phase: '+phase)


if __name__ == '__main__':
    main()
