"""Turn Bifrost request-log rows into origin rows for the TUI.

The monitored identity is the virtual key: every consumer gets its own key,
so the key names the origin regardless of where the backend or browser runs.
"""

from __future__ import annotations

import time
from collections import OrderedDict
from dataclasses import dataclass
from datetime import datetime

from .snapshot import OriginRow, RequestEvent

EVENT_CAPACITY = 8000


def parse_log_row(row: dict) -> RequestEvent | None:
    request_id = str(row.get("id") or "")
    if not request_id:
        return None
    usage = row.get("token_usage") or {}
    if not isinstance(usage, dict):
        usage = {}
    return RequestEvent(
        request_id=request_id,
        timestamp=_row_timestamp(row.get("timestamp")),
        virtual_key=_virtual_key_name(row),
        app=str(row.get("app") or ""),
        user_agent=str(row.get("user_agent") or ""),
        provider=str(row.get("provider") or ""),
        model=str(row.get("model") or ""),
        object_type=str(row.get("object") or ""),
        status=str(row.get("status") or ""),
        stream=bool(row.get("stream")),
        latency_ms=_optional_float(row.get("latency")),
        prompt_tokens=_int(usage.get("prompt_tokens")),
        completion_tokens=_int(usage.get("completion_tokens")),
        content_summary=str(row.get("content_summary") or ""),
    )


@dataclass
class Totals:
    requests: int = 0
    inflight: int = 0
    failed: int = 0
    prompt_tokens: int = 0
    completion_tokens: int = 0
    requests_per_minute: float = 0.0
    completion_tps: float = 0.0


class OriginTracker:
    def __init__(self, capacity: int = EVENT_CAPACITY) -> None:
        self.capacity = capacity
        self._events: OrderedDict[str, RequestEvent] = OrderedDict()

    def ingest(self, rows: list[dict]) -> int:
        added = 0
        for row in rows:
            event = parse_log_row(row)
            if event is None:
                continue
            if event.request_id not in self._events:
                added += 1
            self._events[event.request_id] = event
            self._events.move_to_end(event.request_id)
            while len(self._events) > self.capacity:
                self._events.popitem(last=False)
        return added

    def events_since(self, now: float, window: float) -> list[RequestEvent]:
        cutoff = now - window
        return [event for event in self._events.values() if event.timestamp >= cutoff]

    def feed(self, limit: int = 40) -> list[RequestEvent]:
        ordered = sorted(self._events.values(), key=lambda item: item.timestamp, reverse=True)
        return ordered[:limit]

    def aggregate(
        self,
        now: float,
        window: float,
        include_providers: list[str] | None = None,
        exclude_providers: list[str] | None = None,
    ) -> tuple[list[OriginRow], Totals]:
        include = set(include_providers or [])
        exclude = set(exclude_providers or [])
        rows: dict[tuple[str, str], OriginRow] = {}
        totals = Totals()
        latency_sums: dict[tuple[str, str], float] = {}
        latency_counts: dict[tuple[str, str], int] = {}

        for event in self.events_since(now, window):
            if include and event.provider not in include:
                continue
            if event.provider in exclude:
                continue
            key = (event.virtual_key, _app_label(event))
            row = rows.get(key)
            if row is None:
                row = OriginRow(virtual_key=event.virtual_key, app=_app_label(event))
                rows[key] = row
            row.requests += 1
            row.prompt_tokens += event.prompt_tokens
            row.completion_tokens += event.completion_tokens
            row.last_seen = max(row.last_seen, event.timestamp)
            if event.inflight:
                row.inflight += 1
                totals.inflight += 1
            if event.failed:
                row.failed += 1
            if event.model and event.model not in row.models:
                row.models.append(event.model)
            if event.latency_ms is not None and not event.inflight:
                latency_sums[key] = latency_sums.get(key, 0.0) + event.latency_ms
                latency_counts[key] = latency_counts.get(key, 0) + 1

            totals.requests += 1
            totals.prompt_tokens += event.prompt_tokens
            totals.completion_tokens += event.completion_tokens
            if event.failed:
                totals.failed += 1

        for key, row in rows.items():
            count = latency_counts.get(key, 0)
            if count:
                row.avg_latency_ms = latency_sums[key] / count
            if window > 0:
                row.prompt_tps = row.prompt_tokens / window
                row.completion_tps = row.completion_tokens / window
                totals.requests_per_minute = totals.requests / window * 60.0
                totals.completion_tps = totals.completion_tokens / window

        aggregated = sorted(
            rows.values(),
            key=lambda row: (row.requests, row.completion_tokens),
            reverse=True,
        )
        return aggregated, totals


def _virtual_key_name(row: dict) -> str:
    name = row.get("virtual_key_name")
    if name:
        return str(name)
    nested = row.get("virtual_key")
    if isinstance(nested, dict) and nested.get("name"):
        return str(nested["name"])
    return "anonymous"


OTHER_APP_LABELS = ("", "other", "unknown")


def _app_label(event: RequestEvent) -> str:
    if event.app and event.app.lower() not in OTHER_APP_LABELS:
        return event.app
    # Bifrost labels unrecognized user agents "other"; the UA product still
    # tells the admin which client it was.
    product = _user_agent_product(event.user_agent)
    if product:
        return product
    return "unknown"


def _user_agent_product(user_agent: str) -> str:
    text = user_agent.strip()
    if not text:
        return ""
    token = text.split()[0].split("/")[0].strip()
    return token[:32]


def _row_timestamp(raw: object) -> float:
    if isinstance(raw, str) and raw:
        try:
            return datetime.fromisoformat(raw).timestamp()
        except ValueError:
            pass
    if isinstance(raw, (int, float)):
        return float(raw)
    return time.time()


def _optional_float(raw: object) -> float | None:
    if raw is None:
        return None
    try:
        return float(raw)
    except (TypeError, ValueError):
        return None


def _int(raw: object) -> int:
    try:
        return int(raw or 0)
    except (TypeError, ValueError):
        return 0
