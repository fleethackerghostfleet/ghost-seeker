import json
import os
from pathlib import Path
import subprocess
import tempfile
import sys
import unittest
from unittest.mock import patch

import verify


class VerifyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.commit = "a" * 40
        self.git_patch = patch.object(verify, "git", side_effect=self.git)
        self.git_mock = self.git_patch.start()
        self.addCleanup(self.git_patch.stop)
        root_patch = patch.object(verify, "ROOT", self.root)
        root_patch.start()
        self.addCleanup(root_patch.stop)

    def git(self, *args):
        return self.commit if args == ("rev-parse", "HEAD") else ""

    def report(self):
        return json.loads(next((self.root / "dist" / "verify").glob("*/report.json")).read_text())

    def run_suite(self, results):
        commands = [(result["name"], ["fake"]) for result in results]
        with patch.object(verify, "checks", return_value=commands), \
                patch.object(verify.shutil, "which", return_value="/bin/tool"), \
                patch.object(verify, "run_check", side_effect=results):
            return verify.verify()

    def test_success_records_commit_and_all_checks(self):
        self.assertEqual(self.run_suite([{"name": "tests", "status": "passed"}]), 0)
        self.assertEqual(self.report()["commit"], self.commit)
        self.assertEqual(self.report()["status"], "passed")

    def test_failed_check_cannot_be_overwritten_by_later_success(self):
        self.assertEqual(self.run_suite([{"name": "a", "status": "failed"},
                                         {"name": "b", "status": "passed"}]), 1)
        self.assertEqual(len(self.report()["checks"]), 2)
        self.assertEqual(self.report()["status"], "failed")

    def test_dirty_worktree_fails_before_running_checks(self):
        self.git_mock.side_effect = lambda *args: " M source.go" if args[0] == "status" else self.commit
        with patch.object(verify, "run_check") as run:
            self.assertEqual(verify.verify(), 1)
        run.assert_not_called()
        self.assertIn("clean commit", self.report()["error"])

    def test_missing_tool_fails_instead_of_skipping(self):
        with patch.object(verify.shutil, "which", return_value=None):
            self.assertEqual(verify.verify(), 1)
        self.assertIn("Missing prerequisites", self.report()["error"])

    def test_worktree_mutation_invalidates_passed_checks(self):
        self.git_mock.side_effect = [self.commit, "", self.commit, " M go.sum"]
        self.assertEqual(self.run_suite([{"name": "tests", "status": "passed"}]), 1)
        self.assertIn("Source changed", self.report()["error"])

    def test_head_change_invalidates_passed_checks(self):
        self.git_mock.side_effect = [self.commit, "", "b" * 40]
        self.assertEqual(self.run_suite([{"name": "tests", "status": "passed"}]), 1)

    def test_missing_command_and_timeout_are_logged_as_failures(self):
        for error in (FileNotFoundError("missing"), subprocess.TimeoutExpired(["fake"], 3600)):
            with self.subTest(error=error), patch.object(verify.subprocess, "Popen", side_effect=error):
                result = verify.run_check("missing", ["fake"], self.root)
            self.assertEqual(result["status"], "failed")
            self.assertNotEqual(result["exit_code"], 0)
            self.assertTrue((self.root / result["log"]).read_text())

    def test_timeout_kills_child_processes(self):
        command = [sys.executable, "-c",
                   "import subprocess,sys,time; "
                   "p=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)']); "
                   "print(p.pid,flush=True); time.sleep(60)"]
        result = verify.run_check("timeout", command, self.root, timeout=1)
        self.assertEqual(result["status"], "failed")
        child = int((self.root / "timeout.log").read_text().splitlines()[0])
        try:
            os.kill(child, 0)
        except ProcessLookupError:
            return
        # A reparented zombie is terminated and cannot keep consuming resources.
        self.assertEqual(Path(f"/proc/{child}/stat").read_text().split()[2], "Z")

    def test_generated_comparison_includes_staged_changes(self):
        with patch.object(verify.subprocess, "run") as run:
            verify.check_generated()
        self.assertIn("HEAD", run.call_args.args[0])

    def test_untracked_generated_code_fails(self):
        self.git_mock.side_effect = None
        self.git_mock.return_value = "gen/untracked.go"
        with patch.object(verify.subprocess, "run"), self.assertRaises(RuntimeError):
            verify.check_generated()


class PackageStrictTests(unittest.TestCase):
    def test_optional_tool_skips_fail_in_strict_mode(self):
        script = Path(__file__).resolve().parents[1] / "deploy/zarf/check.sh"
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            target = root / "deploy/zarf"
            target.mkdir(parents=True)
            (target / "check.sh").write_text(script.read_text())
            (target / "check_manifests.py").write_text("")
            python = root / "python3"
            python.write_text("#!/bin/sh\nexit 0\n")
            python.chmod(0o755)
            # Only a Python stub is on PATH: kubeconform and zarf must not be skipped.
            for strict, expected in (("0", 0), ("1", 1)):
                result = subprocess.run(["/bin/sh", str(target / "check.sh")],
                                        env={"PATH": str(root) + ":/bin", "STRICT_CHECKS": strict},
                                        capture_output=True, text=True)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
