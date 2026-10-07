import json

import pytest

from llmtop.config import GatewayConfig
from llmtop.gateway import BifrostClient, GatewayError, _parse_journal_line


def test_parse_journal_line():
    line = json.dumps(
        {
            "level": "info",
            "http.method": "POST",
            "http.target": "/v1/chat/completions",
            "http.status_code": 200,
            "http.request_duration_ms": 1234,
            "http.remote_addr": "10.0.0.4:50772",
            "http.user_agent": "python-httpx2/2.7.0",
            "trace_id": "abc",
            "request_id": "req-42",
            "time": "2026-10-07T12:23:40-05:00",
            "message": "request completed",
        }
    )
    entry = _parse_journal_line(line)
    assert entry is not None
    assert entry.request_id == "req-42"
    assert entry.address == "10.0.0.4"
    assert entry.target == "/v1/chat/completions"
    assert entry.status_code == 200


def test_parse_journal_line_ignores_other_messages():
    assert _parse_journal_line(json.dumps({ "message": "starting server" })) is None
    assert _parse_journal_line("not json") is None


def test_parse_journal_line_ipv6():
    line = json.dumps(
        {
            "http.remote_addr": "[::1]:5000",
            "request_id": "req-6",
            "message": "request completed",
        }
    )
    entry = _parse_journal_line(line)
    assert entry is not None
    assert entry.address == "::1"


class FakeResponse:
    def __init__(self, payload: bytes):
        self.payload = payload

    def read(self) -> bytes:
        return self.payload

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False


def test_fetch_logs_requires_credentials():
    client = BifrostClient(GatewayConfig(username="", password=""))
    with pytest.raises(GatewayError, match="credentials"):
        client.fetch_logs()


def test_fetch_logs_parses_response(monkeypatch: pytest.MonkeyPatch):
    calls = {}

    def fake_urlopen(request, timeout):
        calls["url"] = request.full_url
        calls["auth"] = request.headers.get("Authorization")
        return FakeResponse(json.dumps({ "logs": [ { "id": "a" } ], "pagination": {} }).encode())

    monkeypatch.setattr("urllib.request.urlopen", fake_urlopen)
    client = BifrostClient(GatewayConfig(username="admin", password="pw", log_limit=5))
    logs = client.fetch_logs()
    assert logs == [ { "id": "a" } ]
    assert "limit=5" in calls["url"]
    assert calls["auth"].startswith("Basic ")


def test_health_is_unauthenticated(monkeypatch: pytest.MonkeyPatch):
    def fake_urlopen(request, timeout):
        assert "Authorization" not in request.headers
        return FakeResponse(b"{}")

    monkeypatch.setattr("urllib.request.urlopen", fake_urlopen)
    client = BifrostClient(GatewayConfig(username="", password=""))
    healthy, detail = client.health()
    assert healthy is True
    assert detail == "up"
