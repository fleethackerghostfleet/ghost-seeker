"""Local research HTTP API. Launch with --models <verified export directory>."""
from __future__ import annotations
import argparse
import json
import resource
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import urlparse
from .engine import ONNXEncoder, Vocabulary

MAX_BODY = 24 * 1024 * 1024


def handler_for(vocabulary):
    class Handler(BaseHTTPRequestHandler):
        def reply(self, status, payload):
            raw = json.dumps(payload, allow_nan=False).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(raw)

        def do_GET(self):
            if urlparse(self.path).path != "/health":
                return self.reply(404, {"error": "not found"})
            self.reply(200, {"status": "ready", "modelId": vocabulary.model_id, "dimension": vocabulary.dimension,
                             "minSimilarity": vocabulary.min_similarity, "minMargin": vocabulary.min_margin,
                             "labelStatus": "experimental; thresholds are not calibrated probabilities",
                             "peakRssBytes": resource.getrusage(resource.RUSAGE_SELF).ru_maxrss * (1 if sys.platform == "darwin" else 1024),
                             "imageEncoder": bool(getattr(vocabulary.encoder, "manifest", {}).get("imageGraph"))})

        def do_POST(self):
            try:
                size = int(self.headers.get("Content-Length", "0"))
                if size <= 0 or size > MAX_BODY:
                    raise ValueError("request body must be between 1 byte and 24 MB")
                request = json.loads(self.rfile.read(size))
                if not isinstance(request, dict):
                    raise ValueError("expected a JSON object")
                path = urlparse(self.path).path
                if path == "/embed":
                    vectors = vocabulary.embed_text(request.get("texts"))
                    response = {"modelId": vocabulary.model_id, "dimension": vocabulary.dimension, "embeddings": vectors.tolist()}
                elif path == "/classify":
                    response = vocabulary.classify(request.get("concepts"), request.get("items"))
                elif path == "/image-embed":
                    images = request.get("images")
                    if not isinstance(images, list) or not 1 <= len(images) <= 16:
                        raise ValueError("provide 1–16 already cropped images")
                    if any(not isinstance(i, dict) or not isinstance(i.get("id"), str) or not i["id"] or not isinstance(i.get("imageBase64"), str) for i in images):
                        raise ValueError("each image requires id and imageBase64")
                    if len({i["id"] for i in images}) != len(images):
                        raise ValueError("image IDs must be unique")
                    vectors = vocabulary.encoder.encode_images([i["imageBase64"] for i in images])
                    baseline = vocabulary.baseline.classify(vectors)
                    response = {"modelId": vocabulary.model_id, "dimension": vocabulary.dimension,
                                "items": [{"id": item["id"], "embedding": vector.tolist(), "modelId": vocabulary.model_id,
                                           "baselineSubtype": base["subtype"], "baselineUnknownness": base["unknownness"],
                                           "baselineSimilarity": base["max_sim"]} for item, vector, base in zip(images, vectors, baseline)]}
                else:
                    return self.reply(404, {"error": "not found"})
                self.reply(200, response)
            except (ValueError, TypeError, KeyError, OverflowError) as exc:
                self.reply(400, {"error": str(exc)})
            except Exception:
                # No stack traces or partial result responses across the API boundary.
                self.log_error("inference request failed")
                self.reply(503, {"error": "inference unavailable; active state was not changed"})
    return Handler


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--models", required=True)
    parser.add_argument("--listen", default="127.0.0.1:8091")
    parser.add_argument("--threads", type=int, default=2)
    parser.add_argument("--min-similarity", type=float, default=0.25)
    parser.add_argument("--min-margin", type=float, default=0.02)
    args = parser.parse_args()
    if not 1 <= args.threads <= 16:
        parser.error("threads must be between 1 and 16")
    engine = Vocabulary(ONNXEncoder(args.models, args.threads), args.min_similarity, args.min_margin)
    host, port = args.listen.rsplit(":", 1)
    server = HTTPServer((host, int(port)), handler_for(engine))
    print(json.dumps({"event": "ready", "address": args.listen, "modelId": engine.model_id}), flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
