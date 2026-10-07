"""Immutable data structures passed from the monitor to the TUI."""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class EngineState:
    id: str
    name: str
    kind: str
    provider: str
    url: str
    ok: bool = False
    error: str = ""
    models: list[str] = field(default_factory=list)
    running: int = 0
    waiting: int = 0
    kv_usage: float | None = None
    context_limit: int | None = None
    prefill_now: float = 0.0
    decode_now: float = 0.0
    prefill_headline: float = 0.0
    decode_headline: float = 0.0
    ttft_ms: float | None = None
    spec_accept: float | None = None
    active_slots: int = 0
    total_slots: int = 0
    device_lost: int = 0
    prefill_history: list[float] = field(default_factory=list)
    decode_history: list[float] = field(default_factory=list)
    last_update: float = 0.0


@dataclass
class RequestEvent:
    request_id: str
    timestamp: float
    virtual_key: str = "anonymous"
    app: str = ""
    user_agent: str = ""
    provider: str = ""
    model: str = ""
    object_type: str = ""
    status: str = ""
    stream: bool = False
    latency_ms: float | None = None
    prompt_tokens: int = 0
    completion_tokens: int = 0
    content_summary: str = ""

    @property
    def inflight(self) -> bool:
        return self.status == "processing"

    @property
    def failed(self) -> bool:
        return self.status not in ("processing", "success")


@dataclass
class OriginRow:
    virtual_key: str
    app: str
    requests: int = 0
    inflight: int = 0
    failed: int = 0
    prompt_tokens: int = 0
    completion_tokens: int = 0
    prompt_tps: float = 0.0
    completion_tps: float = 0.0
    avg_latency_ms: float | None = None
    last_seen: float = 0.0
    models: list[str] = field(default_factory=list)


@dataclass
class Snapshot:
    ts: float = 0.0
    engines: list[EngineState] = field(default_factory=list)
    origins: list[OriginRow] = field(default_factory=list)
    feed: list[RequestEvent] = field(default_factory=list)
    gateway_ok: bool = False
    gateway_error: str = ""
    total_requests: int = 0
    total_prompt_tokens: int = 0
    total_completion_tokens: int = 0
    inflight: int = 0
    requests_per_minute: float = 0.0
    paused: bool = False
