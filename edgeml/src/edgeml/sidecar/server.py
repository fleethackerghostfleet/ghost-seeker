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

# connect error code -> HTTP status (connect protocol spec)
STATUS = {"canceled": 499, "unknown": 500, "invalid_argument": 400, "deadline_exceeded": 504, "not_found": 404,
          "already_exists": 409, "permission_denied": 403, "resource_exhausted": 429, "failed_precondition": 412,
          "aborted": 409, "out_of_range": 400, "unimplemented": 404, "internal": 500, "unavailable": 503,
          "data_loss": 500, "unauthenticated": 401}


def make_handler(svc: InferenceService, quiet: bool = True):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, fmt, *args):  # noqa: N802 - stdlib name
            if not quiet:
                log.info(fmt, *args)

        def _send(self, status: int, body: dict):
            data = json.dumps(body, separators=(",", ":")).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def _error(self, code: str, message: str):
            self._send(STATUS.get(code, 500), {"code": code, "message": message})

        def do_GET(self):  # noqa: N802
            if self.path in ("/healthz", "/health"):
                return self._send(200, {"ok": True, **svc.engine.stats()})
            self._error("not_found", "use POST /ghostseeker.v1.InferenceService/<Method>")

        def do_POST(self):  # noqa: N802
            prefix = f"/{svc.SERVICE}/"
            if not self.path.startswith(prefix):
                return self._error("unimplemented", f"unknown service path {self.path}")
            ctype = self.headers.get("Content-Type", "")
            if not ctype.startswith("application/json"):
                return self._send(HTTPStatus.UNSUPPORTED_MEDIA_TYPE, {"code": "unknown", "message": f"unsupported content-type {ctype!r}; this sidecar speaks application/json (connect.WithProtoJSON)"})
            try:
                n = int(self.headers.get("Content-Length", "0"))
                if n < 0 or n > MAX_BODY_BYTES:
                    self.close_connection = True
                    return self._send(HTTPStatus.REQUEST_ENTITY_TOO_LARGE, {"code": "resource_exhausted", "message": f"body {n} bytes exceeds {MAX_BODY_BYTES}"})
                req = json.loads(self.rfile.read(n) or b"{}")
                resp = svc.dispatch(self.path[len(prefix):], req)
            except RpcError as e:
                return self._error(e.code, str(e))
            except (ValueError, KeyError) as e:
                return self._error("invalid_argument", f"{type(e).__name__}: {e}")
            except Exception as e:  # noqa: BLE001 - surface as Connect internal error
                log.exception("rpc failed")
                return self._error("internal", f"{type(e).__name__}: {e}")
            self._send(200, resp)

    return Handler


def parse_listen(listen: str) -> tuple[str, int]:
    """':9090' binds loopback (the edge runs beside the sidecar and the service is unauthenticated);
    pass '0.0.0.0:9090' explicitly to expose it."""
    host, _, port = listen.rpartition(":")
    return (host or "127.0.0.1", int(port or 9090))


def make_server(engine, listen: str = ":9090", quiet: bool = True) -> ThreadingHTTPServer:
    srv = ThreadingHTTPServer(parse_listen(listen), make_handler(InferenceService(engine), quiet=quiet))
    srv.daemon_threads = True
    return srv


def serve(engine, listen: str = ":9090", quiet: bool = False) -> None:
    srv = make_server(engine, listen, quiet=quiet)
    log.info("sidecar listening on %s (%s)", listen, getattr(engine, "model_id", "?"))
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        srv.server_close()


def start_in_thread(engine, listen: str = "127.0.0.1:0") -> tuple[ThreadingHTTPServer, str]:
    """For tests: serve on an ephemeral port, return (server, base_url)."""
    srv = make_server(engine, listen)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    host, port = srv.server_address[:2]
    return srv, f"http://{host}:{port}"
