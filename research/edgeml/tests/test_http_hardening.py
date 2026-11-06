import http.client
import json
import socket
import threading
import time
from http.server import HTTPServer
from types import SimpleNamespace

import numpy as np
import pytest

from edgeml_research.nl_priority import server


@pytest.fixture
def listener(monkeypatch):
    calls = []
    vocabulary = SimpleNamespace(
        model_id="socket-test", dimension=2, min_similarity=0.25, min_margin=0.02,
        encoder=SimpleNamespace(manifest={}, encode_images=lambda images: np.array([[1.0, 0.0] for _ in images])),
        baseline=SimpleNamespace(classify=lambda vectors: [{"subtype": "boat", "unknownness": 0.1, "max_sim": 0.9} for _ in vectors]),
        classify=lambda concepts, items: calls.append((concepts, items)) or {"matches": []},
        embed_text=lambda texts: np.array([[1.0, 0.0] for _ in texts]))
    monkeypatch.setattr(server, "REQUEST_TIMEOUT_SECONDS", 0.05)
    srv = HTTPServer(("127.0.0.1", 0), server.handler_for(vocabulary))
    thread = threading.Thread(target=lambda: srv.serve_forever(poll_interval=0.01), daemon=True)
    thread.start()
    try:
        yield srv.server_address, calls
    finally:
        srv.shutdown()
        srv.server_close()
        thread.join(timeout=1)
        assert not thread.is_alive()


def request(headers, body=b"", path="/classify"):
    return f"POST {path} HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\n{headers}\r\n".encode() + body


def exchange(address, data, *, eof=False):
    with socket.create_connection(address, timeout=1) as connection:
        connection.sendall(data)
        if eof:
            connection.shutdown(socket.SHUT_WR)
        response = http.client.HTTPResponse(connection)
        response.begin()
        payload = json.loads(response.read())
        assert connection.recv(1) == b""
        return response.status, payload


@pytest.mark.parametrize("headers", ["", "Content-Length: \r\n", "Content-Length: -1\r\n", "Content-Length: invalid\r\n", "Content-Length: +2\r\n", "Content-Length: 2.0\r\n", "Content-Length: 2, 2\r\n", "Content-Length: 2\r\nContent-Length: 2\r\n", "Content-Length: 2\r\nTransfer-Encoding: chunked\r\n", "Content-Length: 0\r\n"])
def test_invalid_framing_is_400_without_dispatch(listener, headers):
    address, calls = listener
    status, body = exchange(address, request(headers, b"{}"))
    assert status == 400 and "error" in body and calls == []


def test_oversize_is_413_without_reading_body(listener):
    address, calls = listener
    status, body = exchange(address, request(f"Content-Length: {server.MAX_BODY + 1}\r\n"))
    assert status == 413 and "error" in body and calls == []


def test_body_cap_is_inclusive(listener, monkeypatch):
    address, calls = listener
    monkeypatch.setattr(server, "MAX_BODY", 64)
    raw = b"{}" + b" " * 62
    status, _ = exchange(address, request("Content-Length: 64\r\n", raw))
    assert status == 200 and len(calls) == 1
    status, body = exchange(address, request("Content-Length: 65\r\n", raw + b" "))
    assert status == 413 and "error" in body and len(calls) == 1


@pytest.mark.parametrize("raw", [b"{", b"{} trailing", b"{}{}", b"\xff", b"[]", b"null", b'"text"', b"1"])
def test_invalid_json_returns_400_without_traceback_or_state_change(listener, raw):
    address, calls = listener
    status, body = exchange(address, request(f"Content-Length: {len(raw)}\r\n", raw))
    assert status == 400 and calls == [] and "Traceback" not in json.dumps(body)
    valid = b'{"concepts":[],"items":[]}'
    status, body = exchange(address, request(f"Content-Length: {len(valid)}\r\n", valid))
    assert status == 200 and body == {"matches": []} and calls == [([], [])]


def test_truncated_valid_json_is_400_without_dispatch(listener):
    address, calls = listener
    status, body = exchange(address, request("Content-Length: 10\r\n", b"{}"), eof=True)
    assert status == 400 and "incomplete" in body["error"] and calls == []


def test_partial_headers_are_cut_off_and_health_remains_available(listener):
    address, calls = listener
    with socket.create_connection(address, timeout=1) as connection:
        start = time.monotonic()
        connection.sendall(b"POST /classify HTTP/1.1\r\nHost: local")
        assert connection.recv(1) == b""
        assert time.monotonic() - start < 1
    status, body = exchange(address, b"GET /health HTTP/1.1\r\nHost: localhost\r\n\r\n")
    assert status == 200 and body["status"] == "ready" and calls == []


def test_partial_body_times_out_as_408_without_dispatch(listener):
    address, calls = listener
    start = time.monotonic()
    status, body = exchange(address, request("Content-Length: 10\r\n", b"{"))
    assert status == 408 and body == {"error": "request body timed out"} and calls == []
    assert time.monotonic() - start < 1


@pytest.mark.parametrize("path,payload,key", [("/embed", {"texts": ["boat"]}, "embeddings"), ("/image-embed", {"images": [{"id": "crop-1", "imageBase64": "test-only"}]}, "items")])
def test_embedding_contracts_are_unchanged(listener, path, payload, key):
    address, _ = listener
    raw = json.dumps(payload).encode()
    status, body = exchange(address, request(f"Content-Length: {len(raw)}\r\n", raw, path))
    assert status == 200 and body["modelId"] == "socket-test" and body["dimension"] == 2 and len(body[key]) == 1
    if key == "embeddings":
        assert body[key] == [[1.0, 0.0]]
    else:
        assert body[key][0]["id"] == "crop-1" and body[key][0]["embedding"] == [1.0, 0.0]


def test_backend_timeout_remains_generic_503_not_request_timeout():
    def fail(*args):
        raise TimeoutError("private-model-path")

    srv = HTTPServer(("127.0.0.1", 0), server.handler_for(SimpleNamespace(classify=fail)))
    thread = threading.Thread(target=lambda: srv.serve_forever(poll_interval=0.01), daemon=True)
    thread.start()
    try:
        status, body = exchange(srv.server_address, request("Content-Length: 2\r\n", b"{}"))
        assert status == 503 and body == {"error": "inference unavailable; active state was not changed"}
    finally:
        srv.shutdown()
        srv.server_close()
        thread.join(timeout=1)
        assert not thread.is_alive()
