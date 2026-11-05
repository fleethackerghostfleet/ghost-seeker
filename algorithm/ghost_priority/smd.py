"""Import the repository's sampled SMD archive into separate evidence/eval inputs.

The evaluation proxy is 'has at least one source-annotated object', not an
operator judgement of interest. Object IDs are categories, never event IDs.
"""
from __future__ import annotations

import argparse
import csv
import json
import math
from collections import Counter
from pathlib import Path


def _inside(root, name):
    path = (root / name).resolve()
    if not path.is_relative_to(root) or not path.is_file():
        raise ValueError(f'Missing or unsafe dataset path: {name}')
    return path


def import_dataset(root, out):
    root, out = Path(root).resolve(), Path(out).resolve()
    manifest, labels, annotations, flags = [], {}, {}, []
    sources, classes = Counter(), Counter()
    with (root / 'index.csv').open(newline='') as f:
        rows = list(csv.DictReader(f))
    for row in rows:
        image = _inside(root, row['image'])
        label_path = _inside(root, row['label'])
        data = json.loads(label_path.read_text())
        video, frame = data['video'], data['frame_index']
        if not isinstance(frame, int) or isinstance(frame, bool) or frame < 0:
            raise ValueError('Frame index must be a nonnegative integer')
        if video != row['video'] or frame != int(row['frame_index']):
            raise ValueError('CSV and label frame identities disagree')
        stamp = data['time_s']
        if not isinstance(stamp, (int, float)) or isinstance(stamp, bool) or not math.isfinite(stamp) or stamp < 0:
            raise ValueError('Invalid source timestamp')
        objects = data['objects']
        if not isinstance(objects, list) or len(objects) != int(row['n_objects']):
            raise ValueError('CSV and label object counts disagree')
        width, height = data['width'], data['height']
        if width <= 0 or height <= 0:
            raise ValueError('Image dimensions must be positive')
        item_id = f'smd:{video}:{frame:05d}'
        if item_id in labels:
            raise ValueError(f'Duplicate frame: {item_id}')
        names = []
        for obj in objects:
            name, box = obj['object_type'], obj['bbox_xywh']
            if not isinstance(name, str) or not name:
                raise ValueError('Missing object category')
            if len(box) != 4 or any(isinstance(v, bool) or not isinstance(v, (int, float)) or not math.isfinite(v) for v in box):
                raise ValueError('Invalid bounding box')
            x, y, w, h = box
            if w <= 0 or h <= 0:
                raise ValueError('Bounding box dimensions must be positive')
            if x < 0 or y < 0 or x+w > width or y+h > height:
                flags.append({'id': item_id, 'flag': 'box_crosses_image_bounds', 'bbox_xywh': box})
            names.append(name)
            classes[name] += 1
        manifest.append({'id': item_id, 'path': str(image), 'source': video, 'timestamp_s': stamp})
        labels[item_id] = {'interesting': bool(objects), 'classes': sorted(set(names)),
                           'interest_definition': 'has_source_annotated_object'}
        # Preserve coordinates, IDs and horizon separately; none enter inference.
        annotations[item_id] = data
        sources[video] += 1
    if not manifest:
        raise ValueError('Dataset index is empty')
    summary = {'images': len(manifest), 'sources': dict(sources),
               'annotated_positive_images': sum(x['interesting'] for x in labels.values()),
               'empty_annotation_images': sum(not x['interesting'] for x in labels.values()),
               'object_instances': dict(classes), 'horizon_labels': sum(x['horizon'] is not None for x in annotations.values()),
               'flags': flags,
               'notes': ['Interest is a source-object-presence proxy, not operator relevance.',
                         'No event IDs are created: object_id is category-based in this archive.',
                         'Original boxes are preserved; clip a derived copy when drawing or computing IoU.',
                         'Source label errors and incomplete annotations can affect these metrics.']}
    out.mkdir(parents=True, exist_ok=False)
    (out / 'manifest.jsonl').write_text(''.join(json.dumps(r) + '\n' for r in manifest))
    for filename, value in [('presence_labels.json', labels), ('source_annotations.json', annotations), ('import_summary.json', summary)]:
        (out / filename).write_text(json.dumps(value, indent=2, allow_nan=False)+'\n')
    return summary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', required=True, help='Extracted SMD_VIS_Onboard_frames directory')
    parser.add_argument('--out', required=True, help='New directory for manifest and separate evaluation labels')
    args = parser.parse_args()
    print(json.dumps(import_dataset(args.root, args.out), indent=2))


if __name__ == '__main__':
    main()
