import json

import pytest

from llmtop import engines
from llmtop.config import EngineConfig
from llmtop.engines import EngineReading, EngineRunner


def _reading(ts, prompt=0.0, compute=0.0, output=0.0, psec=0.0, osec=0.0, draft=None, accepted=None):
    return EngineReading(
        ts=ts,
        ok=True,
        prefill_tokens=prompt,
        prefill_compute_tokens=compute,
        output_tokens=output,
        prefill_seconds=psec,
        output_seconds=osec,
        spec_draft_tokens=draft,
        spec_accepted_tokens=accepted,
    )


def _runner(monkeypatch, readings, headline_interval=10.0):
    iterator = iter(readings)
    monkeypatch.setattr(engines, "fetch_reading", lambda config, timeout=1.5: next(iterator))
    config = EngineConfig(id="test", name="Test", kind="vllm", url="http://test")
    return EngineRunner(config, headline_interval=headline_interval, history_seconds=60.0)


def test_instant_rates_and_headline_sampling(monkeypatch):
    runner = _runner(
        monkeypatch,
        [
            _reading(100.0),
            _reading(102.0, prompt=200, compute=200, output=40, psec=1.0, osec=1.0),
            _reading(106.0, prompt=400, compute=400, output=100, psec=2.0, osec=2.0),
            _reading(107.0, prompt=400, compute=400, output=100, psec=2.0, osec=2.0),
        ],
        headline_interval=3.0,
    )
    first = runner.poll()
    assert first.prefill_now == 0.0
    assert first.prefill_headline == 0.0

    second = runner.poll()
    assert second.prefill_now == pytest.approx(100.0)
    assert second.decode_now == pytest.approx(20.0)
    # 2s since the headline reference: not sampled yet.
    assert second.prefill_headline == 0.0
    assert second.decode_headline == 0.0

    third = runner.poll()
    assert third.prefill_now == pytest.approx(50.0)
    assert third.decode_now == pytest.approx(15.0)
    # Sampled at 100..106: compute-time rates, not wall-time averages.
    assert third.prefill_headline == pytest.approx(200.0)
    assert third.decode_headline == pytest.approx(50.0)

    fourth = runner.poll()
    assert fourth.prefill_headline == pytest.approx(200.0)
    assert fourth.decode_headline == pytest.approx(50.0)
    assert len(fourth.decode_history) == 4


def test_counter_reset_resets_headline(monkeypatch):
    runner = _runner(
        monkeypatch,
        [
            _reading(100.0, prompt=1000, compute=1000, output=500, psec=5.0, osec=5.0),
            _reading(102.0, prompt=500, compute=500, output=100, psec=1.0, osec=1.0),
        ],
    )
    runner.poll()
    state = runner.poll()
    assert state.prefill_now == 0.0
    assert state.decode_now == 0.0
    assert state.prefill_headline == 0.0


def test_spec_acceptance_is_sampled_per_headline_interval(monkeypatch):
    runner = _runner(
        monkeypatch,
        [
            _reading(100.0, draft=100, accepted=10),
            _reading(103.0, draft=400, accepted=80),
            _reading(106.0, draft=700, accepted=110),
        ],
        headline_interval=3.0,
    )
    runner.poll()
    middle = runner.poll()
    assert middle.spec_accept == pytest.approx((80 - 10) / (400 - 100))
    state = runner.poll()
    # Lifetime would be 110/700; the headline only counts the last 3s.
    assert state.spec_accept == pytest.approx((110 - 80) / (700 - 400))


def test_back_to_back_polls_do_not_spike(monkeypatch):
    runner = _runner(
        monkeypatch,
        [
            _reading(100.0),
            _reading(100.02, prompt=200, compute=200, output=40),
        ],
    )
    runner.poll()
    state = runner.poll()
    assert state.prefill_now == 0.0
    assert state.decode_now == 0.0


def test_non_finite_reading_marks_engine_down(monkeypatch):
    runner = _runner(monkeypatch, [ _reading(100.0, prompt=float("nan")) ])
    state = runner.poll()
    assert state.ok is False
    assert "non-finite" in state.error


def test_failed_poll_keeps_history(monkeypatch):
    runner = _runner(
        monkeypatch,
        [
            _reading(100.0, prompt=100, compute=100, output=10, psec=1.0, osec=1.0),
            _reading(102.0, prompt=300, compute=300, output=30, psec=2.0, osec=2.0),
            EngineReading(ts=104.0, ok=False, error="unreachable"),
        ],
    )
    runner.poll()
    runner.poll()
    failed = runner.poll()
    assert failed.ok is False
    assert failed.error == "unreachable"
    assert len(failed.decode_history) == 2


def test_fetch_vllm_maps_metrics(monkeypatch):
    text = """
vllm:prompt_tokens_total{model_name="m"} 1000
vllm:prompt_tokens_cached_total{model_name="m"} 600
vllm:generation_tokens_total{model_name="m"} 200
vllm:num_requests_running{model_name="m"} 3
vllm:num_requests_waiting{model_name="m"} 1
vllm:kv_cache_usage_perc{model_name="m"} 0.5
vllm:request_prefill_time_seconds_sum{model_name="m"} 10
vllm:request_decode_time_seconds_sum{model_name="m"} 20
vllm:time_to_first_token_seconds_sum{model_name="m"} 5
vllm:time_to_first_token_seconds_count{model_name="m"} 2
vllm:spec_decode_num_draft_tokens_total{model_name="m"} 100
vllm:spec_decode_num_accepted_tokens_total{model_name="m"} 60
"""
    monkeypatch.setattr(engines, "_http_get", lambda url, timeout: text.encode())
    config = EngineConfig(id="v", name="v", kind="vllm", url="http://v")
    reading = engines._fetch_vllm(config, now=1.0, timeout=1.0)
    assert reading.ok
    assert reading.prefill_tokens == 1000
    assert reading.prefill_compute_tokens == 400
    assert reading.output_tokens == 200
    assert reading.running == 3
    assert reading.waiting == 1
    assert reading.kv_usage == 0.5
    assert reading.prefill_seconds == 10
    assert reading.models == [ "m" ]


def test_fetch_vllm_model_filter(monkeypatch):
    text = """
vllm:prompt_tokens_total{model_name="wanted"} 100
vllm:prompt_tokens_total{model_name="other"} 900
"""
    monkeypatch.setattr(engines, "_http_get", lambda url, timeout: text.encode())
    config = EngineConfig(id="v", name="v", kind="vllm", url="http://v", models=[ "wanted" ])
    reading = engines._fetch_vllm(config, now=1.0, timeout=1.0)
    assert reading.prefill_tokens == 100


def test_fetch_llamacpp_maps_metrics_and_slots(monkeypatch):
    metrics = """
llamacpp:prompt_tokens_total 230290
llamacpp:prompt_seconds_total 199.9
llamacpp:tokens_predicted_total 22416
llamacpp:tokens_predicted_seconds_total 540.5
llamacpp:requests_processing 1
llamacpp:requests_deferred 0
llamacpp:kv_cache_usage_ratio 0.16
llamacpp:spec_decode_num_draft_tokens_total 19212
llamacpp:spec_decode_num_accepted_tokens_total 14823
gufo_device_lost_total 0
"""
    slots = [
        { "id": 0, "n_ctx": 393216, "is_processing": True, "state": 1, "model": "m" },
        { "id": 1, "n_ctx": 393216, "is_processing": False, "state": 0, "model": "m" },
    ]

    def fake_get(url, timeout):
        if url.endswith("/slots"):
            return json.dumps(slots).encode()
        return metrics.encode()

    monkeypatch.setattr(engines, "_http_get", fake_get)
    config = EngineConfig(id="l", name="l", kind="llamacpp", url="http://l")
    reading = engines._fetch_llamacpp(config, now=1.0, timeout=1.0)
    assert reading.ok
    assert reading.active_slots == 1
    assert reading.total_slots == 2
    assert reading.context_limit == 393216
    assert reading.models == [ "m" ]
    assert reading.spec_accepted_tokens == 14823
    assert reading.spec_draft_tokens == 19212
