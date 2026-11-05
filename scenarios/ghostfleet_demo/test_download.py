import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("download", Path(__file__).with_name("download.py"))
download = importlib.util.module_from_spec(spec)
spec.loader.exec_module(download)


class DownloadTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.here = Path(self.temp.name)
        self.original = {"name": "test", "period_s": .2, "events": [{"at_s": 3}],
                         "sets": [{"source": "eval", "path": "frames/eval/img1", "label": "boat"}]}
        (self.here / "scenario.json").write_text(json.dumps(self.original))
        self.env = {"BUCKET": "gs://test/eval", "CLIPS": "eval"}

    def fake_run(self, args, **kwargs):
        if args[2] == "ls":
            return subprocess.CompletedProcess(args, 0, "gs://test/frames/sailboat_frames/\n"
                                               "gs://test/frames/SMD_frames/\n"
                                               "gs://test/frames/not-a-set/\n")
        self.assertIn("--checksums-only", args)
        self.assertIn("--delete-unmatched-destination-objects", args)
        dest = Path(args[4])
        if dest.name == "eval":
            dest /= "img1"
        dest.mkdir(parents=True, exist_ok=True)
        (dest / "sec_0001.jpg").write_bytes(b"image")
        return subprocess.CompletedProcess(args, 0)

    def prepare(self, run=None):
        with patch.object(download, "HERE", self.here), patch.object(
                download.subprocess, "run", side_effect=run or self.fake_run) as mocked:
            download.prepare(self.env)
            return mocked

    def generated(self):
        return json.loads((self.here / "scenario.generated.json").read_text())

    def test_discovery_preserves_fields_and_rerun_syncs(self):
        self.prepare()
        mocked = self.prepare()
        result = self.generated()
        self.assertEqual(result["events"], self.original["events"])
        self.assertEqual(result["period_s"], .2)
        self.assertEqual([s["source"] for s in result["sets"]], ["eval", "SMD_frames", "sailboat_frames"])
        self.assertEqual(result["sets"][0]["label"], "boat")
        self.assertEqual(mocked.call_count, 4)
        self.assertEqual(json.loads((self.here / "scenario.json").read_text()), self.original)

    def test_filters_ignore_cached_sets_and_opt_out(self):
        self.prepare()
        self.env.update(FRAME_SETS_INCLUDE="*frames", FRAME_SETS_EXCLUDE="SMD*")
        self.prepare()
        self.assertEqual([s["source"] for s in self.generated()["sets"]], ["eval", "sailboat_frames"])
        self.env["INCLUDE_BUCKET_FRAMES"] = "0"
        self.assertEqual(self.prepare().call_count, 1)
        self.assertEqual(len(self.generated()["sets"]), 1)

    def test_empty_removes_generated_for_fallback(self):
        self.prepare()
        self.env.update(CLIPS="", INCLUDE_BUCKET_FRAMES="0")
        self.prepare()
        self.assertFalse((self.here / "scenario.generated.json").exists())

    def test_released_eval_cache_and_live_scene_cadence(self):
        scene = {"source": "Ocean", "path": "frames/Ocean/img1", "scene_every_s": 4}
        live = dict(self.original, sets=self.original["sets"] + [scene])
        (self.here / "live.json").write_text(json.dumps(live))
        clip = download.DEFAULT_CLIPS.split()[0]
        path = self.here / "frames" / clip / "img1"
        path.mkdir(parents=True)
        (path / "000001.jpg").write_bytes(b"image")
        self.env = {"CLIPS": clip, "INCLUDE_BUCKET_FRAMES": "0"}
        self.prepare().assert_not_called()
        self.assertEqual(self.generated()["sets"][-1], scene)

    def test_release_first_and_gcs_fallback(self):
        clip = download.DEFAULT_CLIPS.split()[0]
        self.env = {"CLIPS": clip, "INCLUDE_BUCKET_FRAMES": "0"}
        for release_ok in [False, True]:
            with self.subTest(release_ok=release_ok):
                path = self.here / "frames" / clip / "img1"
                if path.exists():
                    (path / "000001.jpg").unlink()
                def run(args, **kwargs):
                    if args[0] == "gcloud" or release_ok:
                        path.mkdir(parents=True, exist_ok=True)
                        (path / "000001.jpg").write_bytes(b"image")
                    return subprocess.CompletedProcess(args, 0 if release_ok or args[0] == "gcloud" else 1)
                mocked = self.prepare(run)
                self.assertTrue(mocked.call_args_list[0].args[0][0].endswith("deploy/demo-data.sh"))
                self.assertEqual(mocked.call_count, 1 if release_ok else 2)
                self.assertEqual(self.generated()["sets"][0]["source"], clip)

    def test_sync_failure_does_not_replace_scenario(self):
        self.prepare()
        before = self.generated()
        with self.assertRaises(subprocess.CalledProcessError):
            self.prepare(lambda args, **kwargs: (_ for _ in ()).throw(subprocess.CalledProcessError(1, args)))
        self.assertEqual(self.generated(), before)

    def test_empty_listing_and_empty_set(self):
        def empty(args, **kwargs):
            return subprocess.CompletedProcess(args, 0, "")
        self.prepare(empty)
        self.assertFalse((self.here / "scenario.generated.json").exists())

    def test_no_remote_frames_and_listing_auth_failure(self):
        self.env["CLIPS"] = ""
        self.prepare(lambda args, **kwargs: subprocess.CompletedProcess(
            args, 1, "", "ERROR: One or more URLs matched no objects."))
        self.assertFalse((self.here / "scenario.generated.json").exists())
        with self.assertRaises(subprocess.CalledProcessError):
            self.prepare(lambda args, **kwargs: subprocess.CompletedProcess(
                args, 1, "", "403 Permission denied"))


if __name__ == "__main__":
    unittest.main()
