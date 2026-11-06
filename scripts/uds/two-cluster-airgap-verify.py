#!/usr/bin/env python3
"""Destructive ship-app-only offline redeployment check; always restore host networking."""
import importlib.util
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time
import zipfile


ROOT = Path(__file__).resolve().parents[2]
STATE = Path(os.environ.get('STATE', str(Path.home()/'.local/share/uds-two-cluster')))
REPORT = Path(os.environ.get('AIRGAP_EVIDENCE_DIR', str(ROOT/'docs/reports/uds-validation/airgap')))
FIREWALL = ROOT/'scripts/uds/two-cluster-airgap.sh'
KUBE = ['kubectl', '--context', 'k3d-uds-ship']
spec = importlib.util.spec_from_file_location('capture', ROOT/'scripts/uds/two-cluster-evidence.py')
capture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(capture)
capture.REPORT = REPORT


def run(name, command, timeout=240, expected=0, env=None):
    with (REPORT/(name+'.log')).open('w') as output:
        result = subprocess.run([str(arg) for arg in command], stdout=output, stderr=subprocess.STDOUT,
                                timeout=timeout, env=env)
    accepted_codes = expected if isinstance(expected, tuple) else (expected,)
    if result.returncode not in accepted_codes:
        raise RuntimeError(f'{name}: exit {result.returncode}; see {REPORT/(name+".log")}')
    print(name+': OK', flush=True)


def pods():
    return json.loads(subprocess.check_output(KUBE+['get', 'pods', '-n', 'ghost-seeker-ship', '-o', 'json']))


def forwards():
    result = []
    for role, service, ports in [('ship', 'edge', '18083:8081'), ('shore', 'shore', '18084:8080')]:
        process = subprocess.Popen(['kubectl', '--context', 'k3d-uds-'+role, '-n', 'ghost-seeker-'+role,
                                    'port-forward', 'service/'+service, ports, '--address', '127.0.0.1'],
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        result.append(process)
        for line in process.stdout:
            if 'Forwarding from' in line:
                break
        else:
            raise RuntimeError(role+' port-forward failed')
    return result


def close_forwards(processes):
    for process in processes:
        process.terminate()
        process.wait(timeout=10)
    processes.clear()


def ready(phase):
    run(phase+'-edge-ready', KUBE+['rollout', 'status', 'statefulset/edge', '-n', 'ghost-seeker-ship', '--timeout=180s'])
    run(phase+'-inference-ready', KUBE+['rollout', 'status', 'deployment/edgeml-sidecar', '-n', 'ghost-seeker-ship', '--timeout=180s'])
    run(phase+'-pods', KUBE+['get', 'pods', '-A', '-o', 'wide'])
    run(phase+'-events', KUBE+['get', 'events', '-n', 'ghost-seeker-ship', '--sort-by=.metadata.creationTimestamp'])
    data = pods()
    capture.save(phase+'-pod-images.json', [
        {'name': p['metadata']['name'], 'uid': p['metadata']['uid'],
         'labels': p['metadata']['labels'],
         'containers': [{'name': c['name'], 'image': c['image']} for c in
                        p['spec']['containers']+p['spec'].get('initContainers', [])],
         'statuses': p['status'].get('containerStatuses', [])}
        for p in data['items']])
    inference = next(p['metadata']['name'] for p in data['items'] if p['metadata']['labels'].get('app') == 'edgeml-sidecar')
    run(phase+'-model-checks', KUBE+['logs', '-n', 'ghost-seeker-ship', inference, '-c', 'models'])
    run(phase+'-edgeml', KUBE+['logs', '-n', 'ghost-seeker-ship', inference, '-c', 'edgeml-sidecar'])


def control_logs():
    credentials = [(STATE/'auth'/name).read_text().strip() for name in
                   ('signing-key', 'operator-token', 'unit-token', 'edge-operator-token') if (STATE/'auth'/name).exists()]
    for namespace in ('pepr-system', 'zarf'):
        data = json.loads(subprocess.check_output(KUBE+['get', 'pods', '-n', namespace, '-o', 'json']))
        for pod in data['items']:
            name = pod['metadata']['name']
            result = subprocess.run(KUBE+['logs', '-n', namespace, name, '--all-containers=true', '--since=15m', '--tail=500'],
                                    text=True, capture_output=True, timeout=60, check=True)
            output = result.stdout+result.stderr
            for credential in credentials:
                output = output.replace(credential, '[REDACTED]')
            (REPORT/(namespace+'-'+name+'.log')).write_text(output)


def ingest(client, phase, index, wait=True):
    index += int(os.environ.get('AIRGAP_FRAME_OFFSET', '0'))
    with zipfile.ZipFile(ROOT/'frames/SMD_VIS_Onboard_frames.zip') as source:
        names = sorted(name for name in source.namelist() if name.lower().endswith('.jpg'))
        data = source.read(names[index])
    capture.save(phase+'-input.json', {'archive': 'frames/SMD_VIS_Onboard_frames.zip', 'member': names[index],
                                     'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest()})
    image = STATE/'evidence-inputs'/f'airgap-{index}.jpg'
    image.write_bytes(data)
    result = client.request(phase+'-accepted', capture.EDGE+'/api/v1/ingest/frames?source=airgap-'+phase,
                            jpeg=image, expected=202)
    if result.get('duplicate'):
        raise RuntimeError('offline input was already ingested')
    if not wait:
        return result
    def arrived():
        page = client.request(phase+'-shore-items', capture.SHORE+'/api/v1/items?limit=500')
        return next((item for item in page.get('items', []) if item['id'] == result['id'] and item['contacts']), None)
    item = capture.wait_for(arrived, 180)
    capture.save(phase+'-received-item.json', item)
    client.snapshot(phase)


def main():
    restart_only = os.environ.get('AIRGAP_RESTART_ONLY') == '1'
    if not restart_only and os.environ.get('AIRGAP_REDEPLOY') != '1':
        raise SystemExit('Set AIRGAP_REDEPLOY=1 for app-package removal/redeployment, or AIRGAP_RESTART_ONLY=1')
    REPORT.mkdir(parents=True, exist_ok=True)
    if not restart_only:
        tag = (STATE/'current-tag').read_text().strip()
        stage = STATE/'build'/tag
        package = stage/'dist/zarf-package-ghost-seeker-ship-amd64-0.1.0.tar.zst'
        bundle = stage/'dist/uds-bundle-ghost-seeker-ship-amd64-0.1.0.tar.zst'
        assert package.is_file() and bundle.is_file()
    client = capture.Capture()
    processes = []
    pid = subprocess.check_output(['docker', 'inspect', '-f', '{{.State.Pid}}', 'k3d-uds-ship-server-0'], text=True).strip()
    node_curl = ['sudo', '-n', 'nsenter', '-t', pid, '-n', 'curl', '--noproxy', '*', '--max-time', '4', '-sS', '-o', '/dev/null']
    try:
        run('firewall-on', [FIREWALL, 'on'])
        run('firewall-on-again', [FIREWALL, 'on'])
        run('blocked-ghcr', node_curl+['--resolve', 'ghcr.io:443:20.29.134.18', 'https://ghcr.io/v2/'], expected=28)
        run('blocked-cloudflare', node_curl+['https://1.1.1.1/'], expected=28)
        run('blocked-dns', ['timeout', '6', 'docker', 'exec', 'k3d-uds-ship-server-0', 'nslookup', 'ghcr.io'], expected=(1, 124))
        run('allowed-shore-tls', node_curl+['--http2', '--resolve', 'shore.uds.dev:443:172.19.0.1',
                                         '-w', 'HTTP %{http_code} protocol=%{http_version} verify=%{ssl_verify_result}\n',
                                         'https://shore.uds.dev/api/v1/health'])
        run('firewall-block-counters', [FIREWALL, 'status'])
        if os.environ.get('AIRGAP_RESUME') == 'redeploy':
            previous = json.loads((REPORT/'restarted-pod-images.json').read_text())
            app_images = sorted({c['image'] for p in previous for c in p['containers'] if '/ghost-seeker/' in c['image']})
        else:
            processes = forwards()
            client.snapshot('blocked-before-restart')
            close_forwards(processes)
            old_pods = pods()
            app_images = sorted({c['image'] for p in old_pods['items'] for c in
                                 p['spec']['containers']+p['spec'].get('initContainers', [])
                                 if '/ghost-seeker/' in c['image']})
            run('delete-app-pods', KUBE+['delete', 'pods', '-n', 'ghost-seeker-ship', '-l', 'zarf.dev/package=ghost-seeker-ship'])
            ready('restarted')
            processes = forwards()
            ingest(client, 'restarted-offline', 23)
            close_forwards(processes)
            if restart_only:
                control_logs()
                run('firewall-final-block-counters', [FIREWALL, 'status'])
                capture.save('result.json', {'offline_restart': True, 'live_ingest_scored_and_synchronized': True,
                                            'app_removal_redeployment': 'not requested; release-only restart check'})
                return
            run('ship-context', ['kubectl', 'config', 'use-context', 'k3d-uds-ship'])
            run('remove-ship-app', ['zarf', 'package', 'remove', package, '--confirm', '--no-color'], timeout=420)
        workloads = json.loads(subprocess.check_output(KUBE+['get', 'statefulset,deployment', '-n', 'ghost-seeker-ship', '-o', 'json']))
        assert not workloads['items'], 'ship app workloads must be absent before fresh package deployment'
        run('retained-pvcs', KUBE+['get', 'pvc', '-n', 'ghost-seeker-ship'])
        for index, image in enumerate(app_images):
            run('evict-app-image-'+str(index), ['docker', 'exec', 'k3d-uds-ship-server-0', 'crictl', 'rmi', image])
        run('images-before-redeploy', ['docker', 'exec', 'k3d-uds-ship-server-0', 'crictl', 'images'])
        environment = dict(os.environ, ROOT=str(ROOT), STATE=str(STATE), SHORE_CONTEXT='k3d-uds-shore',
                           SHIP_CONTEXT='k3d-uds-ship', SHORE_NAMESPACE='ghost-seeker-shore', SHIP_NAMESPACE='ghost-seeker-ship')
        run('restore-preexisting-auth', ['python3', '-B', ROOT/'scripts/uds/two-cluster-auth.py'], env=environment)
        environment['UDS_CONFIG'] = str(stage/'bundles/prod-uds/ship/uds-config.yaml')
        run('redeploy-ship-app', ['uds', 'deploy', bundle, '--confirm', '--no-color', '--no-progress', '--no-log-file',
                                '--retries', '1', '--set', 'ghost-seeker-ship.NAMESPACE=ghost-seeker-ship',
                                '--set', 'ghost-seeker-ship.FRAMES_SOURCE=pvc',
                                '--set', 'ghost-seeker-ship.SHORE_URL=https://shore.uds.dev:443',
                                '--set', 'ghost-seeker-ship.SHORE_EGRESS_CIDR=172.19.0.1/32',
                                '--set', 'ghost-seeker-ship.SHORE_EGRESS_PORT=443'], timeout=600, env=environment)
        run('restore-host-alias', KUBE+['patch', 'statefulset', 'edge', '-n', 'ghost-seeker-ship', '--type=merge', '-p',
                                      json.dumps({'spec': {'template': {'spec': {'hostAliases': [
                                          {'ip': '172.19.0.1', 'hostnames': ['shore.uds.dev']}]}}}})])
        ready('redeployed')
        run('images-after-redeploy', ['docker', 'exec', 'k3d-uds-ship-server-0', 'crictl', 'images'])
        processes = forwards()
        ingest(client, 'redeployed-offline', 25)
        client.rate(0)
        baseline = client.request('low-before-status', capture.EDGE+capture.RPC+'GetStatus', {})
        low_item = ingest(client, 'low-rate', 26, wait=False)
        client.snapshot('low-rate-queued')
        client.rate(64000)
        baseline_time = int(baseline['link']['lastSuccessUnixMs'])
        started = time.monotonic()
        samples = []
        for index in range(12):
            time.sleep(15)
            status = client.request('low-status-'+str(index), capture.EDGE+capture.RPC+'GetStatus', {})
            samples.append(status)
        low_exchanges = int(samples[-1]['link']['lastSuccessUnixMs']) > baseline_time
        capture.save('sustained-low-rate.json', {'bps': 64000, 'elapsed_seconds': time.monotonic()-started,
                                              'baseline_last_success': baseline_time, 'item_id': low_item['id'],
                                              'payload_drained': samples[-1]['itemsComplete'] == samples[-1]['itemsTotal'],
                                              'samples': samples})
        client.snapshot('low-rate-final')
        client.rate(16000000)
        control_logs()
        run('firewall-final-block-counters', [FIREWALL, 'status'])
        capture.save('result.json', {'offline_restart': True, 'offline_redeployment': True,
                                    'uncached_app_image_references': app_images,
                                    'live_ingest_scored_and_synchronized_twice': True, 'sustained_64kbps_exchanges': low_exchanges})
    finally:
        try:
            close_forwards(processes)
            client.rate(16000000)
        finally:
            run('firewall-off', [FIREWALL, 'off'])
            run('firewall-off-again', [FIREWALL, 'off'])
            run('firewall-final-status', [FIREWALL, 'status'])


if __name__ == '__main__':
    main()
