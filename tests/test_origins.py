import time

from llmtop.gateway import JournalEntry
from llmtop.origins import OriginTracker, apply_journal, parse_log_row


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


class FakeJournal:
    def __init__(self, entries):
        self.entries = entries

    def lookup(self, request_id):
        return self.entries.get(request_id)

    def lookup_by_features(self, user_agent, timestamp, tolerance=20.0):
        return None


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


def test_parse_log_row_reads_forwarded_address():
    event = parse_log_row(_row(metadata={ "x-forwarded-for": "203.0.113.9, 10.0.0.7" }))
    assert event is not None
    assert event.address == "203.0.113.9"


def test_metadata_address_wins_over_journal():
    class WrongJournal:
        def lookup(self, request_id):
            return JournalEntry(
                request_id=request_id,
                timestamp=time.time(),
                address="10.0.0.7",
                target="",
                user_agent="",
                status_code=200,
                duration_ms=1.0,
            )

        def lookup_by_features(self, user_agent, timestamp, tolerance=20.0):
            return None

    tracker = OriginTracker()
    now = time.time()
    tracker.ingest(
        [ _row("a", timestamp=_iso(now), metadata={ "x-real-ip": "203.0.113.9" }) ],
        WrongJournal(),
        { "203.0.113.9": "friend" },
    )
    origins, _ = tracker.aggregate(time.time(), 300.0)
    assert origins[0].address == "203.0.113.9"
    assert origins[0].host == "friend"


def test_parse_nested_virtual_key_and_missing_usage():
    event = parse_log_row(_row(virtual_key_name=None, virtual_key={ "name": "friend" }, token_usage=None))
    assert event is not None
    assert event.virtual_key == "friend"
    assert event.prompt_tokens == 0


def test_apply_journal_sets_machine():
    event = parse_log_row(_row())
    assert event is not None
    entry = JournalEntry(
        request_id="req-1",
        timestamp=time.time(),
        address="10.0.0.4",
        target="/v1/chat/completions",
        user_agent="",
        status_code=200,
        duration_ms=5.0,
    )
    apply_journal(event, entry, { "10.0.0.4": "e-desktop" })
    assert event.address == "10.0.0.4"
    assert event.host == "e-desktop"


def test_tracker_dedupes_updates_and_filters():
    tracker = OriginTracker()
    journal = FakeJournal({})
    now = time.time()
    rows = [
        _row("a", timestamp=_iso(now), status="processing", token_usage={}),
        _row("b", timestamp=_iso(now), provider="embed"),
    ]
    tracker.ingest(rows, journal, {})
    tracker.ingest([_row("a", timestamp=_iso(now), status="success")], journal, {})
    origins, totals = tracker.aggregate(now, 300.0, exclude_providers=[ "embed" ])
    assert len(origins) == 1
    assert origins[0].requests == 1
    assert origins[0].prompt_tokens == 100
    assert totals.requests == 1
    assert totals.completion_tokens == 50


def test_tracker_window_and_journal_join():
    tracker = OriginTracker()
    old = time.time() - 600.0
    fresh = time.time() - 5.0
    journal = FakeJournal(
        {
            "fresh": JournalEntry(
                request_id="fresh",
                timestamp=fresh,
                address="10.0.0.4",
                target="",
                user_agent="",
                status_code=200,
                duration_ms=1.0,
            )
        }
    )
    tracker.ingest(
        [
            _row("old", timestamp=_iso(old)),
            _row("fresh", timestamp=_iso(fresh)),
        ],
        journal,
        { "10.0.0.4": "e-desktop" },
    )
    origins, totals = tracker.aggregate(time.time(), 300.0)
    assert totals.requests == 1
    assert origins[0].host == "e-desktop"
    assert origins[0].address == "10.0.0.4"


def test_app_label_falls_back_to_user_agent():
    tracker = OriginTracker()
    now = time.time()
    tracker.ingest(
        [ _row("a", timestamp=_iso(now), app="other", user_agent="python-httpx2/2.7.0") ],
        None,
        {},
    )
    origins, _ = tracker.aggregate(time.time(), 300.0)
    assert origins[0].app == "python-httpx2"


def test_app_label_keeps_known_app():
    tracker = OriginTracker()
    now = time.time()
    tracker.ingest(
        [ _row("a", timestamp=_iso(now), app="opencode", user_agent="opencode/0.1") ],
        None,
        {},
    )
    origins, _ = tracker.aggregate(time.time(), 300.0)
    assert origins[0].app == "opencode"


def test_journal_feature_fallback_fills_machine():
    class FallbackJournal:
        def lookup(self, request_id):
            return None

        def lookup_by_features(self, user_agent, timestamp, tolerance=20.0):
            return JournalEntry(
                request_id="other-id",
                timestamp=timestamp,
                address="10.0.0.4",
                target="",
                user_agent=user_agent,
                status_code=200,
                duration_ms=1.0,
            )

    tracker = OriginTracker()
    now = time.time()
    tracker.ingest([ _row("a", timestamp=_iso(now)) ], FallbackJournal(), { "10.0.0.4": "e-desktop" })
    origins, _ = tracker.aggregate(time.time(), 300.0)
    assert origins[0].address == "10.0.0.4"
    assert origins[0].host == "e-desktop"


def test_totals_rates():
    tracker = OriginTracker()
    now = time.time()
    tracker.ingest([_row("a", timestamp=_iso(now)), _row("b", timestamp=_iso(now))], None, {})
    _, totals = tracker.aggregate(now, 60.0)
    assert totals.requests == 2
    assert totals.requests_per_minute == 2.0
    assert totals.completion_tps == 100.0 / 60.0


def _iso(timestamp: float) -> str:
    from datetime import datetime, timezone

    return datetime.fromtimestamp(timestamp, timezone.utc).isoformat()
