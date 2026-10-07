import json

import pytest

from llmtop.config import GatewayConfig
from llmtop.gateway import BifrostClient, GatewayError


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
