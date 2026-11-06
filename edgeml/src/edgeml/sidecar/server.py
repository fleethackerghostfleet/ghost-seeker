"""Minimal Connect (unary, JSON codec) HTTP server: POST /<service>/<Method> with application/json in and out,
errors as HTTP status + {"code","message"} - exactly what connect-go's client with WithProtoJSON() speaks."""
from __future__ import annotations

import json
import logging
import threading
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from .service import InferenceService, RpcError

log = logging.getLogger("edgeml.sidecar")

MAX_BODY_BYTES = 32 << 20  # a 4K JPEG is ~2-3 MB base64; anything near this is not a frame
REQUEST_TIMEOUT_SECONDS = 10

# connect error code -> HTTP status (connect protocol spec)
STATUS = {"canceled": 499, "unknown": 500, "invalid_argument": 400, "deadline_exceeded": 504, "not_found": 404,
          "already_exists": 409, "permission_denied": 403, "resource_exhausted": 429, "failed_precondition": 412,
          "aborted": 409, "out_of_range": 400, "unimplemented": 404, "internal": 500, "unavailable": 503,
          "data_loss": 500, "unauthenticated": 401}


def make_handler(svc: InferenceService, quiet: bool = True):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"
        timeout = REQUEST_TIMEOUT_SECONDS

        def log_message(self, fmt, *args):  # noqa: N802 - stdlib name
            if not quiet:
                log.info(fmt, *args)

        def _send(self, status: int, body: dict):
            data = json.dumps(body, separators=(",", ":")).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            if self.close_connection:
                self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(data)

        def _error(self, code: str, message: str, http_status: int | None = None):
            self._send(http_status or STATUS.get(code, 500), {"code": code, "message": message})

        def do_GET(self):  # noqa: N802
            if self.path in ("/healthz", "/health"):
                return self._send(200, {"ok": True, **svc.engine.stats()})
            self._error("not_found", "use POST /ghostseeker.v1.InferenceService/<Method>")

        def do_POST(self):  # noqa: N802
            prefix = f"/{svc.SERVICE}/"
            if not self.path.startswith(prefix):
                self.close_connection = True
                return self._error("unimplemented", f"unknown service path {self.path}")
            ctype = self.headers.get("Content-Type", "")
            if not ctype.startswith("application/json"):
                self.close_connection = True
                return self._send(HTTPStatus.UNSUPPORTED_MEDIA_TYPE, {"code": "unknown", "message": f"unsupported content-type {ctype!r}; this sidecar speaks application/json (connect.WithProtoJSON)"})
            try:
                lengths = self.headers.get_all("Content-Length", [])
                if self.headers.get("Transfer-Encoding") is not None or len(lengths) != 1:
                    raise ValueError("a single Content-Length is required; Transfer-Encoding is unsupported")
                length = lengths[0].strip(" \t")
                if not length.isascii() or not length.isdecimal():
                    raise ValueError("Content-Length must be a nonnegative integer")
                n = int(length)
                if n > MAX_BODY_BYTES:
                    self.close_connection = True
                    return self._send(HTTPStatus.REQUEST_ENTITY_TOO_LARGE, {"code": "resource_exhausted", "message": f"body {n} bytes exceeds {MAX_BODY_BYTES}"})
                try:
                    raw = self.rfile.read(n)
                except TimeoutError:
                    self.close_connection = True
                    return self._error("deadline_exceeded", "request body timed out", HTTPStatus.REQUEST_TIMEOUT)
                if len(raw) != n:
                    raise ValueError("incomplete request body")
                req = json.loads(raw or b"{}")
                if not isinstance(req, dict):
                    raise ValueError("expected a JSON object")
                resp = svc.dispatch(self.path[len(prefix):], req)
            except RpcError as e:
                return self._error(e.code, str(e), getattr(e, "http_status", None))
            except (ValueError, KeyError) as e:
                self.close_connection = True
                return self._error("invalid_argument", f"{type(e).__name__}: {e}")
            except Exception:  # noqa: BLE001 - surface as Connect internal error
                log.exception("rpc failed")
                return self._error("internal", "inference request failed")
            self._send(200, resp)

    return Handler


def parse_listen(listen: str) -> tuple[str, int]:
    """':9090' binds loopback (the edge runs beside the sidecar and the service is unauthenticated);
    pass '0.0.0.0:9090' explicitly to expose it."""
    host, _, port = listen.rpartition(":")
    return (host or "127.0.0.1", int(port or 9090))


def make_server(engine, listen: str = ":9090", quiet: bool = True, prioritizer=None, mode: str = "edgeml") -> ThreadingHTTPServer:
    svc = InferenceService(engine, prioritizer=prioritizer, mode=mode)
    srv = ThreadingHTTPServer(parse_listen(listen), make_handler(svc, quiet=quiet))
    srv.daemon_threads = True
    return srv


def serve(engine, listen: str = ":9090", quiet: bool = False, prioritizer=None, mode: str = "edgeml") -> None:
    srv = make_server(engine, listen, quiet=quiet, prioritizer=prioritizer, mode=mode)
    log.info("sidecar listening on %s (%s, prioritizer=%s)", listen, getattr(engine, "model_id", "?"), mode)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        srv.server_close()


def start_in_thread(engine, listen: str = "127.0.0.1:0", **kw) -> tuple[ThreadingHTTPServer, str]:
    """For tests: serve on an ephemeral port, return (server, base_url)."""
    srv = make_server(engine, listen, **kw)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    host, port = srv.server_address[:2]
    return srv, f"http://{host}:{port}"
