"""Minimal InferenceService stub in Python (Connect protocol, JSON).

This is the shape the real sidecar must have. Replace `analyze` / `embed`
with ONNX Runtime (OpenVINO EP on Intel CPUs) calls; keep the HTTP contract.

Run:  python3 inference/stub/server.py --port 8090
Edge: ./bin/edge -infer http://localhost:8090
"""
import argparse
import hashlib
import json
import math
import random
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DIM = 64
MODEL = "python-stub-v1"


def text_vec(text: str):
    v = [0.0] * DIM
    for w in (text.lower().split() or ["nothing"]):
        w = w.rstrip("s")
        r = random.Random(int(hashlib.sha1(w.encode()).hexdigest(), 16))
        for i in range(DIM):
            v[i] += r.gauss(0, 1)
    n = math.sqrt(sum(x * x for x in v)) or 1.0
    return [x / n for x in v]


def analyze(image_b64: str, known_labels):
    # Stand-in: no detections, modest unknownness. A real implementation runs
    # a detector + CLIP-family embedding and derives unknownness from
    # confidence and distance to known_labels prompts.
    return {
        "detections": [],
        "embedding": text_vec("open ocean"),
        "unknownness": 0.1,
        "modelId": MODEL,
        "latencyMs": 1,
    }


def embed(texts):
    return {"embeddings": [{"values": text_vec(t)} for t in texts], "modelId": MODEL}


class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(n) or b"{}")
        if self.path.endswith("/Analyze"):
            out = analyze(body.get("image", ""), body.get("knownLabels", []))
        elif self.path.endswith("/EmbedText"):
            out = embed(body.get("texts", []))
        elif self.path.endswith("/Capabilities"):
            out = {"modelId": MODEL, "openVocabulary": False, "fixedLabels": [], "embeddingDim": DIM}
        else:
            self.send_response(404)
            self.end_headers()
            return
        data = json.dumps(out).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8090)
    a = ap.parse_args()
    print(f"inference stub on :{a.port}")
    ThreadingHTTPServer(("0.0.0.0", a.port), H).serve_forever()
