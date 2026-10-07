import time

import pytest

from llmtop.origins import OriginTracker, parse_log_row


def _row(request_id="req-1", timestamp=None, **overrides):
    row = {
        "id": request_id,
        "timestamp": timestamp or "2026-10-07T12:00:00+00:00",
        "provider": "vllm",
        "model": "qwen3.8-27b",
        "virtual_key_name": "soa",
        "app": "son-of-anton",
        "user_agent": "python-httpx2/2.7.0",
        "status": "success",
        "stream": True,
        "latency": 1234.5,
        "token_usage": { "prompt_tokens": 100, "completion_tokens": 50 },
        "object": "chat.completion",
    }
    row.update(overrides)
    return row


def test_parse_log_row():
    event = parse_log_row(_row())
    assert event is not None
    assert event.request_id == "req-1"
    assert event.virtual_key == "soa"
    assert event.app == "son-of-anton"
    assert event.prompt_tokens == 100
    assert event.completion_tokens == 50
    assert event.latency_ms == 1234.5
    assert event.inflight is False


def test_parse_nested_virtual_key_and_missing_usage():
    event = parse_log_row(_row(virtual_key_name=None, virtual_key={ "name": "friend" }, token_usage=None))
    assert event is not None
    assert event.virtual_key == "friend"
    assert event.prompt_tokens == 0


def test_tracker_dedupes_updates_and_filters():
    tracker = OriginTracker()
    now = time.time()
    rows = [
        _row("a", timestamp=_iso(now), status="processing", token_usage={}),
        _row("b", timestamp=_iso(now), provider="embed"),
    ]
    tracker.ingest(rows)
    tracker.ingest([ _row("a", timestamp=_iso(now), status="success") ])
    origins, totals = tracker.aggregate(now, 300.0, exclude_providers=[ "embed" ])
    assert len(origins) == 1
    assert origins[0].requests == 1
    assert origins[0].prompt_tokens == 100
    assert totals.requests == 1
    assert totals.completion_tokens == 50


def test_tracker_window_filters_old_requests():
    tracker = OriginTracker()
    old = time.time() - 600.0
    fresh = time.time() - 5.0
    tracker.ingest(
        [
            _row("old", timestamp=_iso(old)),
            _row("fresh", timestamp=_iso(fresh)),
        ]
    )
    origins, totals = tracker.aggregate(time.time(), 300.0)
    assert totals.requests == 1
    assert origins[0].last_seen == pytest.approx(fresh)


def test_app_label_falls_back_to_user_agent():
    tracker = OriginTracker()
    now = time.time()
    tracker.ingest(
        [ _row("a", timestamp=_iso(now), app="other", user_agent="python-httpx2/2.7.0") ]
    )
    origins, _ = tracker.aggregate(time.time(), 300.0)
    assert origins[0].app == "python-httpx2"


def test_app_label_keeps_known_app():
    tracker = OriginTracker()
    now = time.time()
    tracker.ingest(
        [ _row("a", timestamp=_iso(now), app="opencode", user_agent="opencode/0.1") ]
    )
    origins, _ = tracker.aggregate(time.time(), 300.0)
    assert origins[0].app == "opencode"


def test_totals_rates():
    tracker = OriginTracker()
    now = time.time()
    tracker.ingest([ _row("a", timestamp=_iso(now)), _row("b", timestamp=_iso(now)) ])
    _, totals = tracker.aggregate(now, 60.0)
    assert totals.requests == 2
    assert totals.requests_per_minute == 2.0
    assert totals.completion_tps == 100.0 / 60.0


def _iso(timestamp: float) -> str:
    from datetime import datetime, timezone

    return datetime.fromtimestamp(timestamp, timezone.utc).isoformat()
