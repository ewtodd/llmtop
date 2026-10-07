"""Engine scrapers and rate accounting.

Each engine is polled for a normalized reading; rates are derived from
monotonic counters between readings, and "recent request" averages come from
the per-request compute-time counters the engines expose.
"""

from __future__ import annotations

import json
import math
import time
import urllib.error
import urllib.request
from collections import deque
from dataclasses import dataclass, field

from .config import EngineConfig
from .prometheus import Sample, label_values, parse_prometheus, total, value
from .snapshot import EngineState

USER_AGENT = "llmtop/0.1"


@dataclass
class EngineReading:
    ts: float
    ok: bool
    error: str = ""
    models: list[str] = field(default_factory=list)
    prefill_tokens: float = 0.0
    prefill_compute_tokens: float = 0.0
    output_tokens: float = 0.0
    prefill_seconds: float = 0.0
    output_seconds: float = 0.0
    running: int = 0
    waiting: int = 0
    kv_usage: float | None = None
    context_limit: int | None = None
    ttft_seconds_sum: float | None = None
    ttft_count: float | None = None
    spec_draft_tokens: float | None = None
    spec_accepted_tokens: float | None = None
    active_slots: int = 0
    total_slots: int = 0
    device_lost: int = 0


def fetch_reading(engine: EngineConfig, timeout: float = 1.5) -> EngineReading:
    now = time.time()
    try:
        if engine.kind == "vllm":
            return _fetch_vllm(engine, now, timeout)
        if engine.kind == "llamacpp":
            return _fetch_llamacpp(engine, now, timeout)
        return _fetch_openai(engine, now, timeout)
    except urllib.error.HTTPError as error:
        return EngineReading(ts=now, ok=False, error=f"HTTP {error.code}")
    except urllib.error.URLError as error:
        return EngineReading(ts=now, ok=False, error=f"unreachable: {error.reason}")
    except TimeoutError:
        return EngineReading(ts=now, ok=False, error="timeout")
    except (ValueError, json.JSONDecodeError) as error:
        return EngineReading(ts=now, ok=False, error=f"bad payload: {error}")


def _http_get(url: str, timeout: float) -> bytes:
    request = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return response.read()


def _fetch_vllm(engine: EngineConfig, now: float, timeout: float) -> EngineReading:
    text = _http_get(f"{engine.url}/metrics", timeout).decode("utf-8", "replace")
    samples = parse_prometheus(text)
    samples = _filter_models(samples, engine.models, "model_name")

    prompt_tokens = total(samples, "vllm:prompt_tokens_total")
    cached_tokens = total(samples, "vllm:prompt_tokens_cached_total")
    generation_tokens = total(samples, "vllm:generation_tokens_total")
    models = label_values(samples, "vllm:prompt_tokens_total", "model_name")
    running = int(total(samples, "vllm:num_requests_running"))
    waiting = int(total(samples, "vllm:num_requests_waiting"))
    kv_values = _values(samples, "vllm:kv_cache_usage_perc")
    return EngineReading(
        ts=now,
        ok=True,
        models=models,
        prefill_tokens=prompt_tokens,
        prefill_compute_tokens=max(prompt_tokens - cached_tokens, 0.0),
        output_tokens=generation_tokens,
        prefill_seconds=total(samples, "vllm:request_prefill_time_seconds_sum"),
        output_seconds=total(samples, "vllm:request_decode_time_seconds_sum"),
        running=running,
        waiting=waiting,
        kv_usage=max(kv_values) if kv_values else None,
        ttft_seconds_sum=total(samples, "vllm:time_to_first_token_seconds_sum") or None,
        ttft_count=total(samples, "vllm:time_to_first_token_seconds_count") or None,
        spec_draft_tokens=total(samples, "vllm:spec_decode_num_draft_tokens_total") or None,
        spec_accepted_tokens=total(samples, "vllm:spec_decode_num_accepted_tokens_total") or None,
    )


def _fetch_llamacpp(engine: EngineConfig, now: float, timeout: float) -> EngineReading:
    text = _http_get(f"{engine.url}/metrics", timeout).decode("utf-8", "replace")
    samples = parse_prometheus(text)

    reading = EngineReading(
        ts=now,
        ok=True,
        prefill_tokens=total(samples, "llamacpp:prompt_tokens_total"),
        prefill_compute_tokens=total(samples, "llamacpp:prompt_tokens_total"),
        output_tokens=total(samples, "llamacpp:tokens_predicted_total"),
        prefill_seconds=total(samples, "llamacpp:prompt_seconds_total"),
        output_seconds=total(samples, "llamacpp:tokens_predicted_seconds_total"),
        running=int(total(samples, "llamacpp:requests_processing")),
        waiting=int(total(samples, "llamacpp:requests_deferred")),
        kv_usage=value(samples, "llamacpp:kv_cache_usage_ratio"),
        spec_draft_tokens=total(samples, "llamacpp:spec_decode_num_draft_tokens_total") or None,
        spec_accepted_tokens=total(samples, "llamacpp:spec_decode_num_accepted_tokens_total") or None,
        device_lost=int(total(samples, "gufo_device_lost_total")),
    )

    if engine.slots:
        slots = _fetch_slots(engine, timeout)
        if slots is not None:
            reading.active_slots = sum(
                1 for slot in slots if slot.get("is_processing") or slot.get("state", 0) != 0
            )
            reading.total_slots = len(slots)
            contexts = [slot.get("n_ctx") for slot in slots if isinstance(slot.get("n_ctx"), int)]
            reading.context_limit = max(contexts) if contexts else None
            reading.models = _unique(
                str(slot["model"]) for slot in slots if slot.get("model")
            )
    return reading


def _fetch_openai(engine: EngineConfig, now: float, timeout: float) -> EngineReading:
    _http_get(f"{engine.url}/health", timeout)
    return EngineReading(ts=now, ok=True)


def _fetch_slots(engine: EngineConfig, timeout: float) -> list[dict] | None:
    try:
        payload = json.loads(_http_get(f"{engine.url}/slots", timeout))
    except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError, ValueError):
        return None
    if isinstance(payload, list):
        return payload
    return None


def _filter_models(samples: list[Sample], models: list[str], label: str) -> list[Sample]:
    if not models:
        return samples
    allowed = set(models)
    return [
        sample
        for sample in samples
        if sample.labels.get(label) in allowed or label not in sample.labels
    ]


def _values(samples: list[Sample], name: str) -> list[float]:
    return [sample.value for sample in samples if sample.name == name]


def _unique(values) -> list[str]:
    seen: list[str] = []
    for item in values:
        if item not in seen:
            seen.append(item)
    return seen


class EngineRunner:
    """Keeps per-engine state between polls and turns readings into rates.

    Two cadences: ``_now`` rates are wall-clock deltas between consecutive
    polls (used by the live bars), while the headline numbers are sampled
    every ``headline_interval`` seconds and held in between, so the big
    number is an instantaneous rate over the last interval, not an average.
    """

    def __init__(self, config: EngineConfig, headline_interval: float, history_seconds: float) -> None:
        self.config = config
        self.headline_interval = headline_interval
        self.history_seconds = history_seconds
        self._last_reading: EngineReading | None = None
        self._headline_reading: EngineReading | None = None
        self._headline: dict[str, float | None] = self._empty_headline()
        self._prefill_history: deque[tuple[float, float]] = deque()
        self._decode_history: deque[tuple[float, float]] = deque()
        self._last_state = self._empty_state()

    def poll(self, timeout: float = 1.5) -> EngineState:
        reading = fetch_reading(self.config, timeout)
        if reading.ok and not _reading_is_finite(reading):
            reading.ok = False
            reading.error = "non-finite metrics"
        now = reading.ts
        if not reading.ok:
            state = self._empty_state()
            state.ok = False
            state.error = reading.error
            state.last_update = now
            state.prefill_history = [rate for _, rate in self._prefill_history]
            state.decode_history = [rate for _, rate in self._decode_history]
            self._last_state = state
            return state

        if self._counter_reset(reading):
            self._headline_reading = None
            self._headline = self._empty_headline()

        state = self._build_state(reading)
        self._record_history(reading.ts, state)
        self._last_reading = reading
        self._last_state = state
        return state

    def _counter_reset(self, reading: EngineReading) -> bool:
        previous = self._last_reading
        if previous is None or not previous.ok:
            return False
        return (
            reading.prefill_tokens < previous.prefill_tokens
            or reading.output_tokens < previous.output_tokens
            or reading.prefill_seconds < previous.prefill_seconds
            or reading.output_seconds < previous.output_seconds
        )

    def _build_state(self, reading: EngineReading) -> EngineState:
        state = EngineState(
            id=self.config.id,
            name=self.config.name,
            kind=self.config.kind,
            provider=self.config.provider,
            url=self.config.url,
            ok=True,
            models=list(reading.models),
            running=reading.running,
            waiting=reading.waiting,
            kv_usage=reading.kv_usage,
            context_limit=reading.context_limit,
            active_slots=reading.active_slots,
            total_slots=reading.total_slots,
            device_lost=reading.device_lost,
            last_update=reading.ts,
        )

        previous = self._last_reading
        if previous is not None and previous.ok:
            elapsed = reading.ts - previous.ts
            # Back-to-back polls (forced refresh racing the timer) would divide
            # by a near-zero interval and produce absurd spikes.
            if elapsed >= 0.05:
                # Per-stream rates for the live bars, matching the big numbers:
                # real tokens (vLLM prompt cache hits are not work) over each
                # request's own processing time.
                state.prefill_now = _average_rate(
                    reading.prefill_compute_tokens - previous.prefill_compute_tokens,
                    reading.prefill_seconds - previous.prefill_seconds,
                    elapsed,
                )
                state.decode_now = _average_rate(
                    reading.output_tokens - previous.output_tokens,
                    reading.output_seconds - previous.output_seconds,
                    elapsed,
                )

        self._update_headline(reading)
        state.prefill_headline = self._headline["prefill"] or 0.0
        state.decode_headline = self._headline["decode"] or 0.0
        state.prefill_total = self._headline["prefill_total"] or 0.0
        state.decode_total = self._headline["decode_total"] or 0.0
        state.ttft_ms = self._headline["ttft_ms"]
        state.spec_accept = self._headline["spec_accept"]
        return state

    def _update_headline(self, reading: EngineReading) -> None:
        if self._headline_reading is None:
            self._headline_reading = reading
            return
        elapsed = reading.ts - self._headline_reading.ts
        if elapsed < self.headline_interval:
            return
        previous = self._headline_reading
        accept = _ratio(
            _delta(reading.spec_accepted_tokens, previous.spec_accepted_tokens),
            _delta(reading.spec_draft_tokens, previous.spec_draft_tokens),
        )
        if accept is None:
            accept = _ratio(reading.spec_accepted_tokens, reading.spec_draft_tokens)
        prefill_tokens = reading.prefill_compute_tokens - previous.prefill_compute_tokens
        decode_tokens = reading.output_tokens - previous.output_tokens
        self._headline = {
            # Headline (big numbers, bars): per-stream speed.
            "prefill": _average_rate(
                prefill_tokens,
                reading.prefill_seconds - previous.prefill_seconds,
                elapsed,
            ),
            "decode": _average_rate(
                decode_tokens,
                reading.output_seconds - previous.output_seconds,
                elapsed,
            ),
            # Aggregate across concurrent requests (all streams).
            "prefill_total": _wall_rate(prefill_tokens, elapsed),
            "decode_total": _wall_rate(decode_tokens, elapsed),
            "ttft_ms": _window_ttft_ms(previous, reading),
            "spec_accept": accept,
        }
        self._headline_reading = reading

    def _empty_headline(self) -> dict[str, float | None]:
        return {
            "prefill": 0.0,
            "decode": 0.0,
            "prefill_total": 0.0,
            "decode_total": 0.0,
            "ttft_ms": None,
            "spec_accept": None,
        }

    def _record_history(self, now: float, state: EngineState) -> None:
        cutoff = now - self.history_seconds
        self._prefill_history.append((now, state.prefill_now))
        self._decode_history.append((now, state.decode_now))
        while len(self._prefill_history) > 2 and self._prefill_history[0][0] < cutoff:
            self._prefill_history.popleft()
        while len(self._decode_history) > 2 and self._decode_history[0][0] < cutoff:
            self._decode_history.popleft()
        state.prefill_history = [rate for _, rate in self._prefill_history]
        state.decode_history = [rate for _, rate in self._decode_history]

    def _empty_state(self) -> EngineState:
        return EngineState(
            id=self.config.id,
            name=self.config.name,
            kind=self.config.kind,
            provider=self.config.provider,
            url=self.config.url,
        )


def _reading_is_finite(reading: EngineReading) -> bool:
    counters = (
        reading.prefill_tokens,
        reading.prefill_compute_tokens,
        reading.output_tokens,
        reading.prefill_seconds,
        reading.output_seconds,
    )
    optional = (
        reading.kv_usage,
        reading.ttft_seconds_sum,
        reading.ttft_count,
        reading.spec_draft_tokens,
        reading.spec_accepted_tokens,
    )
    if any(not math.isfinite(value) for value in counters):
        return False
    return all(value is None or math.isfinite(value) for value in optional)


def _wall_rate(tokens: float, wall_seconds: float) -> float:
    if not math.isfinite(tokens) or tokens < 0:
        return 0.0
    if not math.isfinite(wall_seconds) or wall_seconds <= 0:
        return 0.0
    return tokens / wall_seconds


def _average_rate(tokens: float, seconds: float, wall_seconds: float) -> float:
    if not math.isfinite(tokens) or tokens < 0:
        return 0.0
    if seconds > 0.05 and math.isfinite(seconds):
        return tokens / seconds
    if wall_seconds > 0 and math.isfinite(wall_seconds):
        return tokens / wall_seconds
    return 0.0


def _window_ttft_ms(first: EngineReading, last: EngineReading) -> float | None:
    if first.ttft_seconds_sum is None or first.ttft_count is None:
        return None
    if last.ttft_seconds_sum is None or last.ttft_count is None:
        return None
    requests = last.ttft_count - first.ttft_count
    seconds = last.ttft_seconds_sum - first.ttft_seconds_sum
    if requests <= 0 or seconds < 0:
        return None
    return seconds / requests * 1000.0


def _ratio(accepted: float | None, drafted: float | None) -> float | None:
    if accepted is None or drafted is None or drafted <= 0:
        return None
    return accepted / drafted


def _delta(current: float | None, previous: float | None) -> float | None:
    if current is None or previous is None:
        return None
    return current - previous
