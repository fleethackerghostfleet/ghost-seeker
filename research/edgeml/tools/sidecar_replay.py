#!/usr/bin/env python
"""Replay a MOT-layout clip through a running edgeml sidecar (`make demo-sidecar`) and summarise the sightings it reports.

  python research/edgeml/tools/sidecar_replay.py ~/data/edgeml_eval/ghost_fleet_5fps/Submarine_surfacing --url http://127.0.0.1:9090

Posts every frame as the Go edge does (Connect JSON `Analyze`, base64 JPEG, capturedAtUnixMs at the clip's fps) and prints
one line per sighting (open/update/close counts, first/last frame, tentative flag, best label) plus GT object count, so
"one sighting per object" can be checked on a dev clip without the Go stack.
"""
from __future__ import annotations

import argparse
import base64
import json
import time
import urllib.request
from collections import defaultdict
from pathlib import Path

from edgeml_research.data.seqs import load_sequence


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("seq_root")
    ap.add_argument("--url", default="http://127.0.0.1:9090")
    ap.add_argument("--source", default=None)
    ap.add_argument("--every", type=int, default=1, help="send every n-th frame")
    ap.add_argument("--max-frames", type=int, default=None)
    ap.add_argument("--out", default=None, help="write the raw sightings (jsonl) here")
    ap.add_argument("--sheet", default=None, help="contact sheet (jpg): the frame + box of every open/update report")
    a = ap.parse_args()
    seq = load_sequence(Path(a.seq_root).expanduser())
    source = a.source or seq.seq_id
    t0_ms = 1_700_000_000_000
    per_sid: dict[str, dict] = defaultdict(lambda: {"events": defaultdict(int), "first": None, "last": None, "tentative": False,
                                                      "label": "", "subtype": "", "key_frames": 0})
    raw = []
    t_start = time.time()
    n = 0
    for fi, t_s, p in seq.frames(every=a.every, max_frames=a.max_frames):
        body = {"image": base64.b64encode(p.read_bytes()).decode(), "source": source, "capturedAtUnixMs": str(t0_ms + int(t_s * 1000)),
                "knownLabels": ["boat", "ship"], "wantEmbedding": False}
        req = urllib.request.Request(f"{a.url}/ghostseeker.v1.InferenceService/Analyze", data=json.dumps(body).encode(),
                                     headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=60) as r:
            resp = json.loads(r.read())
        n += 1
        for s in resp.get("sightings", []):
            raw.append({"frame_idx": fi, "t_s": t_s, **s})
            d = per_sid[s["id"]]
            d["events"][s["event"]] += 1
            d["first"] = d["first"] if d["first"] is not None else fi
            d["last"] = fi
            d["label"] = s.get("keyDetection", {}).get("label") or d["label"]
            if s.get("subtype") == "tentative":
                d["tentative"] = True
            d["subtype"] = s.get("subtype") or d["subtype"]
            d["key_frames"] += bool(s.get("isKeyFrame"))
    dt = time.time() - t_start
    gt = seq.gt_tracks()
    print(f"{seq.seq_id}: {n} frames in {dt:.1f}s ({1000 * dt / max(1, n):.0f} ms/frame incl. HTTP); GT objects: {len(gt)}")
    opens = [sid for sid, d in per_sid.items() if d["events"].get("EVENT_OPEN")]
    print(f"sightings opened: {len(opens)} (tentative: {sum(per_sid[s]['tentative'] for s in opens)}); "
          f"reports (open+update): {sum(d['events'].get('EVENT_OPEN', 0) + d['events'].get('EVENT_UPDATE', 0) for d in per_sid.values())}")
    for sid, d in sorted(per_sid.items(), key=lambda kv: kv[1]["first"] or 0):
        ev = ", ".join(f"{k.replace('EVENT_', '').lower()}×{v}" for k, v in sorted(d["events"].items()))
        print(f"  {sid}: frames {d['first']}-{d['last']} label={d['label']!r} subtype={d['subtype']!r}"
              f"{' TENTATIVE' if d['tentative'] else ''} [{ev}]")
    if a.out:
        Path(a.out).expanduser().write_text("".join(json.dumps(r) + "\n" for r in raw))
    if a.sheet:
        contact_sheet(seq, [r for r in raw if r["event"] in ("EVENT_OPEN", "EVENT_UPDATE")], Path(a.sheet).expanduser())


def contact_sheet(seq, reports: list[dict], out: Path, tile_w: int = 480, cols: int = 4) -> None:
    import cv2
    import numpy as np

    tiles = []
    for r in reports:
        img = cv2.imread(str(seq.frame_path(r["frame_idx"])))
        if img is None:
            continue
        H, W = img.shape[:2]
        b = r.get("keyDetection", {}).get("box") or {}
        x0, y0 = int(float(b.get("x", 0)) * W), int(float(b.get("y", 0)) * H)
        x1, y1 = int((float(b.get("x", 0)) + float(b.get("w", 0))) * W), int((float(b.get("y", 0)) + float(b.get("h", 0))) * H)
        tent = r.get("subtype") == "tentative"
        cv2.rectangle(img, (x0, y0), (x1, y1), (0, 200, 255) if tent else (0, 255, 0), max(2, W // 400))
        s = tile_w / W
        tile = cv2.resize(img, (tile_w, int(H * s)))
        txt = f"{r['id'].rsplit(':', 1)[-1]} {r['event'][6:].lower()} f{r['frame_idx']} {r.get('keyDetection', {}).get('label', '')}" + (" TENTATIVE" if tent else "")
        cv2.putText(tile, txt, (6, 18), cv2.FONT_HERSHEY_SIMPLEX, 0.5, (0, 0, 0), 3)
        cv2.putText(tile, txt, (6, 18), cv2.FONT_HERSHEY_SIMPLEX, 0.5, (255, 255, 255), 1)
        tiles.append(tile)
    if not tiles:
        return
    h = max(t.shape[0] for t in tiles)
    tiles = [cv2.copyMakeBorder(t, 0, h - t.shape[0], 0, 0, cv2.BORDER_CONSTANT, value=(30, 30, 30)) for t in tiles]
    while len(tiles) % cols:
        tiles.append(np.full_like(tiles[0], 30))
    rows = [cv2.hconcat(tiles[i:i + cols]) for i in range(0, len(tiles), cols)]
    out.parent.mkdir(parents=True, exist_ok=True)
    cv2.imwrite(str(out), cv2.vconcat(rows), [cv2.IMWRITE_JPEG_QUALITY, 85])


if __name__ == "__main__":
    main()
