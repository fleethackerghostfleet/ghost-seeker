#!/usr/bin/env python3
"""Capture real-image ingress, authenticated gateway SSE, and shaped backlog recovery."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.parse
import zipfile


ROOT = Path(__file__).resolve().parents[2]
STATE = Path(os.environ.get('STATE', str(Path.home()/'.local/share/uds-two-cluster')))
REPORT = Path(os.environ.get('EVIDENCE_DIR', str(ROOT/'docs/reports/uds-validation/step3-priority')))
EDGE = 'http://127.0.0.1:18083'
SHORE = 'https://shore.uds.dev:9443'
SHORE_LOCAL = 'http://127.0.0.1:18084'
RPC = '/ghostseeker.v1.ConsoleService/'


def save(name, value):
    (REPORT/name).write_text(json.dumps(value, indent=2)+'\n')


def wait_for(check, seconds=90):
    deadline = time.monotonic()+seconds
    while time.monotonic() < deadline:
        value = check()
        if value:
            return value
        time.sleep(1)
    raise TimeoutError('evidence condition did not become true')


class Capture:
    def __init__(self):
        token = (STATE/'auth/operator-token').read_text().strip()
        self.config = 'header = "Authorization: Bearer '+token+'"\n'
        self.samples = []

    def command(self, url, authenticated=True):
        return ['curl', '-sS', '--noproxy', '*', '--http2', '--max-time', '120',
                '--resolve', 'shore.uds.dev:9443:127.0.0.1', '--config', '-', url], (
                    self.config if authenticated else '')

    def request(self, name, url, data=None, jpeg=None, authenticated=True, expected=200):
        command, config = self.command(url, authenticated)
        command += ['-o', str(REPORT/(name+'.json')), '-w', 'HTTP %{http_code} protocol=%{http_version}\n']
        if data is not None:
            command += ['-H', 'Content-Type: application/json', '--data-binary', json.dumps(data)]
        if jpeg is not None:
            command += ['-H', 'Content-Type: image/jpeg', '--data-binary', '@'+str(jpeg)]
        result = subprocess.run(command, input=config, text=True, capture_output=True, check=True)
        (REPORT/(name+'-http.txt')).write_text(result.stdout)
        if not result.stdout.startswith('HTTP '+str(expected)+' '):
            raise RuntimeError(name+': '+result.stdout.strip())
        return json.loads((REPORT/(name+'.json')).read_text())

    def snapshot(self, phase):
        edge = self.request(phase+'-edge-status', EDGE+RPC+'GetStatus', {})
        shore = self.request(phase+'-shore-status', SHORE_LOCAL+RPC+'GetStatus', {})
        queue = self.request(phase+'-queue', EDGE+RPC+'GetQueue', {'limit': 500})
        items = self.request(phase+'-edge-items', EDGE+RPC+'ListItems', {'limit': 500})
        page = self.request(phase+'-shore-items', SHORE+'/api/v1/items?limit=500')
        sample = {'phase': phase, 'at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                  'edge': edge, 'shore': shore, 'queue': queue,
                  'shore_receipts': len(page.get('items', []))}
        self.samples.append(sample)
        save('counter-timeline.json', self.samples)
        return edge, shore, queue, items, page

    def rate(self, bps):
        subprocess.run([str(ROOT/'scripts/uds/two-cluster-link.sh'), 'set-rate', str(bps)],
                       check=True, stdout=subprocess.DEVNULL)


def inputs():
    directory = STATE/'evidence-inputs'
    directory.mkdir(exist_ok=True)
    offset = int(os.environ.get('INPUT_OFFSET', '0'))
    selection = [('SMD_VIS_Onboard', 22), ('SMD_VIS_Onboard', 1),
                 ('SMD_VIS_Onboard', 24), ('SMD_VIS_Onboard', 11),
                 ('SMD_VIS_Onboard', 41), ('SMD_VIS_Onboard', 61),
                 ('30_Days_at_Sea_Timelapse', 201)]
    records = []
    for i, (archive, index) in enumerate(selection):
        path = ROOT/'frames'/f'{archive}_frames.zip'
        with zipfile.ZipFile(path) as source:
            names = sorted(n for n in source.namelist() if n.lower().endswith('.jpg'))
            member = names[index+offset]
            data = source.read(member)
        target = directory/f'capture-{i}.jpg'
        target.write_bytes(data)
        records.append({'archive': str(path.relative_to(ROOT)), 'member': member,
                        'sha256': hashlib.sha256(data).hexdigest(), 'bytes': len(data),
                        'local_path': str(target)})
    save('inputs.json', records)
    return records


def arrivals(text, ids):
    seen = set()
    result = []
    for line in text.splitlines():
        if not line.startswith('data: '):
            continue
        event = json.loads(line[6:])
        item = event['item']
        if item['id'] not in ids or item['id'] in seen:
            continue
        seen.add(item['id'])
        result.append({'id': item['id'], 'cursor': event['cursor'], 'event': event['event'],
                       'tier': item['tier'], 'score': item['score'], 'source': item['source'],
                       'contacts': item['contacts']})
    return result


def main():
    REPORT.mkdir(parents=True, exist_ok=False)
    run = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    capture = Capture()
    records = inputs()
    forwards = []
    stream = None
    stream_file = None
    try:
        for role in ('shore', 'ship'):
            result = subprocess.run(['kubectl', '--context', 'k3d-uds-'+role, 'get', 'pods', '-A', '-o', 'wide'],
                                    capture_output=True, text=True, check=True)
            (REPORT/(role+'-pods.txt')).write_text(result.stdout)
        for role, service, ports in [('ship', 'edge', '18083:8081'), ('shore', 'shore', '18084:8080')]:
            forward = subprocess.Popen(['kubectl', '--context', 'k3d-uds-'+role, '-n', 'ghost-seeker-'+role,
                                        'port-forward', 'service/'+service, ports, '--address', '127.0.0.1'],
                                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            forwards.append(forward)
            for line in forward.stdout:
                if 'Forwarding from' in line:
                    break
            else:
                raise RuntimeError(role+' port-forward failed')
        capture.rate(16000000)
        capture.request('unauthenticated', SHORE+'/api/v1/items', authenticated=False, expected=401)
        capture.request('authenticated', SHORE+'/api/v1/items')
        health = capture.request('stream-start-health', SHORE+'/api/v1/health')
        command, config = capture.command(SHORE+'/api/v1/stream?since='+health['stream_cursor'])
        command[command.index('--max-time')+1] = '420'
        command += ['-N']
        stream_file = (REPORT/'shore-stream.sse').open('w')
        stream = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=stream_file,
                                  stderr=(REPORT/'stream-stderr.txt').open('w'), text=True)
        stream.stdin.write(config)
        stream.stdin.close()
        wait_for(lambda: ': connected' in (REPORT/'shore-stream.sse').read_text(), 15)
        accepted = []

        def post(index, phase):
            source = f'uds-{run}-{phase}-{index}'
            result = capture.request(f'accepted-{phase}-{index}', EDGE+'/api/v1/ingest/frames?'+
                                     urllib.parse.urlencode({'source': source}),
                                     jpeg=records[index]['local_path'], expected=202)
            if result.get('duplicate'):
                raise RuntimeError('input already ingested; use fresh image indices with INPUT_OFFSET or fresh application data')
            accepted.append({'source': source, 'result': result, 'input': index})
            save('accepted.json', accepted)
            return result

        first = post(0, 'live')

        def first_arrived():
            page = capture.request('live-items', SHORE+'/api/v1/items?limit=500')
            return any(i['id'] == first['id'] and i['contacts'] for i in page.get('items', []))

        wait_for(first_arrived)
        before = capture.snapshot('before')
        capture.rate(0)
        for index in range(1, len(records)):
            post(index, 'outage')

        def failed():
            status = capture.request('outage-poll', EDGE+RPC+'GetStatus', {})
            return int(status.get('link', {}).get('consecutiveFailures', 0)) >= 2

        wait_for(failed, 90)
        during = capture.snapshot('during')
        if int(during[1].get('itemsTotal', 0)) != int(before[1].get('itemsTotal', 0)):
            raise RuntimeError('shore item count changed during outage')
        capture.rate(64000)
        time.sleep(15)
        capture.snapshot('low-rate')
        capture.rate(16000000)

        def drained():
            status = capture.request('drain-poll', EDGE+RPC+'GetStatus', {})
            heartbeat = status.get('heartbeat', {})
            return (int(heartbeat.get('queuedItems', 0)) == 0 and
                    int(heartbeat.get('unmanifestedItems', 0)) == 0 and
                    status.get('link', {}).get('up', False))

        wait_for(drained, 180)
        after = capture.snapshot('after')
        batch_ids = {entry['result']['id'] for entry in accepted[1:]}
        ordered = wait_for(lambda: rows if len(rows := arrivals((REPORT/'shore-stream.sse').read_text(), batch_ids))
                           == len(batch_ids) else None, 15)
        save('backlog-arrival-order.json', ordered)
        if ordered[0]['tier'] != 'P1':
            raise RuntimeError('first backlog arrival was not P1; inspect relative ranks without altering evidence')
        if any(a['score'] < b['score'] for a, b in zip(ordered, ordered[1:])):
            raise RuntimeError('backlog first-arrival scores were not descending')
        save('result.json', {'accepted_frames': len(accepted), 'outage_frames': len(batch_ids),
                            'shore_before': int(before[1].get('itemsTotal', 0)),
                            'shore_during': int(during[1].get('itemsTotal', 0)),
                            'shore_after': int(after[1].get('itemsTotal', 0)),
                            'first_backlog_tier': ordered[0]['tier'], 'priority_order_verified': True})
        print('Evidence capture completed: '+str(REPORT))
    finally:
        capture.rate(16000000)
        if stream is not None:
            stream.terminate()
            stream.wait(timeout=10)
        if stream_file is not None:
            stream_file.close()
        for process in forwards:
            process.terminate()
            process.wait(timeout=10)


if __name__ == '__main__':
    main()
