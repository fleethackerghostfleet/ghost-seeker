#!/usr/bin/env python3
"""Read authenticated Console status without putting credentials on argv or stdout."""
import http.client
import json
import os
from pathlib import Path
import subprocess


def main():
    directory = Path(os.environ.get('STATE', str(Path.home()/'.local/share/uds-two-cluster')))
    token = (directory/'auth/operator-token').read_text()
    for role, port in (('ship', 18081), ('shore', 18082)):
        edge_path = directory/'auth/edge-operator-token'
        role_token = edge_path.read_text() if role == 'ship' and edge_path.exists() else token
        context = os.environ.get(role.upper()+'_CONTEXT', 'k3d-uds-'+role)
        namespace = os.environ.get(role.upper()+'_NAMESPACE', 'ghost-seeker-'+role)
        resource, target = ('statefulset/edge', 8081) if role == 'ship' else ('statefulset/shore', 8080)
        process = subprocess.Popen(['kubectl', '--context', context, '-n', namespace,
                                    'port-forward', resource, f'{port}:{target}', '--address', '127.0.0.1'],
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        try:
            for line in process.stdout:
                if 'Forwarding from' in line:
                    break
            else:
                raise RuntimeError('port-forward failed for '+role)
            connection = http.client.HTTPConnection('127.0.0.1', port, timeout=15)
            connection.request('POST', '/ghostseeker.v1.ConsoleService/GetStatus', '{}',
                               {'Content-Type': 'application/json', 'Authorization': 'Bearer '+role_token})
            response = connection.getresponse()
            body = json.loads(response.read())
            if response.status != 200:
                raise RuntimeError(f'{role} status: HTTP {response.status}')
            if role == 'ship':
                body.setdefault('link', {}).setdefault('consecutiveFailures', '0')
            print(json.dumps({'role': role, 'status': body}, sort_keys=True))
            connection.close()
        finally:
            process.terminate()
            process.wait(timeout=10)


if __name__ == '__main__':
    main()
