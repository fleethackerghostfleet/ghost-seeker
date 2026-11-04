"""Tracker adapters + the sighting aggregator (workstream C)."""
from .aggregator import AggregatorParams, SightingAggregator
from .boxmot_adapter import BoxmotTracker, make_tracker
from .embed import ColorHistEmbedder

__all__ = ["AggregatorParams", "SightingAggregator", "BoxmotTracker", "make_tracker", "ColorHistEmbedder"]
