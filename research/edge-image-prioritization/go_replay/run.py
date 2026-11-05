"""Replay private fixture bytes against an isolated snapshot of a local Git ref.

Requires an existing compatible Go executable. Does not change the source
checkout, install a toolchain, or validate HTTP authentication/compression.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--repo', required=True, type=Path)
    p.add_argument('--revision', required=True)
    p.add_argument('--fixtures', required=True, type=Path)
    p.add_argument('--out', required=True, type=Path)
    p.add_argument('--go', default='go', help='Existing Go executable; the study used Go 1.23.4')
    p.add_argument('--cache-root', type=Path, help='Optional reusable local Go cache directory')
    args = p.parse_args()
    repo, out, fixtures = args.repo.resolve(), args.out.resolve(), args.fixtures.resolve()
    if not (fixtures/'fixtures.json').is_file():
        p.error('fixtures directory must contain fixtures.json and its referenced JPEGs')
    if out.exists():
        p.error('--out must be a new directory')
    revision = subprocess.check_output(['git', 'rev-parse', args.revision+'^{commit}'], cwd=repo, text=True).strip()
    names = subprocess.check_output(['git', 'ls-tree', '-r', '--name-only', revision], cwd=repo, text=True).splitlines()
    chosen = [name for name in names if name in {'go.mod','go.sum'} or (
        name.startswith(('edge/','shore/','internal/','gen/')) and
        Path(name).suffix in {'.go','.json','.yaml','.yml','.toml','.txt','.csv'})]
    snapshot = out/'snapshot'
    snapshot.mkdir(parents=True)
    manifest = {}
    for name in chosen:
        raw = subprocess.check_output(['git', 'show', revision+':'+name], cwd=repo)
        dest = snapshot/name
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_bytes(raw)
        manifest[name] = hashlib.sha256(raw).hexdigest()
    for path in (Path(__file__).resolve().parent/'harness').glob('*.go'):
        shutil.copy2(path, snapshot/'edge/scheduler'/path.name)
    cache = args.cache_root.resolve() if args.cache_root else out/'go-cache'
    env = os.environ.copy()
    env.update(GOCACHE=str(cache/'build'), GOMODCACHE=str(cache/'modules'), GOPATH=str(cache/'gopath'),
               GOTOOLCHAIN='local', GOENV='off', REPLAY_REVISION=revision, REPLAY_FIXTURES=str(fixtures), REPLAY_OUT=str(out/'results'))
    command = [args.go, 'test', './edge/link', './edge/scheduler', './shore/sync', './shore/store', '-count=1', '-json']
    result = subprocess.run(command, cwd=snapshot, env=env, text=True, capture_output=True)
    (out/'go-test.jsonl').write_text(result.stdout)
    (out/'go-test.stderr.txt').write_text(result.stderr)
    # No personal absolute paths are needed in the portable provenance record.
    record = {'revision':revision, 'source_files_sha256':manifest, 'returncode':result.returncode,
              'command':['go', *command[1:]], 'scope':'in-process service and virtual link; private fixture data supplied externally'}
    (out/'run.json').write_text(json.dumps(record,indent=2)+'\n')
    for name, digest in manifest.items():
        assert hashlib.sha256((snapshot/name).read_bytes()).hexdigest() == digest
    print(json.dumps({'revision':revision,'returncode':result.returncode,'source_files':len(manifest)},indent=2))
    raise SystemExit(result.returncode)


if __name__ == '__main__':
    main()
