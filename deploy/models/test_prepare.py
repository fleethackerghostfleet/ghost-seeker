import hashlib
import importlib.util
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("prepare", Path(__file__).with_name("prepare.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class PrepareTests(unittest.TestCase):
    def test_allowlisted_files_only(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            models = root / "models"
            models.mkdir()
            (models / "model.onnx").write_bytes(b"model")
            (models / "unlisted").write_bytes(b"unused")
            manifest = root / "sums"
            manifest.write_text(hashlib.sha256(b"model").hexdigest() + "  model.onnx\n")
            module.prepare(models, manifest, root / "out")
            self.assertEqual({p.name for p in (root / "out").iterdir()}, {"model.onnx", "SHA256SUMS"})

    def test_empty_unsafe_missing_and_bad_checksum(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            models = root / "models"
            models.mkdir()
            (models / "model.onnx").write_bytes(b"model")
            manifest = root / "sums"
            for index, text in enumerate(["", "bad", *("0" * 64 + "  " + name + "\n"
                    for name in ["/absolute", "../outside", "missing", "model.onnx"])]):
                manifest.write_text(text)
                with self.assertRaises((ValueError, OSError, module.subprocess.CalledProcessError)):
                    module.prepare(models, manifest, root / f"out{index}")


if __name__ == "__main__":
    unittest.main()
