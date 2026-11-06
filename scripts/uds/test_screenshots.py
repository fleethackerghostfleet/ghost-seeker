import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch


spec = importlib.util.spec_from_file_location('screenshots', Path(__file__).with_name('two-cluster-screenshots.py'))
screenshots = importlib.util.module_from_spec(spec)
with patch.dict(sys.modules, {'playwright': Mock(), 'playwright.sync_api': Mock()}):
    spec.loader.exec_module(screenshots)


class ScreenshotMetadataTest(unittest.TestCase):
    def test_paths_inside_checkout_are_relative(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            images = root/'images'
            images.mkdir()
            (images/'feed.png').touch()
            self.assertEqual(screenshots.screenshot_paths(images, root), ['images/feed.png'])

    def test_external_paths_are_absolute_and_sorted(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            root = base/'checkout'
            root.mkdir()
            images = base/'external-images'
            images.mkdir()
            for name in ['status.png', 'feed.png']:
                (images/name).touch()
            self.assertEqual(screenshots.screenshot_paths(images, root),
                             [str((images/name).resolve()) for name in ['feed.png', 'status.png']])

    def test_empty_directory_and_non_images_produce_no_paths(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.assertEqual(screenshots.screenshot_paths(root, root), [])
            (root/'metadata.json').write_text('{}')
            self.assertEqual(screenshots.screenshot_paths(root, root), [])


if __name__ == '__main__':
    unittest.main()
