"""Synthetic monitor used by --demo, screenshots, and tests."""

from __future__ import annotations

import math
import random
import time

from .config import Config
from .monitor import Monitor
from .snapshot import EngineState, OriginRow, RequestEvent, Snapshot

DEMO_ORIGINS = (
    ("soa", "son-of-anton", "10.0.0.4", "e-desktop"),
    ("opencode", "opencode", "10.0.0.4", "e-desktop"),
    ("open-webui", "open-webui", "10.0.0.6", "oracle"),
    ("friend", "unknown", "203.0.113.77", ""),
)

DEMO_MODELS = ("qwen3.8-27b", "qwen3.8-flash-next")


class DemoMonitor(Monitor):
    def __init__(self, config: Config) -> None:
        self.config = config
        self._tick = 0
        self._feed: list[RequestEvent] = []

    def snapshot(self, force: bool = False) -> Snapshot:
        self._tick += 1
        now = time.time()
        engines = [self._engine(engine.id, engine.name, index) for index, engine in enumerate(self.config.engines)]
        origins = self._origins(now)
        self._push_feed(now)
        return Snapshot(
            ts=now,
            engines=engines,
            origins=origins,
            feed=list(reversed(self._feed[-60:])),
            gateway_ok=True,
            gateway_error="",
            journal_status="live (demo)",
            total_requests=sum(row.requests for row in origins),
            total_prompt_tokens=sum(row.prompt_tokens for row in origins),
            total_completion_tokens=sum(row.completion_tokens for row in origins),
            inflight=sum(row.inflight for row in origins),
            requests_per_minute=sum(row.requests for row in origins) / self.config.origin_window_seconds * 60.0,
        )

    def _engine(self, engine_id: str, name: str, index: int) -> EngineState:
        phase = self._tick / 4.0 + index
        decode = max(8.0, 42.0 + 18.0 * math.sin(phase) + random.uniform(-4.0, 4.0))
        prefill = max(40.0, 420.0 + 160.0 * math.sin(phase / 3.0) + random.uniform(-30.0, 30.0))
        decode_history = [
            max(4.0, 42.0 + 18.0 * math.sin(phase - step / 4.0))
            for step in range(40, 0, -1)
        ]
        prefill_history = [
            max(40.0, 420.0 + 160.0 * math.sin(phase / 3.0 - step / 12.0))
            for step in range(40, 0, -1)
        ]
        return EngineState(
            id=engine_id,
            name=name,
            kind="demo",
            provider=engine_id,
            url="demo://",
            ok=True,
            models=[DEMO_MODELS[index % len(DEMO_MODELS)]],
            running=random.randint(0, 3),
            waiting=random.randint(0, 1),
            kv_usage=min(0.95, 0.35 + 0.2 * math.sin(phase / 2.0)),
            context_limit=393216,
            prefill_now=prefill,
            decode_now=decode,
            prefill_headline=prefill * random.uniform(0.9, 1.05),
            decode_headline=decode * random.uniform(0.92, 1.04),
            ttft_ms=random.uniform(120.0, 900.0),
            spec_accept=random.uniform(0.5, 0.8),
            active_slots=1 if random.random() < 0.7 else 0,
            total_slots=2,
            prefill_history=prefill_history,
            decode_history=decode_history,
            last_update=time.time(),
        )

    def _origins(self, now: float) -> list[OriginRow]:
        rows: list[OriginRow] = []
        for index, (key, app, address, host) in enumerate(DEMO_ORIGINS):
            requests = random.randint(1, 40) + index * 3
            prompt = requests * random.randint(200, 1800)
            completion = requests * random.randint(40, 400)
            rows.append(
                OriginRow(
                    virtual_key=key,
                    app=app,
                    address=address,
                    host=host,
                    requests=requests,
                    inflight=1 if index == 0 and random.random() < 0.5 else 0,
                    failed=0,
                    prompt_tokens=prompt,
                    completion_tokens=completion,
                    prompt_tps=prompt / self.config.origin_window_seconds,
                    completion_tps=completion / self.config.origin_window_seconds,
                    avg_latency_ms=random.uniform(1500.0, 6000.0),
                    last_seen=now - random.uniform(0.0, 120.0),
                    models=[DEMO_MODELS[index % len(DEMO_MODELS)]],
                )
            )
        rows.sort(key=lambda row: row.requests, reverse=True)
        return rows

    def _push_feed(self, now: float) -> None:
        if random.random() < 0.8:
            key, app, address, host = random.choice(DEMO_ORIGINS)
            self._feed.append(
                RequestEvent(
                    request_id=f"demo-{self._tick}-{len(self._feed)}",
                    timestamp=now,
                    virtual_key=key,
                    app=app,
                    address=address,
                    host=host,
                    provider=random.choice(("vllm", "strix")),
                    model=random.choice(DEMO_MODELS),
                    object_type="chat.completion",
                    status="processing" if random.random() < 0.15 else "success",
                    stream=True,
                    latency_ms=random.uniform(400.0, 9000.0),
                    prompt_tokens=random.randint(100, 3000),
                    completion_tokens=random.randint(10, 500),
                )
            )
        self._feed = self._feed[-200:]
