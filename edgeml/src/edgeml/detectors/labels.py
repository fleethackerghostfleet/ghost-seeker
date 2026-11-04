"""Map any model's native label onto the class-agnostic taxonomy used for eval and sightings.

agnostic(label) -> "vessel" | "buoy" | "object" | None   (None = not a maritime object, drop)
"""
from __future__ import annotations

from typing import Optional

_VESSEL = {
    # COCO / generic
    "boat", "ship", "vessel", "watercraft", "yacht", "sailboat", "sail boat", "sailing boat", "sailing ship",
    "ferry", "cargo ship", "container ship", "tanker", "tanker ship", "oil tanker", "fishing boat", "fishing vessel",
    "speedboat", "speed boat", "motorboat", "motor boat", "kayak", "canoe", "jet ski", "warship", "destroyer",
    "frigate", "aircraft carrier", "navy ship", "patrol boat", "submarine", "cruise ship", "passenger ship",
    "tugboat", "tug", "barge", "lifeboat", "dinghy", "rowboat", "pontoon", "trawler", "bulk carrier",
    # SeaShips
    "ore carrier", "bulk cargo carrier", "general cargo ship",
    # Singapore Maritime / SMD
    "Vessel/ship", "Boat", "Speed boat", "Sail boat", "Kayak", "Ferry",
    # Argus (alimkacar) maritime checkpoints
    "obstacle_vessel",
}
_BUOY = {"buoy", "Buoy", "navigation buoy", "channel marker", "mooring buoy", "marker buoy"}
_OBJECT = {
    # things a watch-stander would still report / things in the maritime GT that are not vessels
    "bird", "airplane", "aeroplane", "helicopter", "Flying bird/plane", "Other", "person", "swimmer",
    "floating debris", "debris", "whale", "dolphin", "periscope", "obstacle", "iceberg", "rock",
    "surfboard", "kite", "surfer",
}


def agnostic(label: str) -> Optional[str]:
    if label is None:
        return None
    if label in _VESSEL or label.lower() in _VESSEL:
        return "vessel"
    if label in _BUOY or label.lower() in _BUOY:
        return "buoy"
    if label in _OBJECT or label.lower() in _OBJECT:
        return "object"
    return None


def is_vessel_gt(label: str) -> bool:
    return agnostic(label) == "vessel"


# Prompts for open-vocabulary detectors. Keep them short nouns; the agnostic() map covers them all.
MARITIME_PROMPTS = [
    "ship", "boat", "sailboat", "ferry", "cargo ship", "fishing boat", "speedboat", "kayak",
    "warship", "submarine", "buoy",
]
