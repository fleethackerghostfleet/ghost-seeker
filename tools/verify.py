#!/usr/bin/env python3
"""Run the complete verification suite without a CI service."""

import datetime
import json
import os
from pathlib import Path
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]


def git(*args):
    return subprocess.check_output(["git", *args], cwd=ROOT, text=True).strip()


def check_generated():
    subprocess.run(["git", "diff", "--exit-code", "HEAD", "--", "gen/"], cwd=ROOT, check=True)
    if git("ls-files", "--others", "--exclude-standard", "--", "gen/"):
        raise RuntimeError("Untracked generated files; regenerate and commit gen/ first")


def checks():
    return [
        ("verifier-tests", [sys.executable, "-m", "unittest", "discover", "-s", "tools", "-p", "test_verify.py"]),
        ("repository-layout", [sys.executable, "-m", "unittest", "discover", "-s", "tools", "-p", "test_layout.py"]),
        ("go-vet", ["go", "vet", "./..."]),
        ("go-tests", ["go", "test", "./..."]),
        ("go-race", ["go", "test", "-race", "./..."]),
        ("go-build", ["make", "build-all"]),
        ("go-modules", ["go", "mod", "verify"]),
        ("go-vulnerabilities", ["go", "run", "golang.org/x/vuln/cmd/govulncheck@v1.9.0", "./..."]),
        ("web", ["node", "--test", *[str(p.relative_to(ROOT)) for p in sorted((ROOT / "web").glob("*.test.cjs"))]]),
        ("edgeml", [sys.executable, "-m", "pytest", "-q", "edgeml/tests"]),
        ("research", [sys.executable, "-m", "pytest", "-q", "research/edgeml/tests"]),
        ("algorithm", [sys.executable, "-m", "unittest", "discover", "-s", "algorithm/tests"]),
        ("proto-lint", ["buf", "lint"]),
        ("proto-generate", ["buf", "generate"]),
        ("proto-current", [sys.executable, "tools/verify.py", "--check-generated"]),
        ("package", ["make", "package-check"]),
        ("images", ["make", "images"]),
    ]


def run_check(name, command, output, timeout=3600):
    started = time.monotonic()
    log = output / f"{name}.log"
    print(f"Running {name}: {shlex.join(command)}", flush=True)
    with log.open("w") as stream:
        try:
            process = subprocess.Popen(command, cwd=ROOT, stdout=stream, stderr=subprocess.STDOUT,
                                       env={**os.environ, "STRICT_CHECKS": "1", "PYTHON": sys.executable,
                                            "PYTHONPATH": str(ROOT / "algorithm") + os.pathsep + os.environ.get("PYTHONPATH", "")},
                                       start_new_session=True)
            try:
                code = process.wait(timeout=timeout)
            except (subprocess.TimeoutExpired, KeyboardInterrupt):
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                process.wait()
                raise
        except (OSError, subprocess.TimeoutExpired) as exc:
            stream.write(f"\n{exc}\n")
            code = 1
    print(f"{'PASS' if code == 0 else 'FAIL'} {name}: {log}", flush=True)
    return {"name": name, "command": command, "exit_code": code,
            "status": "passed" if code == 0 else "failed", "log": log.name,
            "seconds": round(time.monotonic() - started, 2)}


def verify():
    commit = git("rev-parse", "HEAD")
    parent = ROOT / "dist" / "verify"
    parent.mkdir(parents=True, exist_ok=True)
    output = Path(tempfile.mkdtemp(prefix=f"{commit[:12]}-", dir=parent))
    report = {"commit": commit, "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "status": "failed", "checks": [], "error": None}
    try:
        if git("status", "--porcelain", "--untracked-files=all"):
            raise RuntimeError("Commit or stash local changes before verification; reports must test a clean commit")
        required = ["go", "node", "make", "buf", "protoc-gen-go", "protoc-gen-connect-go",
                    "kubeconform", "zarf", "docker"]
        missing = [tool for tool in required if not shutil.which(tool)]
        if missing:
            raise RuntimeError("Missing prerequisites: " + ", ".join(missing) + ". See docs/VERIFY.md")
        for name, command in checks():
            report["checks"].append(run_check(name, command, output))
        if git("rev-parse", "HEAD") != commit or git("status", "--porcelain", "--untracked-files=all"):
            raise RuntimeError("Source changed during verification; inspect changes and rerun on a clean commit")
        if all(check["status"] == "passed" for check in report["checks"]):
            report["status"] = "passed"
    except (OSError, RuntimeError, subprocess.SubprocessError, KeyboardInterrupt) as exc:
        report["error"] = str(exc) or "Interrupted"
    report["finished_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    (output / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    print(f"Verification {report['status']}: {output / 'report.json'}", flush=True)
    if report["error"]:
        print(report["error"], file=sys.stderr)
    return 0 if report["status"] == "passed" else 1


def terminate(signum, frame):
    raise KeyboardInterrupt(f"Terminated by signal {signum}")


def main():
    signal.signal(signal.SIGTERM, terminate)
    if sys.argv[1:] == ["--check-generated"]:
        check_generated()
        return 0
    elif sys.argv[1:]:
        return "Usage: python3 tools/verify.py"
    return verify()


if __name__ == "__main__":
    sys.exit(main())
