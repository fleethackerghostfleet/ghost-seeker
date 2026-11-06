import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import yaml

spec = importlib.util.spec_from_file_location("release", Path(__file__).with_name("release.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseTests(unittest.TestCase):
    def test_versions(self):
        for version in ("0.1.0", "1.20.3-rc1", "1.2.3-rc.2"):
            self.assertEqual(release.validate_version(version), version)
        for version in ("", "../1.2.3", "v1.2.3", "1.2", "01.2.3", "1.2.3;true"):
            with self.assertRaises(ValueError):
                release.validate_version(version)

    def test_version_drift(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "VERSION").write_text("1.2.3-rc1\n")
            for role in ("ship", "shore"):
                package = root / f"deploy/zarf/{role}/zarf.yaml"
                bundle = root / f"bundles/prod-uds/{role}/uds-bundle.yaml"
                package.parent.mkdir(parents=True)
                bundle.parent.mkdir(parents=True)
                package.write_text(yaml.safe_dump({"metadata": {"version": "1.2.3-rc1"}}))
                bundle.write_text(yaml.safe_dump({"metadata": {"version": "1.2.3-rc1"}, "packages": [{"ref": "1.2.3-rc1"}]}))
            self.assertEqual(release.check_versions(root), "1.2.3-rc1")
            bundle.write_text(bundle.read_text().replace("ref: 1.2.3-rc1", "ref: 9.9.9"))
            with self.assertRaisesRegex(ValueError, "shore: version drift"):
                release.check_versions(root)
            print("negative version-drift test: rejected mismatching shore package ref")

    def test_checksums_and_split(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            release.checksums(root)
            self.assertEqual((root / "SHA256SUMS").read_text(), "")
            (root / "artifact").write_bytes(b"release contents")
            with patch.object(release, "LIMIT", 4):
                release.checksums(root)
            self.assertEqual((root / "artifact.part0000").read_bytes(), b"release contents")
            result = release.run("sha256sum", "-c", "SHA256SUMS", cwd=root, capture_output=True, text=True)
            self.assertIn("artifact: OK", result.stdout)
            self.assertIn("artifact.part0000: OK", result.stdout)

    def test_dirty_rejected_before_output(self):
        with patch.object(release, "check_versions"), patch.object(release.subprocess, "check_output", return_value=" M file\n"):
            with self.assertRaisesRegex(ValueError, "clean working tree"):
                release.release("1.2.3", "amd64", False)


if __name__ == "__main__":
    unittest.main()
