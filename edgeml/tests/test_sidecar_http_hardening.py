import http.client
import json
import socket
import threading
import time
from contextlib import closing
from types import SimpleNamespace

import pytest

from edgeml.sidecar import server

PATH = "/ghostseeker.v1.InferenceService/Capabilities"


@pytest.fixture
def listener(monkeypatch):
    calls = []
    engine = SimpleNamespace(capabilities=lambda: calls.append(True) or {"modelId": "socket-test"}, stats=lambda: {})
    monkeypatch.setattr(server, "REQUEST_TIMEOUT_SECONDS", 0.05)
    srv = server.make_server(engine, "127.0.0.1:0")
    thread = threading.Thread(target=lambda: srv.serve_forever(poll_interval=0.01), daemon=True)
    thread.start()
    try:
        yield srv.server_address, calls
    finally:
        srv.shutdown()
        srv.server_close()
        thread.join(timeout=1)
        assert not thread.is_alive()


def request(headers, body=b"", path=PATH):
    return f"POST {path} HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\n{headers}\r\n".encode() + body


def exchange(address, data, *, eof=False, closed=False):
    with socket.create_connection(address, timeout=1) as connection:
        connection.sendall(data)
        if eof:
            connection.shutdown(socket.SHUT_WR)
        response = http.client.HTTPResponse(connection)
        response.begin()
        payload = response.read()
        if closed:
            assert response.getheader("Connection") == "close"
            assert connection.recv(1) == b""
        return response.status, json.loads(payload)


@pytest.mark.parametrize("headers", ["", "Content-Length: \r\n", "Content-Length: -1\r\n", "Content-Length: nope\r\n", "Content-Length: +2\r\n", "Content-Length: 2.0\r\n", "Content-Length: 2, 2\r\n", "Content-Length: 2\r\nContent-Length: 2\r\n", "Content-Length: 2\r\nTransfer-Encoding: chunked\r\n"])
def test_invalid_framing_is_400_and_closes_without_dispatch(listener, headers):
    address, calls = listener
    status, body = exchange(address, request(headers, b"{}"), closed=True)
    assert status == 400 and body["code"] == "invalid_argument" and calls == []


def test_overflow_is_413_without_waiting_for_body_or_dispatch(listener):
    address, calls = listener
    status, body = exchange(address, request(f"Content-Length: {server.MAX_BODY_BYTES + 1}\r\n"), closed=True)
    assert status == 413 and body["code"] == "resource_exhausted" and calls == []


def test_body_cap_is_inclusive(listener, monkeypatch):
    address, calls = listener
    monkeypatch.setattr(server, "MAX_BODY_BYTES", 64)
    raw = b"{}" + b" " * 62
    status, _ = exchange(address, request("Content-Length: 64\r\nConnection: close\r\n", raw))
    assert status == 200 and len(calls) == 1
    status, body = exchange(address, request("Content-Length: 65\r\n", raw + b" "), closed=True)
    assert status == 413 and body["code"] == "resource_exhausted" and len(calls) == 1


@pytest.mark.parametrize("raw", [b"{", b"{} trailing", b"{}{}", b"\xff", b"[]", b"null", b'"text"', b"1"])
def test_invalid_json_is_400_and_no_traceback(listener, raw):
    address, calls = listener
    status, body = exchange(address, request(f"Content-Length: {len(raw)}\r\n", raw), closed=True)
    assert status == 400 and body["code"] == "invalid_argument" and calls == []
    assert "Traceback" not in json.dumps(body)
    status, body = exchange(address, request("Content-Length: 2\r\nConnection: close\r\n", b"{}"))
    assert status == 200 and body["modelId"] == "socket-test" and len(calls) == 1


def test_truncated_valid_json_is_not_dispatched(listener):
    address, calls = listener
    status, body = exchange(address, request("Content-Length: 10\r\n", b"{}"), eof=True, closed=True)
    assert status == 400 and "incomplete" in body["message"] and calls == []


def test_idle_partial_headers_are_cut_off(listener):
    address, calls = listener
    with socket.create_connection(address, timeout=1) as connection:
        start = time.monotonic()
        connection.sendall(f"POST {PATH} HTTP/1.1\r\nHost: local".encode())
        assert connection.recv(1) == b""
        assert time.monotonic() - start < 1
    assert calls == []


def test_idle_partial_body_is_408_and_closes(listener):
    address, calls = listener
    start = time.monotonic()
    status, body = exchange(address, request("Content-Length: 10\r\n", b"{"), closed=True)
    assert status == 408 and body["code"] == "deadline_exceeded" and calls == []
    assert time.monotonic() - start < 1


@pytest.mark.parametrize("path,ctype,status", [("/wrong-service", "application/json", 404), (PATH, "application/proto", 415)])
def test_early_rejection_cannot_replay_unread_body(listener, path, ctype, status):
    address, calls = listener
    replay = request("Content-Length: 2\r\n", b"{}")
    first = request(f"Content-Length: {len(replay)}\r\n", replay, path).replace(b"Content-Type: application/json", f"Content-Type: {ctype}".encode(), 1)
    actual, _ = exchange(address, first, closed=True)
    assert actual == status and calls == []


def test_zero_length_capabilities_and_keepalive_contract(listener):
    address, calls = listener
    with closing(http.client.HTTPConnection(*address, timeout=1)) as client:
        for payload in (b"", b"{}"):
            client.request("POST", PATH, body=payload, headers={"Content-Type": "application/json"})
            response = client.getresponse()
            assert response.status == 200 and json.loads(response.read())["modelId"] == "socket-test"
        client.request("GET", "/healthz")
        response = client.getresponse()
        assert response.status == 200 and json.loads(response.read())["ok"]
    assert len(calls) == 2


@pytest.mark.parametrize("error", [RuntimeError, TimeoutError])
def test_internal_exception_details_are_not_exposed(error):
    def fail():
        raise error("private-model-path")

    srv = server.make_server(SimpleNamespace(capabilities=fail), "127.0.0.1:0")
    thread = threading.Thread(target=lambda: srv.serve_forever(poll_interval=0.01), daemon=True)
    thread.start()
    try:
        status, body = exchange(srv.server_address, request("Content-Length: 2\r\nConnection: close\r\n", b"{}"))
        assert status == 500 and body == {"code": "internal", "message": "inference request failed"}
    finally:
        srv.shutdown()
        srv.server_close()
        thread.join(timeout=1)
