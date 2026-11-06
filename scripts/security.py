#!/usr/bin/env python3
"""Small, fail-closed report policies shared by local and hosted gates."""
import csv
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
REPORTS = ROOT / "dist/security"


def fingerprint(issue):
    path = Path(issue["file"]).resolve().relative_to(ROOT).as_posix()
    code = "\n".join(re.sub(r"^\d+:\s?", "", line) for line in issue["code"].splitlines())
    return f'{path}:{issue["rule_id"]}:{hashlib.sha256(code.encode()).hexdigest()}'


def new_highs(report, baseline):
    if report.get("Golang errors"):
        raise ValueError(f'Go analysis errors: {report["Golang errors"]}')
    remaining = Counter(baseline)
    findings = []
    for issue in report["Issues"] or []:
        if issue["severity"] != "HIGH":
            continue
        key = fingerprint(issue)
        if remaining[key]:
            remaining[key] -= 1
        else:
            findings.append(issue)
    return findings


def check_licenses(go_rows, python_rows):
    denied = []
    for name, license_name in go_rows + [(r["Name"], r["License"]) for r in python_rows]:
        # LGPL is not GPL; dual-license expressions containing GPL require review.
        if re.search(r"(?<![A-Za-z])(?:AGPL|GPL)(?:[- .0-9v]|$)|Affero|(?<!Lesser )(?<!Library )General Public License", license_name, re.I):
            denied.append((name, license_name))
        if license_name.strip().upper() in ("", "UNKNOWN", "UNDEFINED"):
            denied.append((name, license_name))
    return denied


def main():
    REPORTS.mkdir(parents=True, exist_ok=True)
    if sys.argv[1] == "gosec":
        for profile, tags in (("demo", []), ("prod", ["-tags=prod"])):
            output = REPORTS / f"gosec-{profile}.json"
            subprocess.run(["gosec", "-no-fail", "-severity", "high", "-fmt", "json",
                            "-out", str(output), *tags, "./..."], cwd=ROOT, check=True)
            report = json.loads(output.read_text())
            baseline = json.loads((ROOT / "docs/security/gosec-baseline.json").read_text())[profile]
            findings = new_highs(report, baseline)
            print(f"gosec {profile}: {len(report['Issues'] or [])} existing/current HIGH; {len(findings)} new HIGH", flush=True)
            if findings:
                print(json.dumps(findings, indent=2))
                return 1
    elif sys.argv[1] == "licenses":
        with (REPORTS / "go-licenses.csv").open("w") as output:
            subprocess.run(["go-licenses", "report", "--ignore", "github.com/fleethackerghostfleet/ghost-seeker",
                            "./cmd/..."], stdout=output, check=True,
                           env={**os.environ, "GOFLAGS": "-tags=prod"})
        with (REPORTS / "python-licenses.json").open("w") as output:
            subprocess.run([os.environ.get("PIP_LICENSES", ".venv/security/bin/pip-licenses"),
                            "--python", os.environ.get("SHIPPED_PYTHON", ".venv/shipped/bin/python"),
                            "--format=json", "--ignore-packages", "edgeml"], stdout=output, check=True)
        with (REPORTS / "go-licenses.csv").open() as source:
            go_rows = [(r[0], r[2]) for r in csv.reader(source)]
        python_rows = json.loads((REPORTS / "python-licenses.json").read_text())
        if not go_rows or not python_rows:
            raise ValueError("Empty license inventory")
        denied = check_licenses(go_rows, python_rows)
        print(f"licenses: {len(go_rows)} Go and {len(python_rows)} Python dependencies; {len(denied)} denied/unknown")
        for name, license_name in denied:
            print(f"DENIED {name}: {license_name}")
        return bool(denied)
    else:
        raise ValueError("Expected gosec or licenses")
    return 0


if __name__ == "__main__":
    sys.exit(main())
