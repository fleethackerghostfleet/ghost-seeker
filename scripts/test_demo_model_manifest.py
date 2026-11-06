import json
from pathlib import Path
import tempfile
import unittest

from demo_model_manifest import REQUIRED, write_manifest


class DemoManifestTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        for name in REQUIRED:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"fixture")

    def test_inventory_includes_auxiliary_files_and_is_stable(self):
        extra = self.root / "mobileclip-s1/unknown_calib.npz"
        extra.write_bytes(b"calibration")
        manifest = write_manifest(self.root)
        before = manifest.read_bytes()
        self.assertEqual(len(json.loads(before)["artifacts"]), len(REQUIRED) + 1)
        self.assertEqual(write_manifest(self.root).read_bytes(), before)

    def test_empty_or_missing_required_files_fail_without_manifest(self):
        path = self.root / REQUIRED[0]
        path.write_bytes(b"")
        with self.assertRaisesRegex(ValueError, "missing or empty"):
            write_manifest(self.root)
        path.unlink()
        with self.assertRaisesRegex(ValueError, "missing or empty"):
            write_manifest(self.root)
        self.assertFalse((self.root / "manifest.json").exists())

    def test_changed_bundle_is_not_silently_reapproved(self):
        manifest = write_manifest(self.root)
        before = manifest.read_bytes()
        (self.root / REQUIRED[0]).write_bytes(b"changed")
        with self.assertRaisesRegex(ValueError, "changed since first use"):
            write_manifest(self.root)
        self.assertEqual(manifest.read_bytes(), before)

    def test_symlinks_are_rejected(self):
        (self.root / "mobileclip-s1/alias").symlink_to(self.root / REQUIRED[0])
        with self.assertRaisesRegex(ValueError, "symlink"):
            write_manifest(self.root)


if __name__ == "__main__":
    unittest.main()
