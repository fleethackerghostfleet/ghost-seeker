import unittest
from pathlib import Path

from security import ROOT, check_licenses, fingerprint, new_highs


class SecurityPolicyTests(unittest.TestCase):
    def issue(self, **changes):
        return {"file": str(ROOT / "edge/test.go"), "rule_id": "G115",
                "code": "11: value := int32(input)\n", "severity": "HIGH", **changes}

    def test_existing_high_allowed_but_new_high_fails(self):
        issue = self.issue()
        baseline = [fingerprint(issue)]
        self.assertEqual(new_highs({"Issues": [issue]}, baseline), [])
        self.assertEqual(new_highs({"Issues": [issue, issue]}, baseline), [issue])
        changed = self.issue(code="11: value := int32(other)\n")
        self.assertEqual(new_highs({"Issues": [changed]}, baseline), [changed])
        self.assertEqual(new_highs({"Issues": [self.issue(rule_id="G404")]}, baseline),
                         [self.issue(rule_id="G404")])

    def test_line_numbers_do_not_change_baseline(self):
        self.assertEqual(fingerprint(self.issue()),
                         fingerprint(self.issue(code="24: value := int32(input)\n")))

    def test_empty_gosec_report(self):
        self.assertEqual(new_highs({"Issues": None}, []), [])

    def test_gosec_analysis_errors_fail(self):
        with self.assertRaises(ValueError):
            new_highs({"Issues": [], "Golang errors": {"file.go": ["type error"]}}, [])

    def test_license_normal_and_empty_cases(self):
        self.assertEqual(check_licenses([("go", "MIT")], [{"Name": "py", "License": "BSD-3-Clause"}]), [])
        self.assertEqual(check_licenses([], []), [])
        self.assertEqual(check_licenses([("lib", "LGPL-3.0")], []), [])
        self.assertEqual(check_licenses([("lib", "GNU Lesser General Public License v3 (LGPLv3)")], []), [])

    def test_denied_and_unknown_licenses(self):
        for license_name in ("AGPL-3.0", "GPL-2.0-only", "MIT OR GPL-3.0", "GPLv3", "LGPL-3.0 OR GPL-3.0",
                             "GNU General Public License v3 (GPLv3)", "UNKNOWN", ""):
            with self.subTest(license=license_name):
                self.assertTrue(check_licenses([("lib", license_name)], []))

    def test_gosec_path_outside_repository_fails(self):
        with self.assertRaises(ValueError):
            fingerprint(self.issue(file=str(Path.home() / "outside.go")))


if __name__ == "__main__":
    unittest.main()
