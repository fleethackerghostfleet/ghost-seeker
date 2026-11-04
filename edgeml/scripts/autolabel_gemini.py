#!/usr/bin/env python3
"""Gemini 2.5 Flash frame labelling and greedy IoU track linking."""

from __future__ import annotations

import argparse
import base64
import concurrent.futures
import hashlib
import json
import math
import re
import subprocess
import threading
import time
from collections import Counter
from pathlib import Path
from typing import Any

import cv2
import requests

PROJECT = "qwiklabs-gcp-02-34a870014566"
MODEL = "gemini-2.5-flash"
ENDPOINT = (
    "https://aiplatform.googleapis.com/v1/projects/"
    f"{PROJECT}/locations/global/publishers/google/models/{MODEL}:generateContent"
)
LABELS = [
    "cargo",
    "tanker",
    "container",
    "fishing",
    "sailboat",
    "small_boat",
    "warship",
    "submarine",
    "ferry_passenger",
    "tug",
    "buoy",
    "aircraft",
    "person",
    "marine_animal",
    "unknown",
]
VISIBILITY = {"full": 1.0, "partial": 0.5, "heavy": 0.25}
THREAD_LOCAL = threading.local()
TOKEN_LOCK = threading.Lock()
ACCESS_TOKEN = None
ACCESS_TOKEN_TIME = 0.0


def vertex_schema() -> dict[str, Any]:
    object_schema = {
        "type": "OBJECT",
        "properties": {
            "label": {"type": "STRING", "enum": LABELS},
            "box_2d": {
                "type": "ARRAY",
                "items": {"type": "INTEGER"},
                "minItems": 4,
                "maxItems": 4,
            },
            "visibility": {"type": "STRING", "enum": ["full", "partial", "heavy"]},
            "nav_lights_visible": {"type": "BOOLEAN"},
            "lights": {
                "type": "ARRAY",
                "items": {
                    "type": "STRING",
                    "enum": [
                        "masthead_white",
                        "sidelight_red",
                        "sidelight_green",
                        "stern_white",
                        "all_round_white",
                        "all_round_red",
                        "deck_lights",
                        "other",
                    ],
                },
            },
            "approaching": {"type": "BOOLEAN", "nullable": True},
            "wake": {"type": "BOOLEAN"},
            "confidence": {"type": "NUMBER"},
        },
        "required": [
            "label",
            "box_2d",
            "visibility",
            "nav_lights_visible",
            "lights",
            "approaching",
            "wake",
            "confidence",
        ],
    }
    return {
        "type": "OBJECT",
        "properties": {
            "objects": {"type": "ARRAY", "items": object_schema},
            "night": {"type": "BOOLEAN"},
            "scene": {
                "type": "STRING",
                "enum": ["open_sea", "coastal", "harbour", "river", "other"],
            },
            "frame_notes": {"type": "STRING", "nullable": True},
        },
        "required": ["objects", "night", "scene"],
    }


def verifier_schema() -> dict[str, Any]:
    return {
        "type": "OBJECT",
        "properties": {
            "verdict": {"type": "STRING", "enum": ["real_object", "reject"]},
            "label": {"type": "STRING", "enum": LABELS},
            "box_ok": {"type": "BOOLEAN"},
            "reason": {"type": "STRING"},
        },
        "required": ["verdict", "label", "box_ok", "reason"],
    }


def parse_json_text(text: str) -> dict[str, Any]:
    cleaned = text.strip()
    cleaned = re.sub(r"^```(?:json)?\s*", "", cleaned)
    cleaned = re.sub(r"\s*```$", "", cleaned)
    start = cleaned.find("{")
    if start < 0:
        raise ValueError("response contained no JSON object")
    value, end = json.JSONDecoder().raw_decode(cleaned[start:])
    if not isinstance(value, dict) or not isinstance(value.get("objects"), list):
        raise ValueError("response JSON did not contain an objects array")
    if not isinstance(value.get("night"), bool):
        raise ValueError("response JSON has no boolean night field")
    if value.get("scene") not in {"open_sea", "coastal", "harbour", "river", "other"}:
        raise ValueError("response JSON has an invalid scene")
    if value.get("frame_notes") is not None and not isinstance(value["frame_notes"], str):
        raise ValueError("response JSON has an invalid frame_notes field")
    valid_lights = {
        "masthead_white",
        "sidelight_red",
        "sidelight_green",
        "stern_white",
        "all_round_white",
        "all_round_red",
        "deck_lights",
        "other",
    }
    for obj in value["objects"]:
        if not isinstance(obj, dict) or obj.get("label") not in LABELS:
            raise ValueError("response JSON has an invalid object label")
        box = obj.get("box_2d")
        if not isinstance(box, list) or len(box) != 4 or any(type(number) is not int for number in box):
            raise ValueError("response JSON has an invalid box_2d")
        if obj.get("visibility") not in VISIBILITY:
            raise ValueError("response JSON has an invalid visibility")
        if not isinstance(obj.get("nav_lights_visible"), bool):
            raise ValueError("response JSON has an invalid nav_lights_visible field")
        if not isinstance(obj.get("lights"), list) or any(light not in valid_lights for light in obj["lights"]):
            raise ValueError("response JSON has an invalid lights field")
        if obj.get("approaching") is not None and not isinstance(obj["approaching"], bool):
            raise ValueError("response JSON has an invalid approaching field")
        if not isinstance(obj.get("wake"), bool):
            raise ValueError("response JSON has an invalid wake field")
        confidence = obj.get("confidence")
        if isinstance(confidence, bool) or not isinstance(confidence, (float, int)) or not 0 <= confidence <= 1:
            raise ValueError("response JSON has an invalid confidence")
    return value


def parse_verifier_text(text: str) -> dict[str, Any]:
    cleaned = text.strip()
    cleaned = re.sub(r"^```(?:json)?\s*", "", cleaned)
    cleaned = re.sub(r"\s*```$", "", cleaned)
    start = cleaned.find("{")
    if start < 0:
        raise ValueError("response contained no JSON object")
    value, _ = json.JSONDecoder().raw_decode(cleaned[start:])
    if not isinstance(value, dict):
        raise ValueError("verifier response is not a JSON object")
    if value.get("verdict") not in {"real_object", "reject"}:
        raise ValueError("verifier response has an invalid verdict")
    if value.get("label") not in LABELS:
        raise ValueError("verifier response has an invalid label")
    if not isinstance(value.get("box_ok"), bool):
        raise ValueError("verifier response has an invalid box_ok field")
    if not isinstance(value.get("reason"), str):
        raise ValueError("verifier response has an invalid reason field")
    return value


def box_2d_to_xywh(box_2d: list[int], width: int, height: int) -> list[float] | None:
    if len(box_2d) != 4:
        return None
    ymin, xmin, ymax, xmax = box_2d
    x1 = max(0.0, min(float(width), float(xmin) * width / 1000))
    y1 = max(0.0, min(float(height), float(ymin) * height / 1000))
    x2 = max(0.0, min(float(width), float(xmax) * width / 1000))
    y2 = max(0.0, min(float(height), float(ymax) * height / 1000))
    box_width, box_height = x2 - x1, y2 - y1
    if box_width < 2 or box_height < 2 or box_width * box_height > width * height * 0.60:
        return None
    return [x1, y1, box_width, box_height]


def crop_verifier_box(
    image: Any,
    box: list[float],
) -> tuple[Any, tuple[int, int, int, int]]:
    image_height, image_width = image.shape[:2]
    x, y, box_width, box_height = map(float, box)
    center_x, center_y = x + box_width / 2, y + box_height / 2
    crop_width = max(64, math.ceil(box_width * 2))
    crop_height = max(64, math.ceil(box_height * 2))
    crop_x1 = math.floor(center_x - crop_width / 2)
    crop_y1 = math.floor(center_y - crop_height / 2)
    crop_x2 = crop_x1 + crop_width
    crop_y2 = crop_y1 + crop_height
    if crop_x1 < 0:
        crop_x1, crop_x2 = 0, min(image_width, crop_width)
    if crop_y1 < 0:
        crop_y1, crop_y2 = 0, min(image_height, crop_height)
    if crop_x2 > image_width:
        crop_x2, crop_x1 = image_width, max(0, image_width - crop_width)
    if crop_y2 > image_height:
        crop_y2, crop_y1 = image_height, max(0, image_height - crop_height)
    crop = image[crop_y1:crop_y2, crop_x1:crop_x2].copy()
    crop_height, crop_width = crop.shape[:2]
    scale = max(1.0, 256 / min(crop_height, crop_width))
    if scale > 1:
        crop = cv2.resize(
            crop,
            (max(1, math.ceil(crop_width * scale)), max(1, math.ceil(crop_height * scale))),
            interpolation=cv2.INTER_CUBIC,
        )
    box_x1 = round((x - crop_x1) * scale)
    box_y1 = round((y - crop_y1) * scale)
    box_x2 = round((x + box_width - crop_x1) * scale)
    box_y2 = round((y + box_height - crop_y1) * scale)
    result_height, result_width = crop.shape[:2]
    box_x1 = max(0, min(result_width - 1, box_x1))
    box_x2 = max(0, min(result_width - 1, box_x2))
    box_y1 = max(0, min(result_height - 1, box_y1))
    box_y2 = max(0, min(result_height - 1, box_y2))
    cv2.rectangle(crop, (box_x1, box_y1), (box_x2, box_y2), (0, 0, 255), 2)
    return crop, (crop_x1, crop_y1, crop_x2, crop_y2)


def iou(a: list[float], b: list[float]) -> float:
    ax1, ay1, aw, ah = a
    bx1, by1, bw, bh = b
    x1, y1 = max(ax1, bx1), max(ay1, by1)
    x2, y2 = min(ax1 + aw, bx1 + bw), min(ay1 + ah, by1 + bh)
    intersection = max(0.0, x2 - x1) * max(0.0, y2 - y1)
    union = aw * ah + bw * bh - intersection
    return intersection / union if union > 0 else 0.0


def nms(detections: list[dict[str, Any]], threshold: float = 0.6) -> list[dict[str, Any]]:
    kept = []
    for detection in sorted(detections, key=lambda item: -item["confidence"]):
        if all(iou(detection["box"], existing["box"]) < threshold for existing in kept):
            kept.append(detection)
    return kept


def token(force: bool = False) -> str:
    global ACCESS_TOKEN, ACCESS_TOKEN_TIME
    with TOKEN_LOCK:
        if force or ACCESS_TOKEN is None or time.monotonic() - ACCESS_TOKEN_TIME > 3000:
            result = subprocess.run(
                ["gcloud", "auth", "print-access-token"],
                check=True,
                capture_output=True,
                text=True,
            )
            ACCESS_TOKEN = result.stdout.strip()
            ACCESS_TOKEN_TIME = time.monotonic()
        return ACCESS_TOKEN


def session() -> requests.Session:
    if not hasattr(THREAD_LOCAL, "session"):
        THREAD_LOCAL.session = requests.Session()
    return THREAD_LOCAL.session


def response_text(response: dict[str, Any]) -> str:
    parts = response["candidates"][0]["content"]["parts"]
    return "".join(part.get("text", "") for part in parts if "text" in part)


def request_frame(
    frame: int,
    image_uri: str,
    prompt: str,
    cache_path: Path,
) -> tuple[dict[str, Any] | None, str | None]:
    if cache_path.exists():
        try:
            cached = json.loads(cache_path.read_text(encoding="utf-8"))
            if isinstance(cached, dict) and isinstance(cached.get("objects"), list):
                return cached, None
        except (json.JSONDecodeError, OSError):
            pass
    body = {
        "contents": [
            {
                "role": "user",
                "parts": [
                    {"fileData": {"mimeType": "image/jpeg", "fileUri": image_uri}},
                    {"text": prompt},
                ],
            }
        ],
        "generationConfig": {
            "temperature": 0,
            "thinkingConfig": {"thinkingBudget": 0},
            "maxOutputTokens": 4096,
            "responseMimeType": "application/json",
            "responseSchema": vertex_schema(),
        },
    }
    last_error = "unknown error"
    for attempt in range(3):
        try:
            headers = {"Authorization": f"Bearer {token()}", "Content-Type": "application/json"}
            result = session().post(ENDPOINT, headers=headers, json=body, timeout=180)
            if result.status_code == 401 and attempt < 2:
                token(force=True)
                time.sleep(0.5 * (2**attempt))
                continue
            if result.status_code == 429 or result.status_code >= 500:
                last_error = f"HTTP {result.status_code}: {result.text[:500]}"
                if attempt < 2:
                    time.sleep(1.5 * (2**attempt))
                    continue
                break
            result.raise_for_status()
            parsed = parse_json_text(response_text(result.json()))
            cache_path.parent.mkdir(parents=True, exist_ok=True)
            cache_path.write_text(json.dumps(parsed, ensure_ascii=False) + "\n", encoding="utf-8")
            return parsed, None
        except (requests.RequestException, KeyError, IndexError, TypeError, ValueError, json.JSONDecodeError) as exc:
            last_error = f"{type(exc).__name__}: {exc}"
            if attempt < 2:
                time.sleep(1.5 * (2**attempt))
    return None, f"frame {frame}: {last_error}"


def request_verification(
    frame: int,
    box_index: int,
    image_uri: str,
    crop_jpeg: bytes,
    prompt: str,
    cache_path: Path,
) -> tuple[dict[str, Any] | None, str | None]:
    if cache_path.exists():
        try:
            cached = parse_verifier_text(cache_path.read_text(encoding="utf-8"))
            return cached, None
        except (json.JSONDecodeError, OSError, ValueError):
            pass
    crop_data = base64.b64encode(crop_jpeg).decode("ascii")
    body = {
        "contents": [
            {
                "role": "user",
                "parts": [
                    {"fileData": {"mimeType": "image/jpeg", "fileUri": image_uri}},
                    {"inlineData": {"mimeType": "image/jpeg", "data": crop_data}},
                    {"text": prompt},
                ],
            }
        ],
        "generationConfig": {
            "temperature": 0,
            "thinkingConfig": {"thinkingBudget": 0},
            "maxOutputTokens": 4096,
            "responseMimeType": "application/json",
            "responseSchema": verifier_schema(),
        },
    }
    last_error = "unknown error"
    for attempt in range(3):
        try:
            headers = {"Authorization": f"Bearer {token()}", "Content-Type": "application/json"}
            result = session().post(ENDPOINT, headers=headers, json=body, timeout=180)
            if result.status_code == 401 and attempt < 2:
                token(force=True)
                time.sleep(0.5 * (2**attempt))
                continue
            if result.status_code == 429 or result.status_code >= 500:
                last_error = f"HTTP {result.status_code}: {result.text[:500]}"
                if attempt < 2:
                    time.sleep(1.5 * (2**attempt))
                    continue
                break
            result.raise_for_status()
            parsed = parse_verifier_text(response_text(result.json()))
            cache_path.parent.mkdir(parents=True, exist_ok=True)
            cache_path.write_text(json.dumps(parsed, ensure_ascii=False) + "\n", encoding="utf-8")
            return parsed, None
        except (
            requests.RequestException,
            KeyError,
            IndexError,
            TypeError,
            ValueError,
            json.JSONDecodeError,
        ) as exc:
            last_error = f"{type(exc).__name__}: {exc}"
            if attempt < 2:
                time.sleep(1.5 * (2**attempt))
    return None, f"frame {frame} box {box_index}: {last_error}"


def read_frame_info(seq_dir: Path, frame: int) -> tuple[int, int]:
    image = cv2.imread(str(seq_dir / "img1" / f"{frame:06d}.jpg"), cv2.IMREAD_COLOR)
    if image is None:
        raise FileNotFoundError(seq_dir / "img1" / f"{frame:06d}.jpg")
    return image.shape[1], image.shape[0]


def normalize_frame(
    parsed: dict[str, Any] | None,
    width: int,
    height: int,
) -> tuple[list[dict[str, Any]], int]:
    if parsed is None:
        return [], 0
    detections = []
    dropped = 0
    for obj in parsed.get("objects", []):
        label = obj.get("label")
        box = box_2d_to_xywh(obj.get("box_2d", []), width, height)
        if label not in LABELS or box is None:
            dropped += 1
            continue
        detection = {
            "label": label,
            "box": box,
            "visibility": obj.get("visibility", "heavy"),
            "nav_lights_visible": bool(obj.get("nav_lights_visible", False)),
            "lights": obj.get("lights") or [],
            "approaching": obj.get("approaching"),
            "wake": bool(obj.get("wake", False)),
            "confidence": float(obj.get("confidence", 0.0)),
        }
        detections.append(detection)
    return detections, dropped


def filter_frame_runaway(
    raw_objects: list[Any],
    detections: list[dict[str, Any]],
) -> tuple[list[dict[str, Any]], bool]:
    if len(raw_objects) >= 25:
        return [], True
    return detections, False


def filter_repeated_boxes(
    detections: list[dict[str, Any]],
    image_height: int,
) -> tuple[list[dict[str, Any]], int]:
    adjacency = [set() for _ in detections]
    for left in range(len(detections)):
        _, y_left, _, h_left = detections[left]["box"]
        center_left = y_left + h_left / 2
        for right in range(left + 1, len(detections)):
            _, y_right, _, h_right = detections[right]["box"]
            center_right = y_right + h_right / 2
            if (
                abs(h_left - h_right) <= 0.15 * max(h_left, h_right)
                and abs(center_left - center_right) <= 0.02 * image_height
            ):
                adjacency[left].add(right)
                adjacency[right].add(left)
    visited = set()
    dropped = set()
    for start in range(len(detections)):
        if start in visited:
            continue
        component = set()
        stack = [start]
        visited.add(start)
        while stack:
            current = stack.pop()
            component.add(current)
            for neighbor in adjacency[current] - visited:
                visited.add(neighbor)
                stack.append(neighbor)
        if len(component) >= 6:
            dropped.update(component)
    kept = [detection for index, detection in enumerate(detections) if index not in dropped]
    return kept, len(dropped)


def link_frames(
    frame_detections: dict[int, list[dict[str, Any]]],
) -> tuple[list[list[float | int]], list[dict[str, Any]]]:
    tracks: dict[int, dict[str, Any]] = {}
    next_id = 1
    for frame in sorted(frame_detections):
        detections = frame_detections[frame]
        candidates = []
        for track_id, track in tracks.items():
            if 1 <= frame - track["last_frame"] <= 3:
                for detection_index, detection in enumerate(detections):
                    overlap = iou(track["last_box"], detection["box"])
                    if overlap >= 0.2:
                        candidates.append((overlap, track_id, detection_index))
        assigned_tracks: set[int] = set()
        assigned_detections: set[int] = set()
        assignments = []
        for overlap, track_id, detection_index in sorted(candidates, reverse=True):
            if track_id in assigned_tracks or detection_index in assigned_detections:
                continue
            assigned_tracks.add(track_id)
            assigned_detections.add(detection_index)
            assignments.append((track_id, detection_index))
        for track_id, detection_index in assignments:
            track = tracks[track_id]
            detection = detections[detection_index]
            track["last_frame"] = frame
            track["last_box"] = detection["box"]
            track["frames"][frame] = detection
        for detection_index, detection in enumerate(detections):
            if detection_index in assigned_detections:
                continue
            tracks[next_id] = {
                "last_frame": frame,
                "last_box": detection["box"],
                "frames": {frame: detection},
            }
            next_id += 1

    class_ids = {label: index for index, label in enumerate(LABELS, start=1)}
    mot_rows: list[list[float | int]] = []
    attrs_rows = []
    for track_id, track in sorted(tracks.items()):
        detections = track["frames"]
        label = Counter(item["label"] for item in detections.values()).most_common(1)[0][0]
        for frame, detection in sorted(detections.items()):
            x, y, width, height = detection["box"]
            visibility = detection["visibility"]
            mot_rows.append(
                [
                    frame,
                    track_id,
                    x,
                    y,
                    width,
                    height,
                    1,
                    class_ids[label],
                    VISIBILITY.get(visibility, 0.25),
                ]
            )
            attrs = {
                "frame": frame,
                "id": track_id,
                "label": label,
                "nav_lights_visible": detection["nav_lights_visible"],
                "lights": detection["lights"],
                "approaching": detection["approaching"],
                "wake": detection["wake"],
                "visibility": visibility,
                "confidence": detection["confidence"],
            }
            for key in (
                "label_v1",
                "verifier",
                "verifier_box_ok",
                "verifier_reason",
            ):
                if key in detection:
                    attrs[key] = detection[key]
            attrs_rows.append(attrs)
    return sorted(mot_rows, key=lambda row: (row[0], row[1])), attrs_rows


def load_lumas(seq_dir: Path) -> dict[int, float]:
    path = seq_dir / "frame_luma.jsonl"
    return {
        int(row["frame"]): float(row["mean_luma"])
        for row in (json.loads(line) for line in path.read_text().splitlines() if line)
    }


def annotate_sequence(
    seq_dir: Path,
    prompt_path: Path,
    workers: int = 8,
    verify_prompt_path: Path | None = None,
) -> dict[str, Any]:
    prompt_sha = hashlib.sha256(prompt_path.read_bytes()).hexdigest()
    if verify_prompt_path is None:
        verify_prompt_path = Path("~/r2/gemini_verify_prompt.txt").expanduser()
    verify_prompt = verify_prompt_path.read_text(encoding="utf-8")
    verify_prompt_sha = hashlib.sha256(verify_prompt_path.read_bytes()).hexdigest()
    info = configparser_values(seq_dir / "seqinfo.ini")
    count = int(info["seqLength"])
    frame_map = json.loads((seq_dir / "frame_map.json").read_text())
    teacher_dir = seq_dir / "teacher"
    cache_dir = teacher_dir / "cache"
    teacher_dir.mkdir(parents=True, exist_ok=True)

    parsed_by_frame: dict[int, dict[str, Any] | None] = {}
    for frame in range(1, count + 1):
        cache_path = cache_dir / f"frame_{frame:06d}.json"
        try:
            cached = json.loads(cache_path.read_text(encoding="utf-8"))
            parsed_by_frame[frame] = (
                cached
                if isinstance(cached, dict) and isinstance(cached.get("objects"), list)
                else None
            )
        except (json.JSONDecodeError, OSError):
            parsed_by_frame[frame] = None

    lumas = load_lumas(seq_dir)
    f2_by_frame: dict[int, list[dict[str, Any]]] = {}
    frame_attrs_by_frame = {}
    v1_raw_count = after_f2_count = after_f1_count = 0
    f2_dropped = f1_dropped = invalid_dropped = 0
    for frame in range(1, count + 1):
        parsed = parsed_by_frame[frame]
        width, height = read_frame_info(seq_dir, frame)
        raw_objects = parsed.get("objects", []) if parsed else []
        v1_raw_count += len(raw_objects)
        detections, invalid_count = normalize_frame(parsed, width, height)
        detections, runaway = filter_frame_runaway(raw_objects, detections)
        if runaway:
            f2_dropped += len(raw_objects)
        else:
            invalid_dropped += invalid_count
        after_f2_count += len(detections)
        detections, repeated_count = filter_repeated_boxes(detections, height)
        f1_dropped += repeated_count
        after_f1_count += len(detections)
        f2_by_frame[frame] = detections
        frame_attrs = {
            "frame": frame,
            "night_gemini": parsed.get("night") if parsed else None,
            "mean_luma": lumas.get(frame),
            "scene": parsed.get("scene") if parsed else None,
            "notes": parsed.get("frame_notes") if parsed else None,
        }
        if runaway:
            frame_attrs["teacher_status"] = "runaway_dropped"
        elif parsed is None:
            frame_attrs["teacher_status"] = "v1_cache_missing"
        frame_attrs_by_frame[frame] = frame_attrs

    verified_by_frame: dict[int, list[dict[str, Any]]] = {
        frame: [] for frame in range(1, count + 1)
    }
    verifier_errors = []
    verifier_rejected = 0
    futures = {}
    with concurrent.futures.ThreadPoolExecutor(max_workers=min(8, max(1, workers))) as pool:
        for frame, detections in f2_by_frame.items():
            if not detections:
                continue
            image_path = seq_dir / "img1" / f"{frame:06d}.jpg"
            image = cv2.imread(str(image_path), cv2.IMREAD_COLOR)
            if image is None:
                image_error = f"cannot read verifier image: {image_path}"
                for box_index, detection in enumerate(detections, start=1):
                    detection.update(
                        {
                            "label_v1": detection["label"],
                            "verifier": "error",
                            "verifier_box_ok": None,
                            "verifier_reason": "verification_failed",
                        }
                    )
                    verified_by_frame[frame].append(detection)
                    verifier_errors.append(
                        {
                            "frame": frame,
                            "box_index": box_index,
                            "source_uri": frame_map[str(frame)]["gs_uri"],
                            "error": image_error,
                        }
                    )
                continue
            for box_index, detection in enumerate(detections, start=1):
                detection["label_v1"] = detection["label"]
                crop, _ = crop_verifier_box(image, detection["box"])
                encoded_ok, encoded = cv2.imencode(".jpg", crop)
                if not encoded_ok:
                    detection.update(
                        {
                            "verifier": "error",
                            "verifier_box_ok": None,
                            "verifier_reason": "verification_failed",
                        }
                    )
                    verified_by_frame[frame].append(detection)
                    verifier_errors.append(
                        {
                            "frame": frame,
                            "box_index": box_index,
                            "source_uri": frame_map[str(frame)]["gs_uri"],
                            "error": "failed to encode verifier crop",
                        }
                    )
                    continue
                cache_path = (
                    cache_dir
                    / "verifier"
                    / f"frame_{frame:06d}_box_{box_index:04d}.json"
                )
                future = pool.submit(
                    request_verification,
                    frame,
                    box_index,
                    frame_map[str(frame)]["gs_uri"],
                    encoded.tobytes(),
                    verify_prompt,
                    cache_path,
                )
                futures[future] = (frame, box_index, detection)
        for future in concurrent.futures.as_completed(futures):
            frame, box_index, detection = futures[future]
            result, error = future.result()
            if error:
                detection.update(
                    {
                        "verifier": "error",
                        "verifier_box_ok": None,
                        "verifier_reason": "verification_failed",
                    }
                )
                verified_by_frame[frame].append(detection)
                verifier_errors.append(
                    {
                        "frame": frame,
                        "box_index": box_index,
                        "source_uri": frame_map[str(frame)]["gs_uri"],
                        "error": error,
                    }
                )
            elif result["verdict"] == "reject":
                verifier_rejected += 1
            else:
                detection.update(
                    {
                        "label": result["label"],
                        "verifier": "real_object",
                        "verifier_box_ok": result["box_ok"],
                        "verifier_reason": result["reason"],
                    }
                )
                verified_by_frame[frame].append(detection)

    after_verifier_count = sum(len(detections) for detections in verified_by_frame.values())
    final_by_frame = {}
    nms_dropped = 0
    for frame, detections in verified_by_frame.items():
        final_by_frame[frame] = nms(detections)
        nms_dropped += len(detections) - len(final_by_frame[frame])

    mot_rows, attrs_rows = link_frames(final_by_frame)
    class_names = {str(index): label for index, label in enumerate(LABELS, start=1)}
    gt_text = "".join(
        f"{int(frame)},{int(track_id)},{float(x):.1f},{float(y):.1f},{float(width):.1f},"
        f"{float(height):.1f},{int(conf)},{int(class_id)},{float(vis):.2f}\n"
        for frame, track_id, x, y, width, height, conf, class_id, vis in mot_rows
    )
    (seq_dir / "gt" / "gt.txt").parent.mkdir(parents=True, exist_ok=True)
    (seq_dir / "gt" / "gt.txt").write_text(gt_text, encoding="utf-8")
    (teacher_dir / "gt_teacher.txt").write_text(gt_text, encoding="utf-8")
    (seq_dir / "gt" / "attrs.jsonl").write_text(
        "".join(json.dumps(row, ensure_ascii=False) + "\n" for row in attrs_rows),
        encoding="utf-8",
    )
    (seq_dir / "gt" / "frame_attrs.jsonl").write_text(
        "".join(
            json.dumps(frame_attrs_by_frame[frame], ensure_ascii=False) + "\n"
            for frame in range(1, count + 1)
        ),
        encoding="utf-8",
    )
    (teacher_dir / "verifier_errors.jsonl").write_text(
        "".join(
            json.dumps(error, ensure_ascii=False) + "\n"
            for error in sorted(
                verifier_errors, key=lambda row: (row["frame"], row["box_index"])
            )
        ),
        encoding="utf-8",
    )
    v1_errors_path = teacher_dir / "errors.jsonl"
    v1_error_count = (
        sum(bool(line.strip()) for line in v1_errors_path.read_text().splitlines())
        if v1_errors_path.exists()
        else 0
    )
    stage_counts = {
        "v1_raw": v1_raw_count,
        "after_f2": after_f2_count,
        "after_f1": after_f1_count,
        "after_verifier": after_verifier_count,
        "verifier_errors": len(verifier_errors),
        "verifier_rejected": verifier_rejected,
    }
    labels = {
        "classes": class_names,
        "label_source": "teacher",
        "method": {
            "model": MODEL,
            "prompt_sha256": prompt_sha,
            "v2_enabled": True,
            "decoding": {
                "temperature": 0,
                "thinkingBudget": 0,
                "maxOutputTokens": 4096,
                "responseMimeType": "application/json",
            },
            "stages": {
                "F2": {"rule": "drop all objects when v1 raw object count is >=25"},
                "F1": {
                    "rule": "drop connected components of >=6 boxes",
                    "height_tolerance": 0.15,
                    "vertical_center_tolerance_image_height": 0.02,
                },
                "V": {
                    "model": MODEL,
                    "prompt_sha256": verify_prompt_sha,
                    "crop": {
                        "margin_fraction_per_side": 0.5,
                        "minimum_size": [64, 64],
                        "minimum_upscaled_short_side": 256,
                        "box_color": "red",
                        "box_thickness": 2,
                    },
                    "on_error": "keep_box",
                },
            },
            "stage_counts": stage_counts,
            "nms": {"type": "class_agnostic", "iou": 0.6},
            "linker": {"type": "greedy_class_agnostic_iou", "iou": 0.2, "max_gap_frames": 2},
        },
    }
    (seq_dir / "gt" / "labels.json").write_text(json.dumps(labels, indent=1) + "\n", encoding="utf-8")
    histogram = Counter(class_names[str(int(row[7]))] for row in mot_rows)
    report = {
        "seq_id": seq_dir.name,
        "frames": count,
        **stage_counts,
        "v1_errors": v1_error_count,
        "boxes": len(mot_rows),
        "tracks": len({row[1] for row in mot_rows}),
        "dropped_boxes": (
            f2_dropped + invalid_dropped + f1_dropped + verifier_rejected + nms_dropped
        ),
        "class_histogram": dict(histogram),
        "night_gemini": sum(
            bool(parsed.get("night")) for parsed in parsed_by_frame.values() if parsed
        ),
        "night_luma_lt_60": sum(value < 60 for value in lumas.values()),
    }
    print(
        f"{report['seq_id']}: v1_raw={v1_raw_count} after_F2={after_f2_count} "
        f"after_F1={after_f1_count} after_verifier={after_verifier_count} "
        f"verifier_errors={len(verifier_errors)} boxes={report['boxes']}"
    )
    return report


def configparser_values(path: Path) -> dict[str, str]:
    import configparser

    parser = configparser.ConfigParser()
    parser.optionxform = str
    parser.read(path)
    return dict(parser["Sequence"])


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path("~/eval/ghost_fleet_r2_1fps").expanduser())
    parser.add_argument("--prompt", type=Path, default=Path("~/r2/gemini_prompt.txt").expanduser())
    parser.add_argument(
        "--verify-prompt",
        type=Path,
        default=Path("~/r2/gemini_verify_prompt.txt").expanduser(),
    )
    parser.add_argument("--workers", type=int, default=8)
    args = parser.parse_args()
    summaries = []
    index_rows = []
    for seq_dir in sorted(path for path in args.root.iterdir() if (path / "seqinfo.ini").exists()):
        summary = annotate_sequence(seq_dir, args.prompt, args.workers, args.verify_prompt)
        summaries.append(summary)
        info = configparser_values(seq_dir / "seqinfo.ini")
        gt_rows = [
            line.split(",")
            for line in (seq_dir / "gt" / "gt.txt").read_text(encoding="utf-8").splitlines()
            if line.strip()
        ]
        labels = json.loads((seq_dir / "gt" / "labels.json").read_text(encoding="utf-8"))
        index_rows.append(
            {
                "seq_id": seq_dir.name,
                "set": args.root.name,
                "source_dataset": info["source_dataset"],
                "split": "dev",
                "fps": float(info["frameRate"]),
                "n_frames": int(info["seqLength"]),
                "n_tracks": len({row[1] for row in gt_rows}),
                "n_boxes": len(gt_rows),
                "width": int(info["imWidth"]),
                "height": int(info["imHeight"]),
                "label_source": labels["label_source"],
            }
        )
    (args.root / "gemini_summary.json").write_text(json.dumps(summaries, indent=2) + "\n")
    (args.root / "index.jsonl").write_text(
        "".join(json.dumps(row, ensure_ascii=False) + "\n" for row in index_rows),
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()
