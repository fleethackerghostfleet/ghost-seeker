#!/usr/bin/env python3
"""Route tasks-dev.yaml's local auth generator to the split cluster namespaces."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess


def main():
    os.umask(0o077)
    root = Path(os.environ['ROOT']).resolve()
    directory = Path(os.environ['STATE']).resolve() / 'auth'
    if directory.is_relative_to(root):
        raise ValueError('authentication files must be outside the repository')
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    spec = importlib.util.spec_from_file_location('dev_auth', root/'deploy/zarf/dev_auth.py')
    auth = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(auth)

    def apply(document):
        roles = ('SHORE', 'SHIP') if document['kind'] == 'Namespace' else (
            ('SHIP',) if document['metadata']['name'] == 'edge-auth' else ('SHORE',))
        for role in roles:
            namespace = os.environ[role + '_NAMESPACE']
            document['metadata']['namespace' if document['kind'] == 'Secret' else 'name'] = namespace
            subprocess.run(['kubectl', '--context', os.environ[role + '_CONTEXT'], 'apply', '-f', '-'],
                           input=json.dumps(document), text=True, check=True, stdout=subprocess.DEVNULL)
        for name, value in document.get('stringData', {}).items():
            path = directory/name
            path.write_text(value)
            path.chmod(0o600)

    paths = [directory/name for name in ('signing-key', 'operator-token', 'unit-token')]
    if all(path.exists() for path in paths):
        signing, operator, unit = (path.read_text() for path in paths)
        apply({'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': 'ghost-seeker'}})
        for name, values in {
            'shore-auth': {'signing-key': signing, 'operator-token': operator},
            'operator-auth': {'operator-token': operator},
            'edge-auth': {'unit-token': unit, 'operator-token': operator},
        }.items():
            apply({'apiVersion': 'v1', 'kind': 'Secret', 'type': 'Opaque',
                   'metadata': {'name': name}, 'stringData': values})
    elif any(path.exists() for path in paths):
        raise ValueError('incomplete auth directory; restore all three credential files before deploying')
    else:
        auth.apply = apply
        auth.main()


if __name__ == '__main__':
    main()
