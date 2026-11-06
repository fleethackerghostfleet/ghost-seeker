import hashlib
import importlib.util
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("prepare", Path(__file__).with_name("prepare.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class PrepareTests(unittest.TestCase):
    def catalog_sums(self):
        catalog = Path(__file__).resolve().parents[2] / "internal/catalog"
        return "".join(hashlib.sha256((catalog / name).read_bytes()).hexdigest() + f"  catalog/{name}\n"
                       for name in ("colregs_lights.json", "colregs_night_config.json"))

    def test_allowlisted_files_only(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            models = root / "models"
            models.mkdir()
            (models / "model.onnx").write_bytes(b"model")
            (models / "unlisted").write_bytes(b"unused")
            manifest = root / "sums"
            manifest.write_text(hashlib.sha256(b"model").hexdigest() + "  model.onnx\n" + self.catalog_sums())
            module.prepare(models, manifest, root / "out")
            self.assertEqual({p.name for p in (root / "out").iterdir()}, {"model.onnx", "SHA256SUMS", "catalog"})

    def test_missing_catalog_entries(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "model.onnx").write_bytes(b"model")
            for catalog in ("", self.catalog_sums().splitlines(keepends=True)[0]):
                manifest = root / "sums"
                manifest.write_text(hashlib.sha256(b"model").hexdigest() + "  model.onnx\n" + catalog)
                with self.assertRaisesRegex(ValueError, "missing required catalog entries.*colregs_night_config.json"):
                    module.prepare(root, manifest, root / "out")

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
