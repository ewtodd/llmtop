"""Monitor composition: poll engines and the gateway, produce a Snapshot."""

from __future__ import annotations

import threading
import time
from concurrent.futures import ThreadPoolExecutor

from .config import Config
from .engines import EngineRunner
from .gateway import BifrostClient, GatewayError
from .origins import OriginTracker
from .snapshot import Snapshot

ENGINE_TIMEOUT = 1.5


class Monitor:
    def snapshot(self, force: bool = False) -> Snapshot:
        raise NotImplementedError

    def close(self) -> None:
        pass


class LiveMonitor(Monitor):
    def __init__(self, config: Config) -> None:
        self.config = config
        self.runners = [
            EngineRunner(engine, config.headline_interval_seconds, config.history_seconds)
            for engine in config.engines
            if engine.enabled
        ]
        self.pool = ThreadPoolExecutor(
            max_workers=max(len(self.runners), 1),
            thread_name_prefix="llmtop-engine",
        )
        self.client = BifrostClient(config.gateway)
        self.tracker = OriginTracker()
        self._last_gateway_poll = 0.0
        self._gateway_ok = False
        self._gateway_error = "not polled yet"
        self._lock = threading.Lock()

    def snapshot(self, force: bool = False) -> Snapshot:
        with self._lock:
            return self._snapshot_locked(force)

    def _snapshot_locked(self, force: bool) -> Snapshot:
        now = time.time()
        engines = self._poll_engines()
        if force or now - self._last_gateway_poll >= self.config.gateway_poll_interval:
            self._poll_gateway()
            self._last_gateway_poll = now

        rows, totals = self.tracker.aggregate(
            now,
            self.config.origin_window_seconds,
            include_providers=self.config.gateway.include_providers,
            exclude_providers=self.config.gateway.exclude_providers,
        )
        return Snapshot(
            ts=now,
            engines=engines,
            origins=rows,
            feed=self.tracker.feed(60),
            gateway_ok=self._gateway_ok,
            gateway_error=self._gateway_error,
            total_requests=totals.requests,
            total_prompt_tokens=totals.prompt_tokens,
            total_completion_tokens=totals.completion_tokens,
            inflight=totals.inflight,
            requests_per_minute=totals.requests_per_minute,
        )

    def _poll_engines(self):
        futures = [self.pool.submit(runner.poll, ENGINE_TIMEOUT) for runner in self.runners]
        return [future.result() for future in futures]

    def _poll_gateway(self) -> None:
        if not self.client.enabled:
            self._gateway_ok = False
            self._gateway_error = "disabled"
            return
        if not self.client.has_credentials:
            healthy, detail = self.client.health()
            self._gateway_ok = healthy
            self._gateway_error = (
                "admin credentials not set; origins need BIFROST_ADMIN_USERNAME/PASSWORD"
            )
            if not healthy:
                self._gateway_error = f"gateway {detail}; {self._gateway_error}"
            return
        try:
            rows = self.client.fetch_logs()
        except GatewayError as error:
            self._gateway_ok = False
            self._gateway_error = str(error)
            return
        self.tracker.ingest(rows)
        self._gateway_ok = True
        self._gateway_error = ""

    def close(self) -> None:
        self.pool.shutdown(wait=False, cancel_futures=True)
