"""Encode a JSONL manifest of detected crops into research /api/items JSON.

Each row: {path,id?,box?:[x1,y1,x2,y2],label?,confidence?,subtype?,unknownness?,
source?,capturedAtUnixMs?,labelOverride?,overrideBy?,tomerScore?}. Ground truth is
never read as an input feature. Missing detector metadata remains unknown.
"""
from __future__ import annotations
import argparse
import base64
import hashlib
import io
import json
from pathlib import Path
from urllib.request import Request, urlopen
from PIL import Image, ImageOps

FIELDS = {"label", "confidence", "subtype", "unknownness", "source", "capturedAtUnixMs", "labelOverride", "overrideBy", "tomerScore"}


def post(url, payload):
    request = Request(url, json.dumps(payload, allow_nan=False).encode(), {"Content-Type": "application/json"})
    with urlopen(request, timeout=120) as response:
        return json.load(response)


def prepare_crop(row, root):
    path = Path(row["path"])
    if not path.is_absolute():
        path = root / path
    with Image.open(path) as image:
        image = ImageOps.exif_transpose(image).convert("RGB")
        if "box" in row:
            box = row["box"]
            if len(box) != 4 or not 0 <= box[0] < box[2] <= image.width or not 0 <= box[1] < box[3] <= image.height:
                raise ValueError(f"invalid crop box for {path}")
            image = image.crop(tuple(box))
        crop = io.BytesIO()
        image.save(crop, format="JPEG", quality=95)
        raw = crop.getvalue()
        image.thumbnail((320, 320))
        thumb = io.BytesIO()
        image.save(thumb, format="JPEG", quality=85)
    identifier = row.get("id") or hashlib.sha256(raw).hexdigest()[:24]
    item = {key: value for key, value in row.items() if key in FIELDS}
    item.update({"id": identifier, "imageUrl": "data:image/jpeg;base64," + base64.b64encode(thumb.getvalue()).decode(),
                 "thumbBytes": len(thumb.getvalue()), "fullBytes": len(raw)})
    item.setdefault("label", "unknown")
    item.setdefault("confidence", 0.0)
    item.setdefault("unknownness", 1.0)
    item.setdefault("source", "research detected crop")
    return item, {"id": identifier, "imageBase64": base64.b64encode(raw).decode()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--vision-url", default="http://127.0.0.1:8098")
    parser.add_argument("--output", required=True)
    parser.add_argument("--backend-url")
    args = parser.parse_args()
    manifest = Path(args.manifest)
    rows = [json.loads(line) for line in manifest.read_text().splitlines() if line.strip()]
    items = []
    for start in range(0, len(rows), 8):
        prepared = [prepare_crop(row, manifest.parent) for row in rows[start:start + 8]]
        encoded = post(args.vision_url.rstrip("/") + "/image-embed", {"images": [pair[1] for pair in prepared]})
        by_id = {value["id"]: value for value in encoded["items"]}
        for item, _ in prepared:
            feature = by_id[item["id"]]
            item.update({"embedding": feature["embedding"], "modelId": feature["modelId"]})
            items.append(item)
    payload = {"items": items}
    Path(args.output).write_text(json.dumps(payload, allow_nan=False) + "\n")
    if args.backend_url:
        result = post(args.backend_url.rstrip("/") + "/api/items", payload)
        print(json.dumps(result))
    else:
        print(json.dumps({"output": args.output, "items": len(items), "modelId": items[0]["modelId"] if items else None}))


if __name__ == "__main__":
    main()
