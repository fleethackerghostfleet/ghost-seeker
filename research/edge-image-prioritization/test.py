"""Run the portable research checks without private images, models or cloud I/O."""
import os
from pathlib import Path
import subprocess
import sys

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
env = os.environ.copy()
env['GHOST_BASELINE_PATH'] = str(REPO/'algorithm')
env['GHOST_CANDIDATE_MODULE'] = str(HERE/'phase1/candidates/candidate_methods.py')
for suite in ['phase1/candidates', 'phase1/evaluation', 'phase2/representation', 'phase2/transport', 'phase2']:
    command = [sys.executable, '-m', 'unittest', 'discover', '-s', str(HERE/suite), '-p', 'test_*.py', '-v']
    result = subprocess.run(command, cwd=REPO, env=env)
    if result.returncode:
        raise SystemExit(result.returncode)
