"""Real loopback Connect JSON responses with a model-free fault-injecting engine."""
import http.client
import json
from types import SimpleNamespace
from unittest.mock import Mock

import pytest

from edgeml.sidecar import server
from edgeml.sidecar.service import MockEngine, RpcError


@pytest.fixture(scope="module")
def endpoint():
    engine = MockEngine()
    engine.embed_text = Mock(return_value=[])
    srv, _ = server.start_in_thread(engine, quiet=False)
    yield srv.server_address, engine
    srv.shutdown()
    srv.server_close()


def request(endpoint, method="POST", path="/ghostseeker.v1.InferenceService/EmbedText", body=b"{}", headers=None):
    address, _ = endpoint
    connection = http.client.HTTPConnection(*address, timeout=5)
    try:
        connection.request(method, path, body, headers or {"Content-Type": "application/json; charset=utf-8"})
        response = connection.getresponse()
        raw = response.read()
        assert response.getheader("Content-Type") == "application/json"
        assert int(response.getheader("Content-Length")) == len(raw)
        return response.status, json.loads(raw)
    finally:
        connection.close()


@pytest.mark.parametrize("code,status", [
    ("canceled", 499), ("unknown", 500), ("invalid_argument", 400), ("deadline_exceeded", 504),
    ("not_found", 404), ("already_exists", 409), ("permission_denied", 403), ("resource_exhausted", 429),
    ("failed_precondition", 412), ("aborted", 409), ("out_of_range", 400), ("unimplemented", 404),
    ("internal", 500), ("unavailable", 503), ("data_loss", 500), ("unauthenticated", 401), ("future_code", 500),
])
def test_rpc_errors_map_to_connect_status_and_json(endpoint, code, status):
    endpoint[1].embed_text.side_effect = RpcError(code, "fixture failure")
    assert request(endpoint) == (status, {"code": code, "message": "fixture failure"})


def test_rpc_http_override_and_unexpected_engine_exception(endpoint):
    endpoint[1].embed_text.side_effect = RpcError("resource_exhausted", "pixel cap", http_status=413)
    assert request(endpoint) == (413, {"code": "resource_exhausted", "message": "pixel cap"})
    endpoint[1].embed_text.side_effect = RuntimeError("backend offline")
    assert request(endpoint) == (500, {"code": "internal", "message": "inference request failed"})
    endpoint[1].embed_text.side_effect = KeyError("cache")
    status, error = request(endpoint)
    assert status == 400 and error["code"] == "invalid_argument" and error["message"].startswith("KeyError:")
    endpoint[1].embed_text.side_effect = None
    assert request(endpoint) == (200, {"embeddings": [], "modelId": "edgeml-mock-v1"})


@pytest.mark.parametrize("body,headers,prefix", [
    (b"{", {"Content-Type": "application/json"}, "JSONDecodeError:"),
    (b"{}", {"Content-Type": "application/json", "Content-Length": "invalid"}, "ValueError:"),
])
def test_bad_json_and_content_length_are_invalid_arguments(endpoint, body, headers, prefix):
    status, error = request(endpoint, body=body, headers=headers)
    assert status == 400 and error["code"] == "invalid_argument" and error["message"].startswith(prefix)


@pytest.mark.parametrize("length,status,code", [(-1, 400, "invalid_argument"),
                                               (server.MAX_BODY_BYTES + 1, 413, "resource_exhausted")])
def test_body_limits_reject_without_waiting_for_the_declared_body(endpoint, length, status, code):
    actual, error = request(endpoint, body=b"", headers={"Content-Type": "application/json", "Content-Length": str(length)})
    assert actual == status and error["code"] == code


def test_empty_body_health_routes_and_unknown_paths(endpoint):
    for path in ["/health", "/healthz"]:
        status, health = request(endpoint, "GET", path, b"")
        assert status == 200 and health == {"ok": True, "frames": 0, "ms_per_frame": 0.0, "sources": {}}
    status, error = request(endpoint, "GET", "/missing", b"")
    assert status == 404 and error["code"] == "not_found"
    assert request(endpoint, path="/ghostseeker.v1.InferenceService/Capabilities", body=b"")[0] == 200
    status, error = request(endpoint, path="/other.Service/EmbedText", body=b"")
    assert status == 404 and error["code"] == "unimplemented"
    status, error = request(endpoint, headers={"Content-Type": "application/proto"})
    assert status == 415 and error["code"] == "unknown"


@pytest.mark.parametrize("error", [KeyboardInterrupt(), RuntimeError("serve failed")])
def test_serve_always_closes_server(monkeypatch, error):
    srv = SimpleNamespace(serve_forever=Mock(side_effect=error), server_close=Mock())
    monkeypatch.setattr(server, "make_server", Mock(return_value=srv))
    if isinstance(error, KeyboardInterrupt):
        server.serve(MockEngine(), ":0")
    else:
        with pytest.raises(RuntimeError, match="serve failed"):
            server.serve(MockEngine(), ":0")
    srv.server_close.assert_called_once_with()
