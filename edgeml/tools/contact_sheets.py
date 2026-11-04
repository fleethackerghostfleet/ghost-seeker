"""Contact sheets for the detector benchmark: failure modes on labelled dev sets and qualitative clips.

  failures: python edgeml/tools/contact_sheets.py failures --run ~/runs/<run_id> --model yolo11n --set smd_vis_onboard_sample__dev
  clips:    python edgeml/tools/contact_sheets.py clips --models yolo11n,dfine-n --clips Sailboat_on_the_horizon,... --out ~/runs/<run_id>/clips

GT is drawn green, detections >= tau in red (vessel) / orange (other agnostic class). Sheets are JPEGs under <run>/sheets/.
"""
from __future__ import annotations

import argparse
import csv
import json
import math
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
sys.path.insert(0, str(Path(__file__).resolve().parent))

CAT = os.path.expanduser("~/data/catalog")
ROOT = os.path.expanduser("~/data")


def draw(img, gt_boxes, dets, tau, focus=None, pad=0):
    import cv2
    from edgeml.detectors.labels import agnostic

    out = img.copy()
    for b in gt_boxes:
        x, y, w, h = map(int, b)
        cv2.rectangle(out, (x, y), (x + w, y + h), (0, 255, 0), 2)
    for d in dets:
        if d.score < tau:
            continue
        a = agnostic(d.label)
        col = (0, 0, 255) if a == "vessel" else (0, 165, 255)
        x, y, w, h = map(int, d.box)
        cv2.rectangle(out, (x, y), (x + w, y + h), col, 2)
        cv2.putText(out, f"{d.label[:12]} {d.score:.2f}", (x, max(y - 4, 10)), cv2.FONT_HERSHEY_SIMPLEX, 0.5, col, 1, cv2.LINE_AA)
    if focus is not None:
        x, y, w, h = focus
        cx, cy = x + w / 2, y + h / 2
        s = max(w, h) * 4 + 2 * pad
        s = max(s, 160)
        x0, y0 = int(max(cx - s / 2, 0)), int(max(cy - s / 2, 0))
        x1, y1 = int(min(cx + s / 2, out.shape[1])), int(min(cy + s / 2, out.shape[0]))
        out = out[y0:y1, x0:x1]
    return out


def grid(tiles, cols=3, cell=(480, 300), title=None):
    import cv2
    import numpy as np

    cells = []
    for t, cap in tiles:
        h, w = t.shape[:2]
        sc = min(cell[0] / w, cell[1] / h)
        t = cv2.resize(t, (int(w * sc), int(h * sc)))
        canvas = np.full((cell[1] + 22, cell[0], 3), 30, np.uint8)
        canvas[:t.shape[0], :t.shape[1]] = t
        cv2.putText(canvas, cap[:70], (4, cell[1] + 16), cv2.FONT_HERSHEY_SIMPLEX, 0.45, (255, 255, 255), 1, cv2.LINE_AA)
        cells.append(canvas)
    while len(cells) % cols:
        cells.append(np.full_like(cells[0], 30))
    rows = [np.hstack(cells[i:i + cols]) for i in range(0, len(cells), cols)]
    sheet = np.vstack(rows)
    if title:
        bar = np.full((28, sheet.shape[1], 3), 60, np.uint8)
        cv2.putText(bar, title, (6, 20), cv2.FONT_HERSHEY_SIMPLEX, 0.6, (255, 255, 255), 1, cv2.LINE_AA)
        sheet = np.vstack([bar, sheet])
    return sheet


def failures(args):
    import cv2
    from bench_detectors import load_dets
    from edgeml.data.catalog import eval_set
    from edgeml.detectors.labels import agnostic
    from edgeml.eval.detection import match_at_threshold

    run = Path(args.run)
    metrics = json.loads((run / "metrics.json").read_text())
    ds, split = args.set.rsplit("__", 1)
    entry = metrics[args.model][args.set]
    tau = entry["tau"]
    images = eval_set(ds, split, CAT, ROOT, stride=args.stride)
    dets = load_dets(run / args.model / args.set / "dets.jsonl")
    m = match_at_threshold(images, dets, tau)
    per = m["per_image"]
    by_id = {im.image_id: im for im in images}
    out = run / "sheets"
    out.mkdir(exist_ok=True)
    # worst false-positive frames (sea-only first), worst misses (smallest GT first)
    fp_ids = sorted(per, key=lambda k: (-per[k][1], per[k][0]))[:args.k]
    fn = []
    for k, (tp, fp, nfn) in per.items():
        if nfn:
            im = by_id[k]
            vessel = [b for b, l in zip(im.boxes, im.labels) if agnostic(l) == "vessel"]
            fn.append((min(b[2] * b[3] for b in vessel), k))
    fn_ids = [k for _, k in sorted(fn)[:args.k]]
    for kind, ids in (("fp", fp_ids), ("fn", fn_ids)):
        tiles = []
        for k in ids:
            im = by_id[k]
            img = cv2.imread(im.local_path)
            if img is None:
                continue
            gt = [b for b, l in zip(im.boxes, im.labels) if agnostic(l) == "vessel"]
            focus = None
            if kind == "fn" and gt:
                focus = min(gt, key=lambda b: b[2] * b[3])
            tiles.append((draw(img, gt, dets.get(k, []), tau, focus=focus),
                          f"{k.split('/')[-1]} tp={per[k][0]} fp={per[k][1]} fn={per[k][2]}"))
        if tiles:
            sheet = grid(tiles, cols=args.cols, title=f"{args.model} {args.set} tau={tau} worst-{kind} (GT green, det red)")
            p = out / f"{args.model}__{args.set}__{kind}.jpg"
            cv2.imwrite(str(p), sheet, [cv2.IMWRITE_JPEG_QUALITY, 80])
            print(p)


def clips(args):
    import cv2
    from edgeml.data.catalog import clip_frames, clip_split
    from edgeml.detectors import build
    from edgeml.detectors.labels import agnostic

    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    summary = {}
    dets_models = {m: build(m, imgsz=args.imgsz, score_thr=0.05) for m in args.models.split(",")}
    taus = json.loads(Path(args.taus).read_text()) if args.taus else {}
    for clip in args.clips.split(","):
        frames = [f for f in clip_frames(clip, CAT, ROOT) if os.path.exists(f.local_path)]
        frames = frames[::args.stride][:args.max_frames]
        if not frames:
            print("no local frames for", clip)
            continue
        for mname, det in dets_models.items():
            tau = taus.get(mname, args.tau)
            tiles, n_det, n_frames_with = [], 0, 0
            recs = []
            for f in frames:
                img = cv2.imread(f.local_path)
                ds = det.detect(img)
                keep = [d for d in ds if d.score >= tau and agnostic(d.label) is not None]
                n_det += len(keep)
                n_frames_with += bool(keep)
                recs.append({"image_id": f.image_id, "n": len(keep), "dets": [[d.label, round(d.score, 3), [round(v, 1) for v in d.box]] for d in keep]})
                tiles.append((draw(img, [], ds, tau), f"{f.source_ref} n={len(keep)}"))
            sheet = grid(tiles[:args.max_tiles], cols=args.cols, title=f"{clip} [{clip_split(clip)}] {mname} tau={tau} ({n_frames_with}/{len(frames)} frames with dets)")
            p = out / f"{clip}__{mname}.jpg"
            cv2.imwrite(str(p), sheet, [cv2.IMWRITE_JPEG_QUALITY, 75])
            (out / f"{clip}__{mname}.jsonl").write_text("\n".join(json.dumps(r) for r in recs) + "\n")
            summary.setdefault(clip, {})[mname] = {"split": clip_split(clip), "frames": len(frames), "frames_with_dets": n_frames_with,
                                                   "dets_per_frame": round(n_det / len(frames), 3), "tau": tau}
            print(p, summary[clip][mname])
    (out / "summary.json").write_text(json.dumps(summary, indent=1))


def labels(args):
    import cv2
    import numpy as np

    root = Path(args.root).expanduser()
    groups = {}
    frames = {}
    for seq_dir in sorted(path for path in root.iterdir() if (path / "seqinfo.ini").exists()):
        info = {}
        for line in (seq_dir / "seqinfo.ini").read_text(encoding="utf-8").splitlines():
            if "=" in line and not line.lstrip().startswith("["):
                key, value = line.split("=", 1)
                info[key.strip()] = value.strip()
        labels = json.loads((seq_dir / "gt" / "labels.json").read_text(encoding="utf-8"))
        luma_by_frame = {}
        for line in (seq_dir / "frame_luma.jsonl").read_text(encoding="utf-8").splitlines():
            row = json.loads(line)
            luma_by_frame[int(row["frame"])] = float(row["mean_luma"])
        frame_attrs = {}
        attrs_path = seq_dir / "gt" / "frame_attrs.jsonl"
        if attrs_path.exists():
            for line in attrs_path.read_text(encoding="utf-8").splitlines():
                row = json.loads(line)
                frame_attrs[int(row["frame"])] = row
        box_attrs = {}
        attrs_path = seq_dir / "gt" / "attrs.jsonl"
        if attrs_path.exists():
            for line in attrs_path.read_text(encoding="utf-8").splitlines():
                row = json.loads(line)
                box_attrs[(int(row["frame"]), int(row["id"]))] = row
        gt_by_frame = {}
        gt_path = seq_dir / "gt" / "gt.txt"
        for line in gt_path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                fields = line.split(",")
                gt_by_frame.setdefault(int(fields[0]), []).append(fields)
        for frame in range(1, int(info["seqLength"]) + 1):
            frame_meta = frame_attrs.get(frame, {})
            luma = luma_by_frame.get(frame)
            night = bool(frame_meta.get("night_gemini")) or (luma is not None and luma < 60)
            has_boxes = bool(gt_by_frame.get(frame))
            stratum = (
                f"source={info.get('source_dataset', root.name)}|"
                f"night={str(night).lower()}|boxes={str(has_boxes).lower()}"
            )
            record = {
                "seq_id": seq_dir.name,
                "frame": frame,
                "source": info.get("source_dataset", root.name),
                "night": night,
                "luma": luma,
                "frame_attrs": frame_meta,
                "boxes": gt_by_frame.get(frame, []),
                "box_attrs": box_attrs,
                "classes": labels["classes"],
                "stratum": stratum,
                "seq_dir": seq_dir,
            }
            frames[(seq_dir.name, frame)] = record
            groups.setdefault(stratum, []).append((seq_dir.name, frame))
    rng = np.random.default_rng(0)
    selected = []
    for stratum in sorted(groups):
        members = groups[stratum]
        count = min(len(members), max(2, math.ceil(len(members) * 0.10)))
        indexes = rng.choice(len(members), size=count, replace=False)
        selected.extend((members[int(index)], stratum) for index in indexes)
    if len(selected) > 130:
        selected = sorted(selected, key=lambda item: rng.random())[:130]
    selected.sort(key=lambda item: item[0])
    sample_path = root / "qa_sample.csv"
    with sample_path.open("w", encoding="utf-8", newline="") as stream:
        writer = csv.DictWriter(
            stream, fieldnames=("seq_id", "frame", "stratum", "n_boxes", "verdict", "notes")
        )
        writer.writeheader()
        for (seq_id, frame), stratum in selected:
            writer.writerow(
                {
                    "seq_id": seq_id,
                    "frame": frame,
                    "stratum": stratum,
                    "n_boxes": len(frames[(seq_id, frame)]["boxes"]),
                    "verdict": "",
                    "notes": "",
                }
            )

    colors = [
        (0, 220, 255), (0, 165, 255), (0, 255, 100), (255, 180, 0), (255, 0, 200),
        (255, 255, 0), (160, 90, 255), (0, 100, 255), (100, 255, 200), (200, 160, 0),
        (255, 80, 80), (120, 255, 0), (180, 100, 40), (60, 200, 255), (210, 210, 210),
    ]

    def render(record):
        image_path = record["seq_dir"] / "img1" / f"{record['frame']:06d}.jpg"
        image = cv2.imread(str(image_path), cv2.IMREAD_COLOR)
        if image is None:
            raise FileNotFoundError(image_path)
        original_h, original_w = image.shape[:2]
        scale = 640 / original_w
        display = cv2.resize(image, (640, max(1, round(original_h * scale))))
        insets = []
        for fields in record["boxes"]:
            frame, track_id = int(fields[0]), int(fields[1])
            x, y, width, height = map(float, fields[2:6])
            class_id = int(float(fields[7]))
            vis = float(fields[8]) if len(fields) > 8 else 1.0
            label = record["classes"].get(str(class_id), str(class_id))
            attr = record["box_attrs"].get((frame, track_id), {})
            tags = "".join(
                letter
                for letter, enabled in (
                    ("N", attr.get("nav_lights_visible")),
                    ("A", attr.get("approaching")),
                    ("W", attr.get("wake")),
                )
                if enabled is True
            )
            tag_text = f" {tags}" if tags else ""
            color = colors[(class_id - 1) % len(colors)]
            x1, y1 = round(x * scale), round(y * scale)
            x2, y2 = round((x + width) * scale), round((y + height) * scale)
            cv2.rectangle(display, (x1, y1), (x2, y2), color, 2)
            cv2.putText(
                display,
                f"{track_id}:{label} {vis:.2f}{tag_text}",
                (max(0, x1), max(14, y1 - 4)),
                cv2.FONT_HERSHEY_SIMPLEX,
                0.43,
                color,
                1,
                cv2.LINE_AA,
            )
            if min(width, height) < 32:
                side = max(24, round(max(width, height) * 1.25))
                cx, cy = x + width / 2, y + height / 2
                crop_x1 = max(0, round(cx - side / 2))
                crop_y1 = max(0, round(cy - side / 2))
                crop_x2 = min(original_w, crop_x1 + side)
                crop_y2 = min(original_h, crop_y1 + side)
                crop = image[crop_y1:crop_y2, crop_x1:crop_x2]
                if crop.size:
                    zoom = cv2.resize(
                        crop,
                        (crop.shape[1] * 4, crop.shape[0] * 4),
                        interpolation=cv2.INTER_NEAREST,
                    )
                    insets.append((track_id, zoom, color))
        inset_cells = []
        for track_id, crop, color in insets:
            patch = np.full((136, 156, 3), 32, np.uint8)
            height, width = crop.shape[:2]
            factor = min(148 / width, 108 / height, 1.0)
            resized = cv2.resize(crop, (max(1, round(width * factor)), max(1, round(height * factor))))
            y_offset = 2 + (108 - resized.shape[0]) // 2
            x_offset = 4 + (148 - resized.shape[1]) // 2
            patch[y_offset : y_offset + resized.shape[0], x_offset : x_offset + resized.shape[1]] = resized
            cv2.rectangle(patch, (0, 0), (155, 135), color, 2)
            cv2.putText(
                patch, f"id {track_id} 4x", (4, 130), cv2.FONT_HERSHEY_SIMPLEX, 0.38,
                (255, 255, 255), 1, cv2.LINE_AA
            )
            inset_cells.append(patch)
        inset_rows = math.ceil(len(inset_cells) / 4) if inset_cells else 0
        tile = np.full((display.shape[0] + inset_rows * 136 + 26, 640, 3), 28, np.uint8)
        tile[: display.shape[0], :] = display
        for index, patch in enumerate(inset_cells):
            row, col = divmod(index, 4)
            y_offset = display.shape[0] + row * 136
            x_offset = col * 160
            tile[y_offset : y_offset + 136, x_offset : x_offset + 156] = patch
        caption = (
            f"{record['seq_id']} f{record['frame']} night={str(record['night']).lower()} "
            f"luma={record['luma'] if record['luma'] is not None else 'unknown'}"
        )
        cv2.putText(
            tile, caption[:100], (4, tile.shape[0] - 6), cv2.FONT_HERSHEY_SIMPLEX, 0.42,
            (255, 255, 255), 1, cv2.LINE_AA
        )
        return tile

    output_dir = root / "qa_round2"
    output_dir.mkdir(parents=True, exist_ok=True)
    tiles = [render(frames[key]) for key, _ in selected]
    for sheet_index in range(math.ceil(len(tiles) / 9)):
        page = tiles[sheet_index * 9 : (sheet_index + 1) * 9]
        if not page:
            continue
        cell_height = max(tile.shape[0] for tile in page)
        cells = []
        for tile in page:
            cell = np.full((cell_height, 640, 3), 28, np.uint8)
            cell[: tile.shape[0], :] = tile
            cells.append(cell)
        while len(cells) < 9:
            cells.append(np.full_like(cells[0], 28))
        sheet = np.vstack([np.hstack(cells[row : row + 3]) for row in range(0, 9, 3)])
        path = output_dir / f"sheet_{sheet_index + 1:02d}.jpg"
        cv2.imwrite(str(path), sheet, [cv2.IMWRITE_JPEG_QUALITY, 85])
        print(path)
    print(f"sample: {sample_path} ({len(selected)} frames)")


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    a = sub.add_parser("failures")
    a.add_argument("--run", required=True)
    a.add_argument("--model", required=True)
    a.add_argument("--set", required=True)
    a.add_argument("--stride", type=int, default=1, help="must match the bench run's stride for this set")
    a.add_argument("--k", type=int, default=9)
    a.add_argument("--cols", type=int, default=3)
    a.set_defaults(fn=failures)
    c = sub.add_parser("clips")
    c.add_argument("--models", required=True)
    c.add_argument("--clips", required=True)
    c.add_argument("--out", required=True)
    c.add_argument("--imgsz", type=int, default=640)
    c.add_argument("--tau", type=float, default=0.3)
    c.add_argument("--taus", default=None, help="json {model: tau} from the dev run")
    c.add_argument("--stride", type=int, default=1)
    c.add_argument("--max-frames", type=int, default=60)
    c.add_argument("--max-tiles", type=int, default=12)
    c.add_argument("--cols", type=int, default=3)
    c.set_defaults(fn=clips)
    l = sub.add_parser("labels")
    l.add_argument("--root", required=True, help="MOT set root")
    l.set_defaults(fn=labels)
    args = ap.parse_args()
    args.fn(args)


if __name__ == "__main__":
    main()
