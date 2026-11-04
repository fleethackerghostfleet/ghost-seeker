"""Connect/HTTP+JSON sidecar serving ``ghostseeker.v1.InferenceService`` from the edgeml pipeline.

``python -m edgeml.sidecar --listen :9090 --detector ~/models/D/yolo11n-640/model.onnx --embed ~/models/D/mobileclip-s1``
or ``--mock`` for a scripted engine that needs no models (Go tests / CI). See ``edgeml/sidecar/README.md``.
"""

from .service import EdgemlEngine, InferenceService, MockEngine
from .server import make_server, serve

__all__ = ["EdgemlEngine", "InferenceService", "MockEngine", "make_server", "serve"]
