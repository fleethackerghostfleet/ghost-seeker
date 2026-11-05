import csv
import json
import tempfile
import unittest
from pathlib import Path

from ghost_priority.smd import import_dataset


class SmdTests(unittest.TestCase):
    def fixture(self, root):
        (root/'image.jpg').write_bytes(b'fixture existence only; image decoding is tested by the analyzer')
        data = {'video':'clip', 'frame_index':30, 'time_s':1.0, 'width':100, 'height':50, 'horizon':None,
                'objects':[{'object_id':3, 'object_type':'Vessel/ship', 'bbox_xywh':[-1,10,20,15]}]}
        (root/'label.json').write_text(json.dumps(data))
        with (root/'index.csv').open('w', newline='') as f:
            w=csv.writer(f);w.writerow(['video','frame_index','image','label','n_objects'])
            w.writerow(['clip',30,'image.jpg','label.json',1])
        return data

    def test_labels_and_horizon_stay_out_of_manifest(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); self.fixture(root)
            summary=import_dataset(root,root/'out')
            row=json.loads((root/'out/manifest.jsonl').read_text())
            self.assertEqual(set(row), {'id','path','source','timestamp_s'})
            labels=json.loads((root/'out/presence_labels.json').read_text())
            self.assertTrue(labels[row['id']]['interesting'])
            self.assertNotIn('event_id', labels[row['id']])
            annotations=json.loads((root/'out/source_annotations.json').read_text())
            self.assertEqual(annotations[row['id']]['objects'][0]['bbox_xywh'][0], -1)
            self.assertEqual(len(summary['flags']), 1)

    def test_explicit_empty_source_annotations_are_preserved(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); data=self.fixture(root);data['objects']=[]
            (root/'label.json').write_text(json.dumps(data))
            (root/'index.csv').write_text((root/'index.csv').read_text().replace('label.json,1','label.json,0'))
            result=import_dataset(root,root/'out')
            self.assertEqual(result['empty_annotation_images'], 1)
            labels=json.loads((root/'out/presence_labels.json').read_text())
            self.assertFalse(next(iter(labels.values()))['interesting'])

    def test_mismatched_index_rejected(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td);self.fixture(root)
            (root/'index.csv').write_text((root/'index.csv').read_text().replace('label.json,1','label.json,2'))
            with self.assertRaises(ValueError): import_dataset(root,root/'out')


if __name__ == '__main__': unittest.main()
