"""Tests for artifact integrity and semantic evaluation, independent of the model."""
import hashlib
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import fetch_granite
from eval_commands import equivalent, percentile, summarize


class ArtifactTests(unittest.TestCase):
    def test_activates_only_verified_download(self):
        data = b"known-good model bytes"
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(fetch_granite, "SIZE", len(data)), patch.object(fetch_granite, "SHA256", hashlib.sha256(data).hexdigest()), patch("urllib.request.urlopen", return_value=io.BytesIO(data)):
                result = fetch_granite.fetch(directory)
                self.assertEqual(result.read_bytes(), data)
                self.assertFalse(result.with_suffix(".gguf.part").exists())

    def test_corrupt_download_does_not_replace_existing_file(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / fetch_granite.FILENAME
            target.write_bytes(b"previous artifact")
            with patch("urllib.request.urlopen", return_value=io.BytesIO(b"corrupt")):
                with self.assertRaisesRegex(ValueError, "SHA-256 mismatch"):
                    fetch_granite.fetch(directory)
            self.assertEqual(target.read_bytes(), b"previous artifact")
            self.assertFalse(target.with_suffix(".gguf.part").exists())

    def test_verified_cache_does_not_access_network(self):
        data = b"cached artifact"
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / fetch_granite.FILENAME
            target.write_bytes(data)
            with patch.object(fetch_granite, "SIZE", len(data)), patch.object(fetch_granite, "SHA256", hashlib.sha256(data).hexdigest()), patch("urllib.request.urlopen") as opener:
                self.assertEqual(fetch_granite.fetch(directory), target)
                opener.assert_not_called()


class EvaluationTests(unittest.TestCase):
    def test_semantically_equal_plural_and_order(self):
        self.assertTrue(equivalent([{"concept":"rescue boats","action":"boost","weight":2}], [{"concept":"rescue boat","action":"boost","weight":2}]))

    def test_wrong_polarity_or_weight_fails(self):
        expected = [{"concept":"cargo ship","action":"suppress","weight":1}]
        self.assertFalse(equivalent([{"concept":"cargo ship","action":"boost","weight":1}], expected))
        self.assertFalse(equivalent([{"concept":"cargo ship","action":"suppress","weight":2}], expected))

    def test_paths_reported_separately(self):
        data = [{"correct":True,"supported":True,"path":"rules","category":"direct","latencyMs":1},
                {"correct":False,"supported":True,"path":"granite","category":"paraphrase","latencyMs":3}]
        result = summarize(data)
        self.assertEqual(result["supported"]["accuracy"], .5)
        self.assertEqual(result["path:granite"]["accuracy"], 0)
        self.assertEqual(result["path:rules"]["accuracy"], 1)
        self.assertEqual(percentile(list(range(1, 101)), .95), 95)


if __name__ == "__main__":
    unittest.main()
