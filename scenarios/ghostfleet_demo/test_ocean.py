import copy
import hashlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import zipfile

from PIL import Image

import ocean


class OceanTests(unittest.TestCase):
    def test_checked_crops(self):
        with tempfile.TemporaryDirectory() as tmp:
            archive = Path(tmp) / "source.zip"
            image = Image.new("RGB", (8, 6), "blue")
            source = io.BytesIO()
            image.save(source, format="JPEG")
            output = io.BytesIO()
            Image.open(io.BytesIO(source.getvalue())).crop((0, 0, 4, 3)).save(output, format="JPEG", quality=90)
            with zipfile.ZipFile(archive, "w") as z:
                z.writestr("sec_0001.jpg", source.getvalue())
            manifest = {"source_sha256": hashlib.sha256(archive.read_bytes()).hexdigest(), "crop_xywh": [0, 0, 4, 3], "jpeg": {"quality": 90}, "frames": [{"source": "sec_0001.jpg", "source_sha256": hashlib.sha256(source.getvalue()).hexdigest(), "output_sha256": hashlib.sha256(output.getvalue()).hexdigest()}]}
            self.assertEqual(ocean.crops(manifest, archive), [output.getvalue()])
            for field in ("source_sha256", "output_sha256"):
                bad = copy.deepcopy(manifest)
                bad["frames"][0][field] = "bad"
                with self.assertRaisesRegex(ValueError, "SHA-256 mismatch"):
                    ocean.crops(bad, archive)
            manifest["frames"] = []
            with self.assertRaisesRegex(ValueError, "empty ocean manifest"):
                ocean.crops(manifest, archive)

    def test_replay_names_and_repeatability(self):
        with tempfile.TemporaryDirectory() as tmp:
            manifest = {"source_zip": "unused", "replay_frames": 420, "name_stride": 3}
            with patch.object(ocean, "crops", return_value=[b"clear", b"overcast", b"glare", b"dusk"]), patch.object(Path, "read_text", return_value=json.dumps(manifest)):
                ocean.extract(tmp)
                first = {p.name: p.read_bytes() for p in (Path(tmp) / "Open_ocean_watch/img1").glob("*.jpg")}
                ocean.extract(tmp)
                second = {p.name: p.read_bytes() for p in (Path(tmp) / "Open_ocean_watch/img1").glob("*.jpg")}
            self.assertEqual(first, second)
            self.assertEqual(len(first), 420)
            self.assertEqual(first["000001.jpg"], b"clear")
            self.assertEqual(first["001258.jpg"], b"dusk")


if __name__ == "__main__":
    unittest.main()
