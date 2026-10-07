from rich.console import Group

from llmtop.render import (
    BIG_ROWS,
    big_number_lines,
    engine_card_renderable,
    format_age,
    format_ms,
    format_rate,
    format_tokens,
    headline_number,
    meter_line,
    nice_max,
)
from llmtop.snapshot import EngineState


def _state(**overrides) -> EngineState:
    state = EngineState(
        id="x",
        name="X",
        kind="vllm",
        provider="vllm",
        url="http://x",
        ok=True,
        models=[ "m" ],
        decode_history=[ 10.0, 20.0, 30.0, 24.0, 12.0 ],
        decode_avg=43.66,
        decode_now=40.0,
        prefill_now=420.0,
        prefill_avg=410.0,
        kv_usage=0.4,
        ttft_ms=150.0,
        spec_accept=0.55,
        active_slots=1,
        total_slots=2,
        running=2,
        waiting=1,
    )
    for key, value in overrides.items():
        setattr(state, key, value)
    return state


def test_big_number_lines_counts_and_glyphs():
    lines = big_number_lines("12.3")
    assert len(lines) == BIG_ROWS
    assert all(line.plain for line in lines)
    assert "█" in lines[-1].plain
    assert lines[0].style == "bold"


def test_headline_number_matches_reference_format():
    assert headline_number(43.66) == "43.7"
    assert headline_number(123.4) == "123"
    assert headline_number(1234.0) == "1234"


def test_card_label_shows_average_window():
    card = engine_card_renderable(_state(), width=100, avg_window=3.0)
    label = card.renderables[-1].plain
    assert "3s avg" in label
    card = engine_card_renderable(_state(), width=100, avg_window=120.0)
    assert "2m avg" in card.renderables[-1].plain


def test_meter_line_fills_proportionally():
    line = meter_line("DECODE", 30.0, 100.0, 10, "cyan")
    assert line.plain.startswith("DECODE  ")
    assert line.plain.count("█") == 3
    assert line.plain.count("░") == 7
    assert "30.0" in line.plain
    assert "/" not in line.plain


def test_meter_line_clamps_and_overrides_value_text():
    line = meter_line("KV", 2.0, 1.0, 8, "red", value_text="200%")
    assert line.plain.count("█") == 8
    assert "200%" in line.plain


def test_nice_max_rounds_up():
    assert nice_max(0) == 1.0
    assert nice_max(9) == 10.0
    assert nice_max(43) == 50.0
    assert nice_max(423) == 500.0
    assert nice_max(1200) == 2000.0
    assert nice_max(float("nan")) == 1.0
    assert nice_max(float("inf")) == 1.0


def test_headline_number_sanitizes():
    assert headline_number(float("nan")) == "0.0"
    assert headline_number(float("inf")) == "0.0"
    assert len(headline_number(1e9)) <= 4


def test_engine_card_online_and_offline():
    online = engine_card_renderable(_state(), width=100)
    offline = engine_card_renderable(_state(ok=False, error="connection refused"), width=100)
    assert isinstance(online, Group)
    assert len(online.renderables) == BIG_ROWS + 1
    assert len(offline.renderables) == 3


def test_engine_card_never_exceeds_width():
    # Regression: a card wider than its widget used to wrap and corrupt the screen.
    state = _state(prefill_avg=987654.0, ttft_ms=12345.6, decode_history=[ 1e6, 2e6 ])
    for width in (40, 60, 80, 100, 140):
        card = engine_card_renderable(state, width=width)
        for line in card.renderables:
            assert len(line.plain) <= width


def test_engine_card_survives_non_finite_values():
    card = engine_card_renderable(
        _state(
            decode_avg=float("nan"),
            decode_now=float("inf"),
            prefill_now=float("-inf"),
            kv_usage=float("nan"),
            ttft_ms=None,
            spec_accept=float("nan"),
            decode_history=[ float("nan"), float("inf") ],
        ),
        width=100,
    )
    for line in card.renderables:
        assert "nan" not in line.plain.lower()
        assert "inf" not in line.plain.lower()


def test_format_helpers():
    assert format_rate(999) == "999"
    assert format_rate(1500) == "1.5k"
    assert format_rate(15000) == "15k"
    assert format_ms(None) == "n/a"
    assert format_ms(950.0) == "950ms"
    assert format_ms(2500.0) == "2.5s"
    assert format_tokens(999) == "999"
    assert format_tokens(1500) == "1.5k"
    assert format_tokens(2_500_000) == "2.5M"
    assert format_age(0.5) == "now"
    assert format_age(45) == "45s"
    assert format_age(600) == "10m"
    assert format_age(7200) == "2.0h"
